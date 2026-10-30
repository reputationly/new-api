package relay

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	appconstant "github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// gpaWithHTTPClient 临时初始化共享 HTTP client，并在用例结束后恢复原值。
// 同一二进制里的其它用例（video_input_duration_test.go）依赖 client 为 nil 时的
// 不跟随重定向兜底，所以不能把初始化留在包级 init 里。
func gpaWithHTTPClient(t *testing.T) {
	t.Helper()
	old := service.GetHttpClient()
	if old == nil {
		service.InitHttpClient()
	}
	t.Cleanup(func() { service.SetHttpClientForTest(old) })
}

var gpaChannelID int64 = 9100

func gpaNextChannelID() int {
	return int(atomic.AddInt64(&gpaChannelID, 1))
}

// --- 假上游夹具 ---

type gpaWorker struct {
	srv        *httptest.Server
	port       int
	mu         sync.Mutex
	hits       int
	paths      []string
	requestIds []string
	reject     int
	release    chan struct{}
	started    chan struct{}
	once       sync.Once
}

func gpaNewWorker(t *testing.T) *gpaWorker {
	t.Helper()
	w := &gpaWorker{started: make(chan struct{})}
	w.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.mu.Lock()
		w.hits++
		w.paths = append(w.paths, r.URL.Path)
		w.requestIds = append(w.requestIds, r.Header.Get("X-Oneapi-Request-Id"))
		w.mu.Unlock()
		w.once.Do(func() { close(w.started) })

		if w.reject != 0 {
			rw.WriteHeader(w.reject)
			_, _ = rw.Write([]byte(`{"error":{"message":"worker rejected"}}`))
			return
		}

		var probe struct {
			Stream bool `json:"stream"`
		}
		_ = common.UnmarshalJsonStr(string(body), &probe)
		if probe.Stream {
			rw.Header().Set("Content-Type", "text/event-stream")
			rw.WriteHeader(http.StatusOK)
			gpaWriteSSE(rw, `{"id":"c1","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":"Hel"}}]}`)
			if f, ok := rw.(http.Flusher); ok {
				f.Flush()
			}
			if w.release != nil {
				<-w.release
			}
			gpaWriteSSE(rw, `{"id":"c1","choices":[{"index":0,"delta":{"content":"lo"}}]}`)
			gpaWriteSSE(rw, `{"id":"c1","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`)
			gpaWriteSSE(rw, `{"choices":[],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
			gpaWriteSSE(rw, `[DONE]`)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write([]byte(`{"id":"cmpl-1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	}))
	t.Cleanup(w.srv.Close)
	u, err := url.Parse(w.srv.URL)
	if err != nil {
		t.Fatalf("worker url: %v", err)
	}
	w.port, _ = strconv.Atoi(u.Port())
	return w
}

func gpaWriteSSE(w io.Writer, data string) {
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
}

func (w *gpaWorker) hitCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.hits
}

func (w *gpaWorker) pathsCopy() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.paths...)
}

func (w *gpaWorker) directURL() string {
	return fmt.Sprintf("http://127.0.0.1:%d", w.port)
}

func (w *gpaWorker) requestIdsCopy() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.requestIds...)
}

type gpaGateway struct {
	srv        *httptest.Server
	mu         sync.Mutex
	hits       int
	requestIds []string
	body       string
}

// gpaNewGateway 扮演 GPUStack 网关：同一个 Base URL 上既提供管理接口
// /v2/model-instances，也提供推理接口。生产里这两者本来就同源。
func gpaNewGateway(t *testing.T, workers []*gpaWorker, modelID int) *gpaGateway {
	t.Helper()
	items := make([]string, 0, len(workers))
	for i, w := range workers {
		items = append(items, fmt.Sprintf(
			`{"id":%d,"model_id":%d,"state":"running","model_name":"m","worker_ip":"127.0.0.1","port":%d}`,
			i+1, modelID, w.port))
	}
	listBody := fmt.Sprintf(`{"items":[%s],"pagination":{"page":1,"perPage":100,"total":%d,"totalPage":1}}`,
		strings.Join(items, ","), len(workers))
	g := &gpaGateway{body: `{"id":"cmpl-1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"message":{"role":"assistant","content":"gateway"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`}
	g.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/model-instances" {
			rw.Header().Set("Content-Type", "application/json")
			_, _ = rw.Write([]byte(listBody))
			return
		}
		g.mu.Lock()
		g.hits++
		g.requestIds = append(g.requestIds, r.Header.Get("X-Oneapi-Request-Id"))
		body := g.body
		g.mu.Unlock()
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write([]byte(body))
	}))
	t.Cleanup(g.srv.Close)
	return g
}

func (g *gpaGateway) hitCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.hits
}

func (g *gpaGateway) requestIdsCopy() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.requestIds...)
}

// --- 通用 ---

func gpaSetting(maxInflight int) dto.ChannelSettings {
	return dto.ChannelSettings{
		GPUStackAffinity:                 true,
		GPUStackAffinityKey:              "admin-key",
		GPUStackAffinityMaxInflight:      maxInflight,
		GPUStackAffinityMaxInflightBytes: -1,
	}
}

