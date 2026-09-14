package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/dto"
)

// GetHttpClient() 返回的是由 InitHttpClient() 填充的包级变量；生产在启动时调用，
// 测试里要自己补上，否则 fetch 直接返回「client 未初始化」而不是走 HTTP。
func init() {
	InitHttpClient()
}

func resetGPUStackCache() {
	gpustackInstanceMu.Lock()
	defer gpustackInstanceMu.Unlock()
	gpustackInstanceCache = map[string][]GPUStackInstance{}
	gpustackInstanceFetched = map[string]time.Time{}
	gpustackInstanceLoading = map[string]bool{}
	gpustackInstanceFailedAt = map[string]time.Time{}
}

// waitForInstances 等后台刷新落地。刷新是异步的（请求路径不等 I/O），所以测试要
// 显式等一下，而不是假设第一次调用就有结果。
func waitForInstances(
	t *testing.T, channelID int, baseURL, key string, want int,
) []GPUStackInstance {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := gpustackRunningInstances(channelID, baseURL, key)
		if len(got) == want {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("等后台刷新超时：期望 %d 个实例，最后一次拿到 %d 个", want, len(got))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// 响应体形状来自 gpustack 的 PaginatedList：{"items": [...], "pagination": {...}}。
// 同时验证鉴权头、查询参数，以及三重筛选。
func TestFetchInstancesParsesGatewayShapeAndFiltersByModel(t *testing.T) {
	var gotAuth, gotQuery, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotQuery, gotPath = r.Header.Get("Authorization"), r.URL.RawQuery, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
		  "items": [
		    {"id": 23, "model_id": 7, "state": "running",  "model_name": "deepseek-v4-flash"},
		    {"id": 24, "model_id": 7, "state": "starting", "model_name": "deepseek-v4-flash"},
		    {"id": 25, "model_id": 7, "state": "running",  "model_name": "deepseek-v4-flash"},
		    {"id": 0,  "model_id": 7, "state": "running",  "model_name": "deepseek-v4-flash"},
		    {"id": 99, "model_id": 8, "state": "running",  "model_name": "qwen-image"}
		  ],
		  "pagination": {"page": 1, "perPage": 100, "total": 5, "totalPage": 1}
		}`))
	}))
	defer srv.Close()

	got, err := fetchGPUStackInstances(srv.URL, "secret-key")
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if gotPath != "/v2/model-instances" {
		t.Errorf("请求路径 = %q，期望 /v2/model-instances", gotPath)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("Authorization = %q，期望 Bearer secret-key", gotAuth)
	}
	// state 交服务端过滤，page/perPage 必须带上——只读第一页会在大机队上筛出空列表。
	for _, want := range []string{"state=running", "page=1", "perPage=100"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query = %q，缺少 %s", gotQuery, want)
		}
	}
	// fetch 只做 state 与 id 合法性两道筛（模型筛在取用时做，见下）：
	// starting 的被 state 挡掉，id=0 的拼不出合法路由头，剩 23/25/99。
	if len(got) != 3 {
		t.Fatalf("fetch 应保留 3 个 state/id 合法的实例，实际 %d 个: %+v", len(got), got)
	}

	// 模型筛必须把别的模型挡掉——选中 qwen-image 的实例会把文本请求路由到一个
	// 根本不提供该模型的进程上，那不是「优化失效」，是把请求打坏。
	mine := gpustackInstancesForModel(got, "deepseek-v4-flash")
	if len(mine) != 2 {
		t.Fatalf("按模型筛后应剩 2 个，实际 %d 个: %+v", len(mine), mine)
	}
	for _, inst := range mine {
		if inst.ModelName != "deepseek-v4-flash" {
			t.Errorf("混进了别的模型的实例: %+v", inst)
		}
	}
	if mine[0].RouteHeaderValue() != "model-7-23.static" {
		t.Errorf("路由头 = %q，期望 model-7-23.static", mine[0].RouteHeaderValue())
	}
}

func TestFetchInstancesSurfacesHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"detail":"Forbidden"}`))
	}))
	defer srv.Close()

	if _, err := fetchGPUStackInstances(srv.URL, "k"); err == nil {
		t.Fatal("403 应当报错，否则运营填错 key 时会静默退回随机分流")
	}
}

// 请求路径不能等 I/O：缓存冷时第一次调用必须立刻返回空，而不是同步去拉。
// 代价是该渠道的头几个请求没有亲和——这是刻意的取舍，所以在这里固定住。
func TestRunningInstancesNeverBlocksOnColdCache(t *testing.T) {
	resetGPUStackCache()
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release // 模拟一个很慢的管理 API
		_, _ = w.Write([]byte(`{"items":[{"id":1,"model_id":7,"state":"running","model_name":"m"}]}`))
	}))
	defer srv.Close()
	defer close(release)

	start := time.Now()
	got := gpustackRunningInstances(10, srv.URL, "k")
	elapsed := time.Since(start)

	if len(got) != 0 {
		t.Errorf("缓存冷时应立刻返回空，实际 %+v", got)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("请求路径被 I/O 阻塞了 %v —— 亲和绝不能给用户请求加延迟", elapsed)
	}
}

