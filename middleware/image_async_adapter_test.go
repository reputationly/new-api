package middleware

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relay"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// 下游一律经 GetBodyStorage 读 body（UnmarshalBodyReusable 不看 c.Request.Body），
// 所以断言必须从 storage 取，否则测试会在真实链路早已失效的情况下依然通过。
type asyncProbe struct {
	body      string
	relayMode int
	isAsync   bool
	action    string
	ctype     string
}

func runImageAsync(t *testing.T, path, contentType, body string, headers map[string]string) (*httptest.ResponseRecorder, *asyncProbe) {
	t.Helper()
	probe := &asyncProbe{}
	router := gin.New()
	router.POST(path, ImageAsyncConvert(), func(c *gin.Context) {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			t.Fatalf("body storage: %s", err)
		}
		raw, err := storage.Bytes()
		if err != nil {
			t.Fatalf("body bytes: %s", err)
		}
		probe.body = string(raw)
		probe.relayMode = c.GetInt("relay_mode")
		probe.isAsync = c.GetBool(CtxKeyImageAsync)
		probe.action = c.GetString("action")
		probe.ctype = c.Request.Header.Get("Content-Type")
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w, probe
}

// 同步请求必须一个字节都不动。这是整个改造最重要的不变量：图片端点上绝大多数流量
// 是同步的，中间件误伤它就是把主链路改坏了。
func TestImageAsyncConvertLeavesSyncRequestUntouched(t *testing.T) {
	body := `{"model":"z-image","prompt":"a cat","size":"1024x1024"}`
	_, probe := runImageAsync(t, "/v1/images/generations", "application/json", body, nil)

	if probe.body != body {
		t.Errorf("sync request body was rewritten:\n got: %s\nwant: %s", probe.body, body)
	}
	if probe.isAsync {
		t.Error("sync request was marked as async")
	}
	if probe.relayMode != 0 {
		t.Errorf("sync request relay_mode was set to %d, want unset", probe.relayMode)
	}
}

// async:false 与不传等价，同样不能改写。
func TestImageAsyncConvertIgnoresExplicitFalse(t *testing.T) {
	body := `{"model":"z-image","prompt":"a cat","async":false}`
	_, probe := runImageAsync(t, "/v1/images/generations", "application/json", body, nil)

	if probe.isAsync {
		t.Error("async:false was treated as async")
	}
	if probe.body != body {
		t.Errorf("async:false request body was rewritten: %s", probe.body)
	}
}

func TestImageAsyncConvertRewritesToTaskContract(t *testing.T) {
	body := `{"model":"z-image","prompt":"a cat","size":"1024x1024","async":true,"seed":42,"negative_prompt":"blurry"}`
	_, probe := runImageAsync(t, "/v1/images/generations", "application/json", body, nil)

	if !probe.isAsync {
		t.Fatal("async:true was not recognized")
	}
	if probe.relayMode != relayconstant.RelayModeImageSubmit {
		t.Errorf("relay_mode = %d, want RelayModeImageSubmit", probe.relayMode)
	}
	if probe.action != constant.TaskActionImageGenerate {
		t.Errorf("action = %q, want %q", probe.action, constant.TaskActionImageGenerate)
	}

	var got map[string]any
	if err := common.Unmarshal([]byte(probe.body), &got); err != nil {
		t.Fatalf("rewritten body is not valid json: %s", err)
	}
	if got["model"] != "z-image" || got["prompt"] != "a cat" || got["size"] != "1024x1024" {
		t.Errorf("top-level task contract fields wrong: %+v", got)
	}

	md, ok := got["metadata"].(map[string]any)
	if !ok {
		t.Fatal("metadata missing from rewritten body")
	}
	if md["task_type"] != "t2i" {
		t.Errorf("metadata.task_type = %v, want t2i", md["task_type"])
	}
	// 渠道专有参数必须随 metadata 整体透传到适配器，否则 seed / negative_prompt 这些
	// 同步模式下可用的参数在异步模式下会静默失效。
	if md["seed"] == nil || md["negative_prompt"] != "blurry" {
		t.Errorf("channel-specific params lost in metadata: %+v", md)
	}
	// async 是网关控制字段，不能透传给门面/引擎。
	if _, leaked := md["async"]; leaked {
		t.Error("async control field leaked into metadata")
	}
}

func TestImageAsyncConvertEditsSetsI2I(t *testing.T) {
	body := `{"model":"qwen-image-edit","prompt":"make it blue","async":true,"image":"https://example.com/a.png"}`
	_, probe := runImageAsync(t, "/v1/images/edits", "application/json", body, nil)

	if probe.action != constant.TaskActionImageEdit {
		t.Errorf("action = %q, want %q", probe.action, constant.TaskActionImageEdit)
	}
	var got map[string]any
	_ = common.Unmarshal([]byte(probe.body), &got)
	md, _ := got["metadata"].(map[string]any)
	if md["task_type"] != "i2i" {
		t.Errorf("metadata.task_type = %v, want i2i", md["task_type"])
	}
	images, ok := got["images"].([]any)
	if !ok || len(images) != 1 || images[0] != "https://example.com/a.png" {
		t.Errorf("images not normalized: %+v", got["images"])
	}
	// 原始 image 键必须从 metadata 剥掉：门面见到裸的原始输入字段会整单 400。
	if _, leaked := md["image"]; leaked {
		t.Error("raw image key leaked into metadata")
	}
}

// header 开关是 passthrough 场景下唯一可用的开关（body 会被原样转发给上游，
// 多带一个 async 字段会被上游拒）。
func TestImageAsyncConvertHeaderSwitch(t *testing.T) {
	body := `{"model":"z-image","prompt":"a cat"}`
	_, probe := runImageAsync(t, "/v1/images/generations", "application/json", body,
		map[string]string{AsyncHeader: "true"})

	if !probe.isAsync {
		t.Fatal("X-New-Api-Async header was not recognized")
	}
	if probe.relayMode != relayconstant.RelayModeImageSubmit {
		t.Errorf("relay_mode = %d, want RelayModeImageSubmit", probe.relayMode)
	}
}

// header 优先于 body：基础设施注入的开关应当压过请求体里的值。
func TestImageAsyncConvertHeaderOverridesBody(t *testing.T) {
	body := `{"model":"z-image","prompt":"a cat","async":true}`
	_, probe := runImageAsync(t, "/v1/images/generations", "application/json", body,
		map[string]string{AsyncHeader: "false"})

	if probe.isAsync {
		t.Error("header false did not override body true")
	}
}

func buildMultipart(t *testing.T, fields map[string]string, files map[string][]byte) (string, string) {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			t.Fatalf("write field: %s", err)
		}
	}
	for name, content := range files {
		fw, err := w.CreateFormFile(name, name+".png")
		if err != nil {
			t.Fatalf("create form file: %s", err)
		}
		if _, err := io.Copy(fw, bytes.NewReader(content)); err != nil {
			t.Fatalf("copy file: %s", err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %s", err)
	}
	return w.FormDataContentType(), buf.String()
}

// OpenAI 官方 SDK 的 /v1/images/edits 走 multipart。蒙版走的是独立的 mask 文件字段，
// 漏掉它的话出图看起来「成功」却没生效 —— 同步链路专门为这个坑写过注释
// （relay/channel/gpustackplus/adaptor.go:302），异步不能重蹈覆辙。
func TestImageAsyncConvertMultipartCarriesImageAndMask(t *testing.T) {
	imageBytes := []byte("\x89PNG\r\n\x1a\nfake-image-payload")
	maskBytes := []byte("\x89PNG\r\n\x1a\nfake-mask-payload")
	ctype, body := buildMultipart(t,
		map[string]string{"model": "qwen-image-edit", "prompt": "make it blue"},
		map[string][]byte{"image": imageBytes, "mask": maskBytes})

	// multipart 的异步开关只能走 header，不能用表单字段 —— 见
	// TestImageAsyncConvertMultipartIgnoresFormFieldSwitch 的理由。
	_, probe := runImageAsync(t, "/v1/images/edits", ctype, body,
		map[string]string{AsyncHeader: "true"})

	if !probe.isAsync {
		t.Fatal("multipart async switch was not recognized")
	}
	// multipart 转 JSON 后必须同步改 Content-Type，否则下游会拿 multipart 分支去解 JSON。
	if !strings.HasPrefix(probe.ctype, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", probe.ctype)
	}

	var got map[string]any
	if err := common.Unmarshal([]byte(probe.body), &got); err != nil {
		t.Fatalf("rewritten body is not valid json: %s", err)
	}
	images, ok := got["images"].([]any)
	if !ok || len(images) != 1 {
		t.Fatalf("uploaded image not converted: %+v", got["images"])
	}
	wantImage := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes)
	if images[0] != wantImage {
		t.Errorf("image data-uri mismatch:\n got: %v\nwant: %s", images[0], wantImage)
	}

	md, _ := got["metadata"].(map[string]any)
	wantMask := "data:image/png;base64," + base64.StdEncoding.EncodeToString(maskBytes)
	if md["mask"] != wantMask {
		t.Errorf("mask lost or mismatched:\n got: %v\nwant: %s", md["mask"], wantMask)
	}
}