// warm 等异步的实例列表刷新落地。请求路径本身不等 I/O，所以测试要显式等。
func gpaWarm(t *testing.T, channelID int, adminURL string, setting dto.ChannelSettings) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := service.GPUStackAffinityBaseURL(nil, setting, channelID, adminURL, "m",
			&dto.GeneralOpenAIRequest{Messages: []dto.Message{{Role: "user", Content: "warm"}}})
		if got != "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("等 GPUStack 实例列表刷新超时")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func gpaContext(t *testing.T, path, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	return c, rec
}

func gpaResponsesInfo(channelID int, gatewayURL string, setting dto.ChannelSettings, stream bool) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeResponses,
		RelayFormat:     types.RelayFormatOpenAIResponses,
		OriginModelName: "m",
		IsStream:        stream,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:              appconstant.APITypeOpenAI,
			ChannelType:          appconstant.ChannelTypeOpenAI,
			ChannelId:            channelID,
			ChannelBaseUrl:       gatewayURL,
			ChannelSetting:       setting,
			UpstreamModelName:    "m",
			SupportStreamOptions: true,
		},
	}
}

func gpaCallResponses(t *testing.T, c *gin.Context, info *relaycommon.RelayInfo, req *dto.OpenAIResponsesRequest) (*dto.Usage, *types.NewAPIError) {
	t.Helper()
	adaptor := GetAdaptor(appconstant.APITypeOpenAI)
	if adaptor == nil {
		t.Fatal("openai adaptor 未注册")
	}
	adaptor.Init(info)
	return responsesViaChatCompletions(c, info, adaptor, req)
}

func gpaAssertSingleWorker(t *testing.T, workers []*gpaWorker, want int) *gpaWorker {
	t.Helper()
	var hit *gpaWorker
	for _, w := range workers {
		n := w.hitCount()
		if n == 0 {
			continue
		}
		if hit != nil {
			t.Fatalf("请求被分散到多个实例: %s 与 %s", hit.directURL(), w.directURL())
		}
		hit = w
	}
	if hit == nil {
		t.Fatal("没有任何实例收到请求")
	}
	if hit.hitCount() != want {
		t.Fatalf("实例 %s 收到 %d 次，期望 %d 次", hit.directURL(), hit.hitCount(), want)
	}
	return hit
}

func gpaAssertPaths(t *testing.T, w *gpaWorker, want string) {
	t.Helper()
	for _, p := range w.pathsCopy() {
		if p != want {
			t.Fatalf("直连路径 = %q，期望 %q", p, want)
		}
	}
}

// --- Responses 入口 ---

func TestGPUStackAffinityResponsesEntrySticksByPromptCacheKey(t *testing.T) {
	gpaWithHTTPClient(t)
	workers := []*gpaWorker{gpaNewWorker(t), gpaNewWorker(t), gpaNewWorker(t)}
	gateway := gpaNewGateway(t, workers, 701)
	channelID := gpaNextChannelID()
	setting := gpaSetting(-1)
	gpaWarm(t, channelID, gateway.srv.URL, setting)

	for turn := 1; turn <= 5; turn++ {
		c, _ := gpaContext(t, "/v1/responses", "{}")
		req := &dto.OpenAIResponsesRequest{
			Model:          "m",
			Input:          json.RawMessage(fmt.Sprintf(`"turn %d"`, turn)),
			PromptCacheKey: json.RawMessage(`"sess-r1"`),
		}
		if _, apiErr := gpaCallResponses(t, c, gpaResponsesInfo(channelID, gateway.srv.URL, setting, false), req); apiErr != nil {
			t.Fatalf("第 %d 轮失败: %v", turn, apiErr)
		}
	}
	hit := gpaAssertSingleWorker(t, workers, 5)
	gpaAssertPaths(t, hit, "/v1/chat/completions")
	if gateway.hitCount() != 0 {
		t.Fatalf("不应回退网关，实际 %d 次", gateway.hitCount())
	}
}

func TestGPUStackAffinityResponsesEntrySpreadsDifferentKeys(t *testing.T) {
	gpaWithHTTPClient(t)
	workers := []*gpaWorker{gpaNewWorker(t), gpaNewWorker(t), gpaNewWorker(t)}
	gateway := gpaNewGateway(t, workers, 702)
	channelID := gpaNextChannelID()
	setting := gpaSetting(-1)
	gpaWarm(t, channelID, gateway.srv.URL, setting)

	for i := 0; i < 20; i++ {
		c, _ := gpaContext(t, "/v1/responses", "{}")
		req := &dto.OpenAIResponsesRequest{
			Model:          "m",
			Input:          json.RawMessage(`"hi"`),
			PromptCacheKey: json.RawMessage(fmt.Sprintf(`"sess-%d"`, i)),
		}
		if _, apiErr := gpaCallResponses(t, c, gpaResponsesInfo(channelID, gateway.srv.URL, setting, false), req); apiErr != nil {
			t.Fatalf("第 %d 个会话失败: %v", i, apiErr)
		}
	}
	used := 0
	for _, w := range workers {
		if w.hitCount() > 0 {
			used++
		}
	}
	if used < 2 {
		t.Fatalf("20 个不同会话只用到 %d 个实例，分布塌缩", used)
	}
}