// 单飞：管理 API 很慢时，大量并发请求只应触发一次拉取，否则是惊群。
func TestRunningInstancesSingleFlights(t *testing.T) {
	resetGPUStackCache()
	var hits int64
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		<-release
		_, _ = w.Write([]byte(`{"items":[{"id":1,"model_id":7,"state":"running","model_name":"m"}]}`))
	}))
	defer srv.Close()

	for i := 0; i < 50; i++ {
		gpustackRunningInstances(11, srv.URL, "k")
	}
	time.Sleep(100 * time.Millisecond)
	if n := atomic.LoadInt64(&hits); n != 1 {
		t.Errorf("50 个并发请求应只触发 1 次拉取，实际 %d 次", n)
	}
	close(release)
}

// TTL 内不再重复拉取：实例列表分钟级才变，每请求打一次会把 gpustack 打满。
func TestRunningInstancesCachesWithinTTL(t *testing.T) {
	resetGPUStackCache()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		_, _ = w.Write([]byte(`{"items":[{"id":1,"model_id":7,"state":"running","model_name":"m"}]}`))
	}))
	defer srv.Close()

	waitForInstances(t, 12, srv.URL, "k", 1)
	for i := 0; i < 20; i++ {
		if got := gpustackRunningInstances(12, srv.URL, "k"); len(got) != 1 {
			t.Fatalf("第 %d 次应命中缓存，实际 %d 个", i, len(got))
		}
	}
	if n := atomic.LoadInt64(&hits); n != 1 {
		t.Errorf("TTL 内应只拉取一次，实际 %d 次", n)
	}
}

// 拉取失败时沿用旧列表：「按旧列表钉住」远好于「退回随机分流」——后者会让整个
// 机队的前缀缓存同时失效。
func TestRunningInstancesKeepsStaleOnFailure(t *testing.T) {
	resetGPUStackCache()
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"id":42,"model_id":7,"state":"running","model_name":"m"}]}`))
	}))
	defer srv.Close()

	waitForInstances(t, 13, srv.URL, "k", 1)

	gpustackInstanceMu.Lock()
	for k := range gpustackInstanceFetched {
		gpustackInstanceFetched[k] = time.Now().Add(-time.Hour)
	}
	gpustackInstanceMu.Unlock()
	fail.Store(true)

	// 触发一次后台刷新（会失败），等它跑完，旧列表应当还在。
	gpustackRunningInstances(13, srv.URL, "k")
	time.Sleep(300 * time.Millisecond)

	got := gpustackRunningInstances(13, srv.URL, "k")
	if len(got) != 1 || got[0].ID != 42 {
		t.Errorf("拉取失败时应沿用旧列表，实际 %+v", got)
	}
}

// 从未成功过就一直是空 → 上层不发头 → 退回 gpustack 自己的分流。
func TestRunningInstancesEmptyWhenNeverFetched(t *testing.T) {
	resetGPUStackCache()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	gpustackRunningInstances(14, srv.URL, "bad")
	time.Sleep(300 * time.Millisecond)
	if got := gpustackRunningInstances(14, srv.URL, "bad"); len(got) != 0 {
		t.Errorf("从未成功拉取时应为空，实际 %+v", got)
	}
}

// 端到端（打到假 gpustack）：同一段对话的多轮必须拿到同一个实例头。
func TestAffinityHeaderEndToEndIsStableAcrossTurns(t *testing.T) {
	resetGPUStackCache()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[
		  {"id":1,"model_id":7,"state":"running","model_name":"m"},
		  {"id":2,"model_id":7,"state":"running","model_name":"m"},
		  {"id":3,"model_id":7,"state":"running","model_name":"m"},
		  {"id":4,"model_id":7,"state":"running","model_name":"m"},
		  {"id":5,"model_id":7,"state":"running","model_name":"m"}
		]}`))
	}))
	defer srv.Close()

	waitForInstances(t, 15, srv.URL, "k", 5)

	setting := dto.ChannelSettings{GPUStackAffinity: true, GPUStackAffinityKey: "k"}
	doc := "一份用于问答的长文档，内容在多轮之间不变。"
	turn2 := []dto.Message{msg("user", doc), msg("assistant", "答一"), msg("user", "再问")}
	turn3 := append(append([]dto.Message{}, turn2...), msg("assistant", "答二"), msg("user", "三问"))

	h2 := GPUStackAffinityHeader(setting, 15, srv.URL, "m", turn2)
	h3 := GPUStackAffinityHeader(setting, 15, srv.URL, "m", turn3)
	if h2 == "" {
		t.Fatal("应算出实例头")
	}
	if h2 != h3 {
		t.Errorf("同一段对话的后续轮次必须落到同一实例: %q vs %q", h2, h3)
	}

	// 不同对话要分散开，而不是全钉到一台。
	seen := map[string]bool{}
	for i := 0; i < 60; i++ {
		other := []dto.Message{msg("user", fmt.Sprintf("另一份文档 %d", i))}
		seen[GPUStackAffinityHeader(setting, 15, srv.URL, "m", other)] = true
	}
	if len(seen) < 3 {
		t.Errorf("60 段不同对话只落到 %d 个实例，分布过于集中", len(seen))
	}
}

