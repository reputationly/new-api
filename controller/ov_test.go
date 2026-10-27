package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// withMediaStorageForTest 临时打开媒体存储（指向一个连不上的 endpoint），
// 让 `PostOvFiles` 走过 Enabled() 那道门。返回的函数负责恢复原值。
func withMediaStorageForTest(t *testing.T, maxObjectSizeMB int) func() {
	t.Helper()
	settings := system_setting.GetMediaStorageSettings()
	previous := *settings
	settings.Enabled = true
	settings.Endpoint = "http://127.0.0.1:1"
	settings.Region = "test"
	settings.Bucket = "test-bucket"
	settings.AccessKeyID = "ak"
	settings.SecretAccessKey = "sk"
	settings.MaxObjectSizeMB = maxObjectSizeMB
	return func() { *settings = previous }
}

// 没有配对象存储时，上传入口必须回 503 + capability_unavailable，
// **不是 500**：那不是"服务器坏了"，客户端该做的是退回内联上传（L-2），
// 而 500 会让它当成一次故障去重试。
func TestPostOvFilesWithoutStorageAnswersCapabilityUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.POST("/v1/ov/files", PostOvFiles)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/ov/files", strings.NewReader(""))
	request.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	engine.ServeHTTP(recorder, request)

	// 这个用例依赖"测试环境没开媒体存储"（系统设置默认 false）。
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("= %d，期望 503（测试环境没配对象存储）", recorder.Code)
	}
	if !strings.Contains(recorder.Body.String(), string(types.ErrorCodeCapabilityUnavailable)) {
		t.Fatalf("响应体里没有 %s：%s", types.ErrorCodeCapabilityUnavailable, recorder.Body.String())
	}
}

// `limit == 0` 是"不限"，必须完整读取。旧实现无条件套 LimitReader(limit+1)，
// 于是一个 20 字节的上传只会存进 **1 个字节**，还返回 200。
func TestReadOvUploadUnlimitedReadsEverything(t *testing.T) {
	payload := strings.Repeat("a", 2048)
	body, tooLarge, err := readOvUpload(strings.NewReader(payload), 0)
	if err != nil {
		t.Fatalf("读取失败: %v", err)
	}
	if tooLarge {
		t.Fatalf("不限大小不该判超限")
	}
	if len(body) != len(payload) {
		t.Fatalf("读完 %d 字节，期望 %d —— 不限大小必须完整读", len(body), len(payload))
	}
}

// 有上限时多读 1 字节识别"谎报的 Size"。
func TestReadOvUploadDetectsOversize(t *testing.T) {
	body, tooLarge, err := readOvUpload(strings.NewReader(strings.Repeat("b", 100)), 100)
	if err != nil || tooLarge {
		t.Fatalf("刚好等于上限不该判超限：tooLarge=%v err=%v body=%d", tooLarge, err, len(body))
	}
	if _, tooLarge, _ := readOvUpload(strings.NewReader(strings.Repeat("b", 101)), 100); !tooLarge {
		t.Fatalf("超出 1 字节也要判超限")
	}
}

// 超限请求必须在**读请求体之前**被拒：否则 c.FormFile 会先把整个请求体收进
// 内存/临时文件，一个已鉴权的请求就能把磁盘写满。
//
// 断言手法：把对象存储指到一个连不上的地址。若代码走到了 Persist，响应会是 502；
// 拿到 413 说明它在写存储之前就短路了。同一个引擎下再发一次**不超限**的请求，
// 确认它确实会去碰存储（502），两组对照才说明"413 是提前拒的"，不是存储没配好。
func TestPostOvFilesRejectsOversizeBeforeReadingBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	restore := withMediaStorageForTest(t, 1 /* MaxObjectSizeMB = 1MB */)
	defer restore()

	engine := gin.New()
	engine.POST("/v1/ov/files", PostOvFiles)

	oversized := strings.Repeat("x", 3*1024*1024)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/ov/files", strings.NewReader(oversized))
	request.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("= %d，期望 413：%s", recorder.Code, recorder.Body.String())
	}

	// 对照组：在限额内的请求会走到 Persist（存储指到了连不上的地址 → 502）。
	small := "--x\r\nContent-Disposition: form-data; name=\"file\"; filename=\"a.png\"\r\nContent-Type: image/png\r\n\r\n" + strings.Repeat("y", 32) + "\r\n--x--\r\n"
	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodPost, "/v1/ov/files", strings.NewReader(small))
	request.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	engine.ServeHTTP(recorder, request)
	if recorder.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("限额内的请求被误拒：%s", recorder.Body.String())
	}
	if recorder.Code == http.StatusOK {
		t.Fatalf("测试环境没有可用的对象存储，不该返回 200")
	}
}

// 参考素材上传必须有**本接口自己的**硬顶：全局对象上限默认 200MB，而这个接口
// 会把整包读进内存，对所有已 KYC 账号开放 —— 沿用全局值等于给每个账号一个
// 200MB 的内存放大器。系统设置更小时听系统设置。
func TestOvUploadMaxBytesIsCapped(t *testing.T) {
	restore := withMediaStorageForTest(t, 200) // 系统设置默认 200MB
	defer restore()
	if got := ovUploadMaxBytes(); got != ovUploadMaxBytesCap {
		t.Fatalf("上限 = %d，期望封顶 %d", got, ovUploadMaxBytesCap)
	}

	restoreSmall := withMediaStorageForTest(t, 1) // 运营把全局上限调到 1MB
	defer restoreSmall()
	if got := ovUploadMaxBytes(); got != 1<<20 {
		t.Fatalf("系统设置更小时应听系统设置，实得 %d", got)
	}
}