// 不带 prompt_cache_key 时走内容键；instructions 固定，前两条非空消息在
// 每一轮都不变，所以 5 轮必须命中同一实例。
func TestGPUStackAffinityResponsesEntrySticksByContentKey(t *testing.T) {
	gpaWithHTTPClient(t)
	workers := []*gpaWorker{gpaNewWorker(t), gpaNewWorker(t), gpaNewWorker(t)}
	gateway := gpaNewGateway(t, workers, 703)
	channelID := gpaNextChannelID()
	setting := gpaSetting(-1)
	gpaWarm(t, channelID, gateway.srv.URL, setting)

	input := `[{"type":"message","role":"user","content":[{"type":"input_text","text":"第一轮任务"}]}]`
	for turn := 1; turn <= 5; turn++ {
		c, _ := gpaContext(t, "/v1/responses", "{}")
		req := &dto.OpenAIResponsesRequest{
			Model:        "m",
			Input:        json.RawMessage(input),
			Instructions: json.RawMessage(`"固定的系统指令"`),
		}
		if _, apiErr := gpaCallResponses(t, c, gpaResponsesInfo(channelID, gateway.srv.URL, setting, false), req); apiErr != nil {
			t.Fatalf("第 %d 轮失败: %v", turn, apiErr)
		}
		// 后续轮次在客户端侧追加历史；前两条非空消息仍是 instructions + 首条 user。
		input = strings.TrimSuffix(input, "]") +
			`,{"type":"message","role":"assistant","content":[{"type":"output_text","text":"回答"}]},` +
			`{"type":"message","role":"user","content":[{"type":"input_text","text":"继续"}]}]`
	}
	hit := gpaAssertSingleWorker(t, workers, 5)
	gpaAssertPaths(t, hit, "/v1/chat/completions")
}

// 流式请求在飞时溢出到次选，结束后释放、回到首选。用黑盒方式验证账本：
// 只通过第二个请求选中的直连地址判断，不依赖未导出的账本函数。
func TestGPUStackAffinityResponsesEntryReleasesInflightAfterStream(t *testing.T) {
	gpaWithHTTPClient(t)
	release := make(chan struct{})
	workers := []*gpaWorker{gpaNewWorker(t), gpaNewWorker(t), gpaNewWorker(t)}
	for _, w := range workers {
		w.release = release
	}
	gateway := gpaNewGateway(t, workers, 704)
	channelID := gpaNextChannelID()
	setting := gpaSetting(1)
	gpaWarm(t, channelID, gateway.srv.URL, setting)

	c, _ := gpaContext(t, "/v1/responses", "{}")
	stream := true
	req := &dto.OpenAIResponsesRequest{
		Model:          "m",
		Input:          json.RawMessage(`"stream"`),
		PromptCacheKey: json.RawMessage(`"sess-r5"`),
		Stream:         &stream,
	}
	info := gpaResponsesInfo(channelID, gateway.srv.URL, setting, true)
	done := make(chan *types.NewAPIError, 1)
	go func() {
		_, err := gpaCallResponses(t, c, info, req)
		done <- err
	}()

	// 等到某个实例真的收到请求（此时在飞账已记上）。
	deadline := time.Now().Add(3 * time.Second)
	for {
		if gpaTotalHits(workers) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("等首个流式请求超时")
		}
		time.Sleep(5 * time.Millisecond)
	}
	first := gpaBusyWorker(t, workers)
	gpaAssertPaths(t, first, "/v1/chat/completions")

	c2, _ := gpaContext(t, "/v1/responses", "{}")
	spillReq := &dto.GeneralOpenAIRequest{
		Messages:       []dto.Message{{Role: "user", Content: "stream"}},
		PromptCacheKey: "sess-r5",
	}
	spillURL := service.GPUStackAffinityBaseURL(c2, setting, channelID, gateway.srv.URL, "m", spillReq)
	service.GPUStackAffinityDone(c2)
	if spillURL == "" {
		t.Fatal("在飞时第二个请求应拿到直连地址")
	}
	if spillURL == first.directURL() {
		t.Fatalf("首选实例在飞已满，第二个请求仍选中它: %s", spillURL)
	}

	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("流式请求失败: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("流式请求未结束")
	}

	c3, _ := gpaContext(t, "/v1/responses", "{}")
	backURL := service.GPUStackAffinityBaseURL(c3, setting, channelID, gateway.srv.URL, "m", spillReq)
	service.GPUStackAffinityDone(c3)
	if backURL != first.directURL() {
		t.Fatalf("请求结束后应回到首选 %s，实际 %s（账本没释放）", first.directURL(), backURL)
	}
}