// BaseURL 为空、模型名为空时必须直接放弃，而不是去打一个拼坏的 URL。
func TestAffinityHeaderDeclinesOnMissingTarget(t *testing.T) {
	setting := dto.ChannelSettings{GPUStackAffinity: true, GPUStackAffinityKey: "k"}
	messages := []dto.Message{msg("user", "你好")}
	if got := GPUStackAffinityHeader(setting, 16, "", "m", messages); got != "" {
		t.Errorf("BaseURL 为空时应返回空串，实际 %q", got)
	}
	if got := GPUStackAffinityHeader(setting, 17, "http://127.0.0.1:1", "", messages); got != "" {
		t.Errorf("模型名为空时应返回空串，实际 %q", got)
	}
}

// 上游失败后标记过期：TTL 是 30 秒，实例挂掉后不能让这 30 秒的请求继续打过去。
// 关键是「标记过期」而非「删除」——失效后仍应返回旧列表，直到后台刷新落地。
func TestInvalidateMarksStaleButKeepsList(t *testing.T) {
	resetGPUStackCache()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		_, _ = w.Write([]byte(`{"items":[{"id":7,"model_id":7,"state":"running","model_name":"m"}]}`))
	}))
	defer srv.Close()

	waitForInstances(t, 20, srv.URL, "k", 1)
	if n := atomic.LoadInt64(&hits); n != 1 {
		t.Fatalf("预热应只拉一次，实际 %d", n)
	}

	// 把上一次成功推到下限之外，否则会被 gpustackInvalidateMinInterval 挡掉
	// （那条下限本身另有用例覆盖）。
	agePastInvalidateFloor(t)
	InvalidateGPUStackInstances(20)

	// 失效后第一次调用：应立刻拿到**旧列表**（不是空），同时触发后台刷新。
	got := gpustackRunningInstances(20, srv.URL, "k")
	if len(got) != 1 || got[0].ID != 7 {
		t.Errorf("失效后应仍返回旧列表（否则亲和会整体丢失），实际 %+v", got)
	}

	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt64(&hits) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := atomic.LoadInt64(&hits); n != 2 {
		t.Errorf("失效后应触发一次重新拉取，实际总共 %d 次", n)
	}
}

