package middleware

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/continuation"

	"github.com/gin-gonic/gin"
)

// runContinuation 跑一条真实的 gin 链：模拟 TokenAuth 注入用户 id → 续写中间件 → 终点处理器。
// 终点处理器把改写后的 body / action / 快照抓出来供断言。
type continuationCapture struct {
	reached  bool
	body     map[string]any
	action   string
	snapshot *model.ContinuationProperties
}

func runContinuation(t *testing.T, body string) (*continuationCapture, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cap := &continuationCapture{}
	rec := httptest.NewRecorder()

	r := gin.New()
	r.POST("/v1/videos", func(c *gin.Context) {
		// 相当于 TokenAuth：后续 task: 引用要靠这个 id 做归属校验。
		c.Set("id", 42)
		c.Next()
	}, ContinuationConvert(), func(c *gin.Context) {
		cap.reached = true
		cap.action = c.GetString("action")
		cap.snapshot = GetContinuationSnapshot(c)
		cap.body = taskBodyMap(t, c)
		c.Status(200)
	})

	req := httptest.NewRequest("POST", "/v1/videos", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	return cap, rec
}

// taskBodyMap 读下游适配器真正会看到的 body（走 GetTaskRequest 的同一条存储）。
func taskBodyMap(t *testing.T, c *gin.Context) map[string]any {
	t.Helper()
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	raw, err := storage.Bytes()
	if err != nil {
		t.Fatalf("bytes: %v", err)
	}
	var out map[string]any
	if err := common.Unmarshal(raw, &out); err != nil {
		t.Fatalf("body 不是 JSON: %v (%s)", err, raw)
	}
	return out
}

func stubPrepare(t *testing.T, result *continuation.ContinuationPrepareResult, err error) {
	t.Helper()
	previous := prepareContinuation
	prepareContinuation = func(context.Context, continuation.ContinuationPrepareOptions, *continuation.PrepareDeps) (*continuation.ContinuationPrepareResult, error) {
		return result, err
	}
	t.Cleanup(func() { prepareContinuation = previous })
}

// 非续写请求必须一个字节都不改 —— 这条中间件挂在所有视频提交路由上，误伤面最大。
func TestContinuationConvertLeavesOtherRequestsAlone(t *testing.T) {
	const body = `{"model":"minimax-h3","prompt":"cat","duration":5,"metadata":{"image_mode":"reference"}}`
	stubPrepare(t, nil, errors.New("不该被调用"))

	cap, rec := runContinuation(t, body)
	if !cap.reached {
		t.Fatal("非续写请求应继续往下走")
	}
	if rec.Code != 200 {
		t.Fatalf("不该写响应, code=%d body=%s", rec.Code, rec.Body.String())
	}
	if cap.body["duration"] != float64(5) {
		t.Fatalf("body 被改了: %+v", cap.body)
	}
	if cap.action != "" {
		t.Fatalf("非续写不该设置 action, got %q", cap.action)
	}
}

// 续写：image_mode=video-extension → i2v + 首帧 + 生成段时长，并留下快照。
func TestContinuationConvertRewritesImageModeRequest(t *testing.T) {
	const body = `{"model":"minimax-h3","prompt":"keep walking","duration":10,
	  "metadata":{"image_mode":"video-extension","source_video":"https://x.com/a.mp4","resolution":"768P"}}`
	stubPrepare(t, &continuation.ContinuationPrepareResult{
		FirstFrameDataURL: "data:image/png;base64,AAAA",
		Probe:             continuation.ContinuationProbe{DurationSeconds: 3},
		GenerateSeconds:   7,
	}, nil)

	cap, _ := runContinuation(t, body)
	if !cap.reached {
		t.Fatal("改写后应继续往下走")
	}
	if cap.body["image"] != "data:image/png;base64,AAAA" {
		t.Fatalf("首帧没写进 body: %+v", cap.body)
	}
	if cap.body["duration"] != float64(7) {
		t.Fatalf("duration 应改成生成段秒数, got %v", cap.body["duration"])
	}
	meta, _ := cap.body["metadata"].(map[string]any)
	if meta["task_type"] != "i2v" {
		t.Fatalf("task_type 应改成 i2v, got %v", meta["task_type"])
	}
	if _, has := meta["image_mode"]; has {
		t.Fatal("image_mode 应被消费掉（留着会和真实输入形态打架）")
	}
	if _, has := meta["source_video"]; has {
		t.Fatal("source_video 应被消费掉")
	}
	if meta["resolution"] != "768P" {
		t.Fatalf("其它 metadata 不该被丢: %+v", meta)
	}
	if cap.action != constant.TaskActionContinuation {
		t.Fatalf("action = %q, want %q", cap.action, constant.TaskActionContinuation)
	}
	if cap.snapshot == nil {
		t.Fatal("缺少续写快照")
	}
	if cap.snapshot.SourceSeconds != 3 || cap.snapshot.GenerateSeconds != 7 || cap.snapshot.TotalSeconds != 10 {
		t.Fatalf("快照不对: %+v", cap.snapshot)
	}
	if !strings.Contains(cap.snapshot.SourceRef, "a.mp4") {
		t.Fatalf("快照应保留源片引用: %q", cap.snapshot.SourceRef)
	}
}

// task_type=continuation 与 image_mode 等价（两个入口都要认）。
func TestContinuationConvertAcceptsTaskTypeAlias(t *testing.T) {
	const body = `{"model":"minimax-h3","prompt":"p","duration":8,
	  "metadata":{"task_type":"continuation","source_video":"task:task_x"}}`
	stubPrepare(t, &continuation.ContinuationPrepareResult{
		FirstFrameDataURL: "data:image/png;base64,BBBB",
		Probe:             continuation.ContinuationProbe{DurationSeconds: 4},
		GenerateSeconds:   4,
	}, nil)

	cap, _ := runContinuation(t, body)
	if cap.body["duration"] != float64(4) {
		t.Fatalf("duration = %v, want 4", cap.body["duration"])
	}
	if cap.snapshot == nil {
		t.Fatal("缺少续写快照")
	}
}

// 预处理失败：4xx + 稳定 code，且**不继续往下走**（不落任务、不烧 GPU）。
func TestContinuationConvertRejectsWhenPrepareFails(t *testing.T) {
	const body = `{"model":"minimax-h3","prompt":"p","duration":3,
	  "metadata":{"image_mode":"video-extension","source_video":"https://x.com/a.mp4"}}`
	stubPrepare(t, nil, &continuation.ContinuationError{
		Code:    "duration_below_source",
		Message: "最终总时长 3 s 必须大于源视频(3.00 s)",
	})

	cap, rec := runContinuation(t, body)
	if cap.reached {
		t.Fatal("预处理失败不该继续提交")
	}
	if rec.Code != 400 {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "duration_below_source") {
		t.Fatalf("响应里应带错误码: %s", rec.Body.String())
	}
}

// 缺 source_video：不调预处理就拒，省一次下载。
func TestContinuationConvertRequiresSource(t *testing.T) {
	const body = `{"model":"minimax-h3","prompt":"p","duration":10,"metadata":{"image_mode":"video-extension"}}`
	stubPrepare(t, nil, errors.New("不该被调用"))

	cap, rec := runContinuation(t, body)
	if cap.reached {
		t.Fatal("缺源片不该继续提交")
	}
	if rec.Code != 400 || !strings.Contains(rec.Body.String(), "source_missing") {
		t.Fatalf("应报 source_missing, got %d %s", rec.Code, rec.Body.String())
	}
}

// metadata 是 JSON 字符串形态时，改写后必须仍是字符串（有些客户端按字符串解析）。
func TestContinuationConvertPreservesStringMetadataShape(t *testing.T) {
	const body = `{"model":"minimax-h3","prompt":"p","duration":9,
	  "metadata":"{\"image_mode\":\"video-extension\",\"source_video\":\"https://x.com/a.mp4\"}"}`
	stubPrepare(t, &continuation.ContinuationPrepareResult{
		FirstFrameDataURL: "data:image/png;base64,CCCC",
		Probe:             continuation.ContinuationProbe{DurationSeconds: 3},
		GenerateSeconds:   6,
	}, nil)

	cap, _ := runContinuation(t, body)
	if _, ok := cap.body["metadata"].(string); !ok {
		t.Fatalf("metadata 应保持字符串形态, got %T", cap.body["metadata"])
	}
}