// 直连 503 必须回退网关，而且账本要释放：第二次同键请求仍应打同一台。
func TestGPUStackAffinityResponsesEntryFallsBackAndReleasesOnDirectFailure(t *testing.T) {
	gpaWithHTTPClient(t)
	workers := []*gpaWorker{gpaNewWorker(t), gpaNewWorker(t)}
	for _, w := range workers {
		w.reject = http.StatusServiceUnavailable
	}
	gateway := gpaNewGateway(t, workers, 705)
	channelID := gpaNextChannelID()
	setting := gpaSetting(1)
	gpaWarm(t, channelID, gateway.srv.URL, setting)

	for turn := 0; turn < 2; turn++ {
		c, _ := gpaContext(t, "/v1/responses", "{}")
		req := &dto.OpenAIResponsesRequest{
			Model:          "m",
			Input:          json.RawMessage(`"x"`),
			PromptCacheKey: json.RawMessage(`"sess-r6"`),
		}
		if _, apiErr := gpaCallResponses(t, c, gpaResponsesInfo(channelID, gateway.srv.URL, setting, false), req); apiErr != nil {
			t.Fatalf("第 %d 次失败: %v", turn, apiErr)
		}
	}
	if gateway.hitCount() != 2 {
		t.Fatalf("网关应收到 2 次回退请求，实际 %d 次", gateway.hitCount())
	}
	// 两次都先直连同一台（首选），失败后回退；若账本泄漏，第二次会溢出到另一台。
	if workers[0].hitCount() > 0 && workers[1].hitCount() > 0 {
		t.Fatalf("两次同键请求被分散到两台实例，账本未释放: %d / %d",
			workers[0].hitCount(), workers[1].hitCount())
	}
	if gpaTotalHits(workers) != 2 {
		t.Fatalf("两台实例合计应收到 2 次直连，实际 %d", gpaTotalHits(workers))
	}
}

func TestGPUStackAffinityResponsesCompactDoesNotAffinity(t *testing.T) {
	gpaWithHTTPClient(t)
	workers := []*gpaWorker{gpaNewWorker(t), gpaNewWorker(t)}
	gateway := gpaNewGateway(t, workers, 706)
	channelID := gpaNextChannelID()
	setting := gpaSetting(-1)
	gpaWarm(t, channelID, gateway.srv.URL, setting)

	c, _ := gpaContext(t, "/v1/responses/compact", "{}")
	info := gpaResponsesInfo(channelID, gateway.srv.URL, setting, false)
	info.RelayMode = relayconstant.RelayModeResponsesCompact
	adaptor := GetAdaptor(appconstant.APITypeOpenAI)
	adaptor.Init(info)
	req := &dto.OpenAIResponsesRequest{
		Model: "m",
		Input: json.RawMessage(`[{"type":"message","role":"user","content":[{"type":"input_text","text":"compact me"}]}]`),
	}
	if _, apiErr := responsesCompactViaChatCompletions(c, info, adaptor, req); apiErr != nil {
		t.Fatalf("compact 失败: %v", apiErr)
	}
	if gpaTotalHits(workers) != 0 {
		t.Fatalf("compact 不应直连实例，实际 %d 次", gpaTotalHits(workers))
	}
	if gateway.hitCount() != 1 {
		t.Fatalf("compact 应走网关 1 次，实际 %d 次", gateway.hitCount())
	}
}

func TestGPUStackAffinityResponsesPreviousResponseIDRejectedBeforeAffinity(t *testing.T) {
	gpaWithHTTPClient(t)
	workers := []*gpaWorker{gpaNewWorker(t)}
	gateway := gpaNewGateway(t, workers, 707)
	channelID := gpaNextChannelID()
	setting := gpaSetting(-1)
	gpaWarm(t, channelID, gateway.srv.URL, setting)

	c, _ := gpaContext(t, "/v1/responses", "{}")
	req := &dto.OpenAIResponsesRequest{
		Model:              "m",
		Input:              json.RawMessage(`"x"`),
		PreviousResponseID: "resp_1",
	}
	if _, apiErr := gpaCallResponses(t, c, gpaResponsesInfo(channelID, gateway.srv.URL, setting, false), req); apiErr == nil {
		t.Fatal("previous_response_id 应该被拒绝")
	}
	if gpaTotalHits(workers) != 0 || gateway.hitCount() != 0 {
		t.Fatalf("被拒绝的请求不该发出: workers=%d gateway=%d", gpaTotalHits(workers), gateway.hitCount())
	}
}

// --- Claude 入口 ---

func gpaClaudeContext(t *testing.T, channelID int, gatewayURL string, channelType int, setting dto.ChannelSettings, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	c, rec := gpaContext(t, "/v1/messages", body)
	c.Set(common.KeyRequestBody, []byte(body))
	common.SetContextKey(c, appconstant.ContextKeyChannelType, channelType)
	common.SetContextKey(c, appconstant.ContextKeyChannelId, channelID)
	common.SetContextKey(c, appconstant.ContextKeyChannelBaseUrl, gatewayURL)
	common.SetContextKey(c, appconstant.ContextKeyChannelKey, "sk-test")
	common.SetContextKey(c, appconstant.ContextKeyChannelSetting, setting)
	common.SetContextKey(c, appconstant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{})
	common.SetContextKey(c, appconstant.ContextKeyOriginalModel, "m")
	return c, rec
}