// 已确认要异步、但请求本身有问题时必须 abort，不能放行给同步链路：
// 客户端在等 job 对象，同步链路会回一个形状完全不同的 ImageResponse，解析必崩。
func TestImageAsyncConvertAbortsOnBadUpload(t *testing.T) {
	ctype, body := buildMultipart(t,
		map[string]string{"model": "qwen-image-edit", "prompt": "x"},
		map[string][]byte{"image": {}})

	w, probe := runImageAsync(t, "/v1/images/edits", ctype, body,
		map[string]string{AsyncHeader: "true"})

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
	if probe.body != "" {
		t.Error("request was forwarded downstream instead of being aborted")
	}
}

// —— 以下两组是 code review 发现的 P1 回归，各自固定一条承重不变量 ——

// P1：同步 multipart 请求的 body 绝不能被中间件碰。
//
// OpenAI 的 /v1/images/edits 按规范就是 multipart，是同步 edits 的标准调用方式。
// 中间件曾用 c.Request.ParseMultipartForm 去找 async 字段，那会消费掉 c.Request.Body；
// 而 body storage 是懒创建的，后续第一次 GetBodyStorage 从已耗尽的 Body 读，拿到空内容
// —— Distribute 解不出 model，请求直接 400「model name required」。
// 换句话说：加个异步功能，把本来好用的同步 edits 弄坏了。
func TestImageAsyncConvertLeavesSyncMultipartIntact(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	var seenModel string
	router.POST("/v1/images/edits", ImageAsyncConvert(), func(c *gin.Context) {
		// 复刻 Distribute 的读法：getModelFromRequest → UnmarshalBodyReusable。
		var mr struct {
			Model string `json:"model" form:"model"`
		}
		if err := common.UnmarshalBodyReusable(c, &mr); err != nil {
			t.Logf("UnmarshalBodyReusable error: %s", err)
		}
		seenModel = mr.Model
		c.Status(http.StatusOK)
	})

	ctype, body := buildMultipart(t,
		map[string]string{"model": "qwen-image-edit", "prompt": "x"},
		map[string][]byte{"image": []byte("\x89PNG\r\n\x1a\npayload")})
	req := httptest.NewRequest(http.MethodPost, "/v1/images/edits", strings.NewReader(body))
	req.Header.Set("Content-Type", ctype)
	router.ServeHTTP(httptest.NewRecorder(), req)

	if seenModel != "qwen-image-edit" {
		t.Errorf("downstream saw model=%q, want qwen-image-edit; "+
			"the middleware consumed the sync multipart body", seenModel)
	}
}