// 失效只影响指定渠道，别的渠道不该被连带清掉。
func TestInvalidateIsScopedToChannel(t *testing.T) {
	resetGPUStackCache()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[{"id":1,"model_id":7,"state":"running","model_name":"m"}]}`))
	}))
	defer srv.Close()

	waitForInstances(t, 21, srv.URL, "k", 1)
	waitForInstances(t, 22, srv.URL, "k", 1)

	agePastInvalidateFloor(t)
	InvalidateGPUStackInstances(21)

	gpustackInstanceMu.Lock()
	defer gpustackInstanceMu.Unlock()
	for key, at := range gpustackInstanceFetched {
		invalidated := time.Since(at) >= gpustackInstanceCacheTTL
		switch {
		case strings.HasPrefix(key, "21|") && !invalidated:
			t.Errorf("渠道 21 应被标记过期: %s", key)
		case strings.HasPrefix(key, "22|") && invalidated:
			t.Errorf("渠道 22 不该被连带失效: %s", key)
		}
		// 失效必须保留非零时间，否则日志会误报「从未成功拉取过」。
		if at.IsZero() {
			t.Errorf("%s 的时间戳被清零了", key)
		}
	}
}

// 只有路由/可用性类的状态码才算「实例不可用」。
func TestShouldInvalidateOnlyForRoutingFailures(t *testing.T) {
	for _, code := range []int{404, 502, 503, 504} {
		if !GPUStackAffinityShouldInvalidate(code) {
			t.Errorf("%d 应触发失效", code)
		}
	}
	// 400 参数错、401 鉴权、429 限流、500 模型自身报错都与实例存活无关，
	// 拿它们失效缓存会让正常的用户错误不断打掉亲和。
	for _, code := range []int{400, 401, 403, 429, 500} {
		if GPUStackAffinityShouldInvalidate(code) {
			t.Errorf("%d 不该触发失效", code)
		}
	}
}

// 分页：目标模型的实例可能不在第一页（机队大或一个 org 下模型多），
// 只读第一页会筛出空列表、亲和静默失效。
func TestFetchInstancesFollowsPagination(t *testing.T) {
	var pages []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		if page == "1" {
			// 满页，且全是别的模型——只读第一页就会得到空列表。
			items := make([]string, 0, gpustackInstancePerPage)
			for i := 1; i <= gpustackInstancePerPage; i++ {
				items = append(items, fmt.Sprintf(
					`{"id":%d,"model_id":8,"state":"running","model_name":"qwen-image"}`, i))
			}
			_, _ = fmt.Fprintf(w, `{"items":[%s],"pagination":{"totalPage":2}}`,
				strings.Join(items, ","))
			return
		}
		_, _ = w.Write([]byte(
			`{"items":[{"id":777,"model_id":7,"state":"running","model_name":"m"}],` +
				`"pagination":{"totalPage":2}}`))
	}))
	defer srv.Close()

	got, err := fetchGPUStackInstances(srv.URL, "k")
	if err != nil {
		t.Fatalf("不该报错: %v", err)
	}
	if len(pages) < 2 {
		t.Errorf("应当翻到第二页，实际只请求了 %v", pages)
	}
	mine := gpustackInstancesForModel(got, "m")
	if len(mine) != 1 || mine[0].ID != 777 {
		t.Errorf("按模型筛后应只剩第二页里那个实例，实际 %+v", mine)
	}
}

// 失败退避：key 填错时不能变成「每个用户请求打一次管理 API + 一行日志」。
func TestRunningInstancesBacksOffAfterFailure(t *testing.T) {
	resetGPUStackCache()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	// 第一次触发拉取（会 403），等它落地。
	gpustackRunningInstances(30, srv.URL, "bad")
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt64(&hits) < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	// 之后 200 个请求都在退避窗口内，不应再打管理 API。
	for i := 0; i < 200; i++ {
		gpustackRunningInstances(30, srv.URL, "bad")
	}
	time.Sleep(150 * time.Millisecond)
	if n := atomic.LoadInt64(&hits); n != 1 {
		t.Errorf("退避窗口内应只打过 1 次管理 API，实际 %d 次", n)
	}
}

// agePastInvalidateFloor 把所有「上次成功」时间推到失效下限之外，让
// InvalidateGPUStackInstances 能生效。只推到刚够，不要推过一个 TTL——
// 否则就分不清「被失效了」和「本来就过期了」。
func agePastInvalidateFloor(t *testing.T) {
	t.Helper()
	shift := gpustackInvalidateMinInterval + 100*time.Millisecond
	gpustackInstanceMu.Lock()
	defer gpustackInstanceMu.Unlock()
	for k, at := range gpustackInstanceFetched {
		gpustackInstanceFetched[k] = at.Add(-shift)
	}
}

// 失效要有下限：实例抖动时（gpustack 仍报它 running、网关却对它 502），每个失败
// 请求都会重新武装一次刷新，管理 API 就会看到「每个拉取时延一次全量翻页扫」。
// 刚成功拉过的列表不该被立刻再失效——再扫一遍拿到的也是同一份。
func TestInvalidateIsFlooredRightAfterSuccess(t *testing.T) {
	resetGPUStackCache()
	var hits int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&hits, 1)
		_, _ = w.Write([]byte(`{"items":[{"id":1,"model_id":7,"state":"running","model_name":"m"}]}`))
	}))
	defer srv.Close()

	waitForInstances(t, 40, srv.URL, "k", 1)

	// 模拟「一个实例在抖」：连续 100 次失败各触发一次失效。
	for i := 0; i < 100; i++ {
		InvalidateGPUStackInstances(40)
		gpustackRunningInstances(40, srv.URL, "k")
	}
	time.Sleep(200 * time.Millisecond)

	if n := atomic.LoadInt64(&hits); n != 1 {
		t.Errorf("下限内的 100 次失效应不触发任何重扫，实际总共拉取 %d 次", n)
	}

	// 过了下限之后，失效必须真的生效。
	agePastInvalidateFloor(t)
	InvalidateGPUStackInstances(40)
	gpustackRunningInstances(40, srv.URL, "k")
	deadline := time.Now().Add(2 * time.Second)
	for atomic.LoadInt64(&hits) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := atomic.LoadInt64(&hits); n != 2 {
		t.Errorf("过了下限后失效应触发一次重扫，实际总共 %d 次", n)
	}
}