// gpaWithBillingSafe 让 PostTextConsumeQuota 不碰数据库：批量更新走内存，
// 消费日志关闭，价格为零（结算差额为 0）。
func gpaWithBillingSafe(t *testing.T) {
	t.Helper()
	oldBatch, oldLog := common.BatchUpdateEnabled, common.LogConsumeEnabled
	common.BatchUpdateEnabled = true
	common.LogConsumeEnabled = false
	t.Cleanup(func() {
		common.BatchUpdateEnabled = oldBatch
		common.LogConsumeEnabled = oldLog
	})
}

func gpaClaudeRequest(t *testing.T, system string, turns ...dto.ClaudeMessage) *dto.ClaudeRequest {
	t.Helper()
	req := &dto.ClaudeRequest{Model: "m"}
	sys := system
	req.System = []dto.ClaudeMediaMessage{{Type: "text", Text: &sys, CacheControl: []byte(`{"type":"ephemeral"}`)}}
	req.Messages = turns
	return req
}

func gpaClaudeUser(text string) dto.ClaudeMessage {
	t := text
	return dto.ClaudeMessage{Role: "user", Content: []dto.ClaudeMediaMessage{{
		Type: "text", Text: &t, CacheControl: []byte(`{"type":"ephemeral"}`),
	}}}
}

func gpaClaudeAssistant(text string) dto.ClaudeMessage {
	t := text
	return dto.ClaudeMessage{Role: "assistant", Content: []dto.ClaudeMediaMessage{{Type: "text", Text: &t}}}
}

func gpaCallClaude(t *testing.T, c *gin.Context, info *relaycommon.RelayInfo, req *dto.ClaudeRequest) *types.NewAPIError {
	t.Helper()
	info.Request = req
	return ClaudeHelper(c, info)
}

func gpaClaudeInfo(channelID int, stream bool) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		RelayFormat:     types.RelayFormatClaude,
		OriginModelName: "m",
		IsStream:        stream,
		StartTime:       time.Now(),
		ClaudeConvertInfo: &relaycommon.ClaudeConvertInfo{
			LastMessagesType: relaycommon.LastMessageTypeNone,
		},
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channelID},
	}
}

func TestGPUStackAffinityClaudeEntrySticksAcrossTurnsWithTools(t *testing.T) {
	gpaWithHTTPClient(t)
	gpaWithBillingSafe(t)
	workers := []*gpaWorker{gpaNewWorker(t), gpaNewWorker(t), gpaNewWorker(t)}
	gateway := gpaNewGateway(t, workers, 801)
	channelID := gpaNextChannelID()
	setting := gpaSetting(-1)
	gpaWarm(t, channelID, gateway.srv.URL, setting)

	history := []dto.ClaudeMessage{gpaClaudeUser("修复登录 bug")}
	for turn := 1; turn <= 5; turn++ {
		body := fmt.Sprintf(`{"model":"m","messages":[],"turn":%d}`, turn)
		c, _ := gpaClaudeContext(t, channelID, gateway.srv.URL, appconstant.ChannelTypeOpenAI, setting, body)
		req := gpaClaudeRequest(t, "共享系统提示", history...)
		if apiErr := gpaCallClaude(t, c, gpaClaudeInfo(channelID, false), req); apiErr != nil {
			t.Fatalf("第 %d 轮失败: %v", turn, apiErr)
		}
		if turn == 3 {
			// 工具往返：assistant tool_use + user tool_result 追加在历史尾部。
			history = append(history,
				dto.ClaudeMessage{Role: "assistant", Content: []dto.ClaudeMediaMessage{{
					Type: "tool_use", Id: "t1", Name: "bash", Input: map[string]any{"cmd": "ls"},
				}}},
				dto.ClaudeMessage{Role: "user", Content: []dto.ClaudeMediaMessage{{
					Type: "tool_result", ToolUseId: "t1", Content: "file.txt",
				}}},
			)
		} else {
			history = append(history, gpaClaudeAssistant(fmt.Sprintf("回答 %d", turn)), gpaClaudeUser(fmt.Sprintf("继续 %d", turn)))
		}
	}
	hit := gpaAssertSingleWorker(t, workers, 5)
	gpaAssertPaths(t, hit, "/v1/chat/completions")
	if gateway.hitCount() != 0 {
		t.Fatalf("不应回退网关，实际 %d 次", gateway.hitCount())
	}
}