// multipart 下表单字段 async=true 刻意不生效，只认 header。
//
// 这是个有意的取舍而不是遗漏：要读表单字段就得把整个 body 解出来（几 MB 的图会落一次
// 临时盘），让**所有**同步 edits 为一个罕见用法付这个代价不划算。这条用例把取舍固定住，
// 免得有人「顺手」加回表单判断，再次把同步 multipart 的 body 吃掉。
func TestImageAsyncConvertMultipartIgnoresFormFieldSwitch(t *testing.T) {
	ctype, body := buildMultipart(t,
		map[string]string{"model": "qwen-image-edit", "prompt": "x", "async": "true"},
		map[string][]byte{"image": []byte("\x89PNG\r\n\x1a\npayload")})

	_, probe := runImageAsync(t, "/v1/images/edits", ctype, body, nil)

	if probe.isAsync {
		t.Error("multipart form field async=true must NOT switch to async; header is the only switch there")
	}
}

// P1：中间件设的 relay_mode 必须活到 relayInfo.RelayMode。
//
// genBaseRelayInfo 用 Path2RelayMode(path) 初始化 RelayMode，只在结果为 Unknown 时才回落
// 读 context。异步图片与同步图片**共用同一个路径**，Path2RelayMode 必然返回
// RelayModeImagesGenerations，把 context 里的 RelayModeImageSubmit 盖掉 ——
// 后果是提交回视频对象、查询在 fetchRespBuilders 里查不到 builder 而空指针 panic。
// 视频端点只是恰好没被 Path2RelayMode 覆盖才一直没暴露这个问题。
func TestAsyncImageRelayModeSurvivesGenRelayInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	var gotMode int
	router.POST("/v1/images/generations", ImageAsyncConvert(), func(c *gin.Context) {
		info, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
		if err != nil {
			t.Fatalf("GenRelayInfo: %s", err)
		}
		gotMode = info.RelayMode
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations",
		strings.NewReader(`{"model":"z-image","prompt":"a cat","async":true}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), req)

	if gotMode != relayconstant.RelayModeImageSubmit {
		t.Errorf("relayInfo.RelayMode = %d, want RelayModeImageSubmit (%d); "+
			"Path2RelayMode shadowed the context value",
			gotMode, relayconstant.RelayModeImageSubmit)
	}
}

// 反向守护：同步图片请求的 RelayMode 必须保持 RelayModeImagesGenerations。
// 上面那条修复动的是 GenRelayInfo 的 task 分支，这条确认它没把同步链路带偏。
func TestSyncImageRelayModeUnchanged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	var gotMode int
	router.POST("/v1/images/generations", ImageAsyncConvert(), func(c *gin.Context) {
		gotMode = relayconstant.Path2RelayMode(c.Request.URL.Path)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations",
		strings.NewReader(`{"model":"z-image","prompt":"a cat"}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), req)

	if gotMode != relayconstant.RelayModeImagesGenerations {
		t.Errorf("sync image RelayMode = %d, want RelayModeImagesGenerations (%d)",
			gotMode, relayconstant.RelayModeImagesGenerations)
	}
}

// 承重前提：异步图片提交必须让 IsAsyncImageSubmit 返回 true。
//
// controller.RelayTask 靠它决定要不要写 task.APIProtocol="image"，而查询/取消端点
// 又靠那一列做守卫。这条链断在任何一环，后果都是「任务在跑、钱已扣，但查不到」。
// 单独固定它，因为链条的两端分处三个包，任何一端的改动都可能悄悄断掉它。
func TestAsyncImageSubmitIsRecognizedDownstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	var recognized bool
	router.POST("/v1/images/generations", ImageAsyncConvert(), func(c *gin.Context) {
		info, err := relaycommon.GenRelayInfo(c, types.RelayFormatTask, nil, nil)
		if err != nil {
			t.Fatalf("GenRelayInfo: %s", err)
		}
		recognized = relay.IsAsyncImageSubmit(info)
		c.Status(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/images/generations",
		strings.NewReader(`{"model":"z-image","prompt":"a cat","async":true}`))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(httptest.NewRecorder(), req)

	if !recognized {
		t.Error("IsAsyncImageSubmit returned false for an async image submit; " +
			"task.APIProtocol would never be set, and the fetch/cancel guards would reject the task")
	}
}