func TestGPUStackAffinityClaudeEntryDoesNotCollapseOnSharedSystem(t *testing.T) {
	gpaWithHTTPClient(t)
	gpaWithBillingSafe(t)
	workers := []*gpaWorker{gpaNewWorker(t), gpaNewWorker(t), gpaNewWorker(t)}
	gateway := gpaNewGateway(t, workers, 802)
	channelID := gpaNextChannelID()
	setting := gpaSetting(-1)
	gpaWarm(t, channelID, gateway.srv.URL, setting)

	for i := 0; i < 20; i++ {
		c, _ := gpaClaudeContext(t, channelID, gateway.srv.URL, appconstant.ChannelTypeOpenAI, setting, "{}")
		req := gpaClaudeRequest(t, "共享系统提示", gpaClaudeUser(fmt.Sprintf("任务 %d", i)))
		if apiErr := gpaCallClaude(t, c, gpaClaudeInfo(channelID, false), req); apiErr != nil {
			t.Fatalf("第 %d 个会话失败: %v", i, apiErr)
		}
	}
	used := 0
	for _, w := range workers {
		if w.hitCount() > 0 {
			used++
		}
	}
	if used < 2 {
		t.Fatalf("20 个不同会话只用到 %d 个实例，说明按 system 塌缩了", used)
	}
}

func TestGPUStackAffinityClaudeEntryIgnoresMetadataUserID(t *testing.T) {
	gpaWithHTTPClient(t)
	gpaWithBillingSafe(t)
	workers := []*gpaWorker{gpaNewWorker(t), gpaNewWorker(t)}
	gateway := gpaNewGateway(t, workers, 803)
	channelID := gpaNextChannelID()
	setting := gpaSetting(-1)
	gpaWarm(t, channelID, gateway.srv.URL, setting)

	var chosen string
	for i := 0; i < 2; i++ {
		c, _ := gpaClaudeContext(t, channelID, gateway.srv.URL, appconstant.ChannelTypeOpenAI, setting, "{}")
		req := gpaClaudeRequest(t, "共享系统提示", gpaClaudeUser("同一段对话"))
		req.Metadata = []byte(fmt.Sprintf(`{"user_id":"user-%d"}`, i))
		if apiErr := gpaCallClaude(t, c, gpaClaudeInfo(channelID, false), req); apiErr != nil {
			t.Fatalf("第 %d 次失败: %v", i, apiErr)
		}
		hit := gpaBusyWorker(t, workers)
		if chosen == "" {
			chosen = hit.directURL()
		} else if hit.directURL() != chosen {
			t.Fatalf("metadata.user_id 变化不该换实例: %s -> %s", chosen, hit.directURL())
		}
	}
}

func TestGPUStackAffinityClaudeEntryPassThroughGoesToGateway(t *testing.T) {
	gpaWithHTTPClient(t)
	gpaWithBillingSafe(t)
	workers := []*gpaWorker{gpaNewWorker(t)}
	gateway := gpaNewGateway(t, workers, 804)
	channelID := gpaNextChannelID()
	setting := gpaSetting(-1)
	setting.PassThroughBodyEnabled = true
	gpaWarm(t, channelID, gateway.srv.URL, setting)

	c, _ := gpaClaudeContext(t, channelID, gateway.srv.URL, appconstant.ChannelTypeOpenAI, setting, `{"model":"m"}`)
	req := gpaClaudeRequest(t, "共享系统提示", gpaClaudeUser("hello"))
	if apiErr := gpaCallClaude(t, c, gpaClaudeInfo(channelID, false), req); apiErr != nil {
		t.Fatalf("透传请求失败: %v", apiErr)
	}
	if gpaTotalHits(workers) != 0 {
		t.Fatalf("透传请求不该直连实例，实际 %d 次", gpaTotalHits(workers))
	}
	if gateway.hitCount() != 1 {
		t.Fatalf("透传请求应走网关 1 次，实际 %d 次", gateway.hitCount())
	}
}

// Claude 流式请求在飞时占用账本，结束后释放。
func TestGPUStackAffinityClaudeEntryReleasesInflightAfterStream(t *testing.T) {
	gpaWithHTTPClient(t)
	gpaWithBillingSafe(t)
	release := make(chan struct{})
	workers := []*gpaWorker{gpaNewWorker(t), gpaNewWorker(t), gpaNewWorker(t)}
	for _, w := range workers {
		w.release = release
	}
	gateway := gpaNewGateway(t, workers, 805)
	channelID := gpaNextChannelID()
	setting := gpaSetting(1)
	gpaWarm(t, channelID, gateway.srv.URL, setting)

	c, _ := gpaClaudeContext(t, channelID, gateway.srv.URL, appconstant.ChannelTypeOpenAI, setting, "{}")
	req := gpaClaudeRequest(t, "共享系统提示", gpaClaudeUser("流式请求"))
	req.Stream = common.GetPointer(true)
	info := gpaClaudeInfo(channelID, true)
	done := make(chan *types.NewAPIError, 1)
	go func() {
		done <- gpaCallClaude(t, c, info, req)
	}()

	deadline := time.Now().Add(3 * time.Second)
	for {
		if gpaTotalHits(workers) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("等首个 Claude 流式请求超时")
		}
		time.Sleep(5 * time.Millisecond)
	}
	first := gpaBusyWorker(t, workers)
	gpaAssertPaths(t, first, "/v1/chat/completions")

	// 用真实转换结果复现同一个亲和键。
	converted, err := service.ClaudeToOpenAIRequest(*req, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: appconstant.ChannelTypeOpenAI},
	})
	if err != nil {
		t.Fatalf("转换失败: %v", err)
	}
	c2, _ := gpaContext(t, "/v1/messages", "{}")
	spillURL := service.GPUStackAffinityBaseURL(c2, setting, channelID, gateway.srv.URL, "m", converted)
	service.GPUStackAffinityDone(c2)
	if spillURL == "" || spillURL == first.directURL() {
		t.Fatalf("在飞时第二个请求应溢出到次选，first=%s spill=%s", first.directURL(), spillURL)
	}

	close(release)
	select {
	case apiErr := <-done:
		if apiErr != nil {
			t.Fatalf("Claude 流式请求失败: %v", apiErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Claude 流式请求未结束")
	}

	c3, _ := gpaContext(t, "/v1/messages", "{}")
	backURL := service.GPUStackAffinityBaseURL(c3, setting, channelID, gateway.srv.URL, "m", converted)
	service.GPUStackAffinityDone(c3)
	if backURL != first.directURL() {
		t.Fatalf("结束后应回到首选 %s，实际 %s（账本没释放）", first.directURL(), backURL)
	}
}

func gpaTotalHits(workers []*gpaWorker) int {
	total := 0
	for _, w := range workers {
		total += w.hitCount()
	}
	return total
}

func gpaBusyWorker(t *testing.T, workers []*gpaWorker) *gpaWorker {
	t.Helper()
	var hit *gpaWorker
	for _, w := range workers {
		if w.hitCount() > 0 {
			if hit != nil {
				t.Fatalf("多个实例同时收到请求: %s / %s", hit.directURL(), w.directURL())
			}
			hit = w
		}
	}
	if hit == nil {
		t.Fatal("没有实例收到请求")
	}
	return hit
}

// 原生 Claude 渠道（非 OpenAI-wire）即便打开开关也不能改写目标地址：
// ConvertClaudeRequest 返回的不是 GeneralOpenAIRequest，亲和直接跳过。
func TestGPUStackAffinityClaudeNativeChannelDoesNotRewrite(t *testing.T) {
	gpaWithHTTPClient(t)
	gpaWithBillingSafe(t)
	workers := []*gpaWorker{gpaNewWorker(t)}
	gateway := gpaNewGateway(t, workers, 806)
	gateway.body = `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"model":"m","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`
	channelID := gpaNextChannelID()
	setting := gpaSetting(-1)
	gpaWarm(t, channelID, gateway.srv.URL, setting)

	c, _ := gpaClaudeContext(t, channelID, gateway.srv.URL, appconstant.ChannelTypeAnthropic, setting, `{"model":"m"}`)
	req := gpaClaudeRequest(t, "共享系统提示", gpaClaudeUser("hello"))
	if apiErr := gpaCallClaude(t, c, gpaClaudeInfo(channelID, false), req); apiErr != nil {
		t.Fatalf("原生 Claude 请求失败: %v", apiErr)
	}
	if gpaTotalHits(workers) != 0 {
		t.Fatalf("原生 Claude 渠道不该直连 GPUStack 实例，实际 %d 次", gpaTotalHits(workers))
	}
	if gateway.hitCount() != 1 {
		t.Fatalf("应走网关 1 次，实际 %d 次", gateway.hitCount())
	}
}

// R4：渠道开关关闭时，Responses / Claude 两条入口都必须走网关，实例一次都不碰。
// 这条防的是「只在非空时写」的回退，或者绕过了 GPUStackAffinity 的开关判断。
func TestGPUStackAffinitySwitchOffGoesToGateway(t *testing.T) {
	gpaWithHTTPClient(t)
	gpaWithBillingSafe(t)

	off := dto.ChannelSettings{
		GPUStackAffinity:                 false,
		GPUStackAffinityKey:              "admin-key",
		GPUStackAffinityMaxInflight:      -1,
		GPUStackAffinityMaxInflightBytes: -1,
	}

	t.Run("responses", func(t *testing.T) {
		workers := []*gpaWorker{gpaNewWorker(t), gpaNewWorker(t)}
		gateway := gpaNewGateway(t, workers, 901)
		channelID := gpaNextChannelID()
		// 先把实例列表预热好：否则「绕过开关」也会因为缓存冷而返回空串，
		// 这条测试就退化成恒真，测不出开关被绕过。
		warm := off
		warm.GPUStackAffinity = true
		gpaWarm(t, channelID, gateway.srv.URL, warm)

		c, _ := gpaContext(t, "/v1/responses", "{}")
		req := &dto.OpenAIResponsesRequest{
			Model:          "m",
			Input:          json.RawMessage(`"x"`),
			PromptCacheKey: json.RawMessage(`"sess-off"`),
		}
		if _, apiErr := gpaCallResponses(t, c, gpaResponsesInfo(channelID, gateway.srv.URL, off, false), req); apiErr != nil {
			t.Fatalf("开关关闭时请求失败: %v", apiErr)
		}
		if got := gpaTotalHits(workers); got != 0 {
			t.Fatalf("开关关闭时实例不该收到请求，实际 %d 次", got)
		}
		if got := gateway.hitCount(); got != 1 {
			t.Fatalf("开关关闭时应走网关 1 次，实际 %d 次", got)
		}
	})

	t.Run("claude", func(t *testing.T) {
		workers := []*gpaWorker{gpaNewWorker(t), gpaNewWorker(t)}
		gateway := gpaNewGateway(t, workers, 902)
		channelID := gpaNextChannelID()
		warm := off
		warm.GPUStackAffinity = true
		gpaWarm(t, channelID, gateway.srv.URL, warm)

		c, _ := gpaClaudeContext(t, channelID, gateway.srv.URL, appconstant.ChannelTypeOpenAI, off, "{}")
		req := gpaClaudeRequest(t, "共享系统提示", gpaClaudeUser("hello"))
		if apiErr := gpaCallClaude(t, c, gpaClaudeInfo(channelID, false), req); apiErr != nil {
			t.Fatalf("开关关闭时请求失败: %v", apiErr)
		}
		if got := gpaTotalHits(workers); got != 0 {
			t.Fatalf("开关关闭时实例不该收到请求，实际 %d 次", got)
		}
		if got := gateway.hitCount(); got != 1 {
			t.Fatalf("开关关闭时应走网关 1 次，实际 %d 次", got)
		}
	})
}

// 亲和直连与回退网关两条路径，都必须把 new-api 的 Request ID 带给上游。
func TestGPUStackAffinityForwardsRequestIdOnDirectAndFallback(t *testing.T) {
	t.Run("直连", func(t *testing.T) {
		gpaWithHTTPClient(t)
		workers := []*gpaWorker{gpaNewWorker(t), gpaNewWorker(t)}
		gateway := gpaNewGateway(t, workers, 720)
		channelID := gpaNextChannelID()
		setting := gpaSetting(-1)
		gpaWarm(t, channelID, gateway.srv.URL, setting)

		c, _ := gpaContext(t, "/v1/responses", "{}")
		info := gpaResponsesInfo(channelID, gateway.srv.URL, setting, false)
		info.RequestId = "rid-direct"
		info.ChannelMeta.HeadersOverride = map[string]any{
			"X-Oneapi-Request-Id": "{request_id}",
		}
		req := &dto.OpenAIResponsesRequest{
			Model:          "m",
			Input:          json.RawMessage(`"hi"`),
			PromptCacheKey: json.RawMessage(`"sess-rid-direct"`),
		}
		if _, apiErr := gpaCallResponses(t, c, info, req); apiErr != nil {
			t.Fatalf("直连请求失败: %v", apiErr)
		}

		hit := gpaAssertSingleWorker(t, workers, 1)
		if got := hit.requestIdsCopy(); len(got) != 1 || got[0] != "rid-direct" {
			t.Fatalf("直连实例收到的 Request ID = %v，期望 [rid-direct]", got)
		}
		if got := gateway.hitCount(); got != 0 {
			t.Fatalf("直连不该走网关，网关实际命中 %d 次", got)
		}
		if got := gateway.requestIdsCopy(); len(got) != 0 {
			t.Fatalf("网关不该收到推理请求，实际 %v", got)
		}
	})

	t.Run("回退", func(t *testing.T) {
		gpaWithHTTPClient(t)
		workers := []*gpaWorker{gpaNewWorker(t), gpaNewWorker(t)}
		for _, w := range workers {
			w.reject = http.StatusServiceUnavailable
		}
		gateway := gpaNewGateway(t, workers, 721)
		channelID := gpaNextChannelID()
		setting := gpaSetting(1)
		gpaWarm(t, channelID, gateway.srv.URL, setting)

		c, _ := gpaContext(t, "/v1/responses", "{}")
		info := gpaResponsesInfo(channelID, gateway.srv.URL, setting, false)
		info.RequestId = "rid-fallback"
		info.ChannelMeta.HeadersOverride = map[string]any{
			"X-Oneapi-Request-Id": "{request_id}",
		}
		req := &dto.OpenAIResponsesRequest{
			Model:          "m",
			Input:          json.RawMessage(`"hi"`),
			PromptCacheKey: json.RawMessage(`"sess-rid-fallback"`),
		}
		if _, apiErr := gpaCallResponses(t, c, info, req); apiErr != nil {
			t.Fatalf("回退请求失败: %v", apiErr)
		}

		// 直连那次被实例 503 拒掉，回退那次打到网关，两处都该带上同一个 ID。
		if got := gpaTotalHits(workers); got != 1 {
			t.Fatalf("应有一次失败的直连尝试，实际 %d 次", got)
		}
		direct := gpaBusyWorker(t, workers)
		if got := direct.requestIdsCopy(); len(got) != 1 || got[0] != "rid-fallback" {
			t.Fatalf("直连尝试收到的 Request ID = %v，期望 [rid-fallback]", got)
		}
		if got := gateway.requestIdsCopy(); len(got) != 1 || got[0] != "rid-fallback" {
			t.Fatalf("网关收到的 Request ID = %v，期望 [rid-fallback]", got)
		}
	})
}
