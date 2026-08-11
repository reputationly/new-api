package middleware

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/minimaxv2"

	"github.com/gin-gonic/gin"
)

// MiniMax v2 官方协议兼容层的 gin 接线。协议本身在 relay/minimaxv2 包里,
// 这里只负责三件事:改写请求 body 与路径、把响应改写成官方形态、统一错误信封。
//
// 请求侧沿用 KlingRequestConvert / JimengRequestConvert 那条既有路径
// (改写 body + c.Request.URL.Path,再复用 controller.RelayTask),但**转换本身是真转换**
// 而不是把原始 body 整个塞进 metadata —— 它们的上游就是对应厂商,我们的上游是自建引擎。
//
// 响应侧必须做:统一契约的提交接口回的是 OpenAI 风格的完整 video 对象,官方只回一个
// task_id;而 controller / middleware 的错误是本仓自己的形态,官方 SDK 解不了。
// 两者都靠下面这个缓冲 writer 在 c.Next() 之后改写。

// minimaxV2Writer 缓冲下游写出的响应,待处理链跑完后统一改写再落到真 writer 上。
type minimaxV2Writer struct {
	gin.ResponseWriter
	buf         bytes.Buffer
	status      int
	wroteHeader bool
	// transformSuccess 非 nil 时用于改写 2xx 响应体;nil 表示成功响应原样透出
	// (查询/列表/删除这几个端点的 body 本来就已经是官方形态)。
	transformSuccess func([]byte) ([]byte, error)
}

func (w *minimaxV2Writer) WriteHeader(code int) {
	if code > 0 {
		w.status = code
		w.wroteHeader = true
	}
}

// WriteHeaderNow 必须吞掉:真写头一旦发生就锁死了状态码,后面改写不了。
func (w *minimaxV2Writer) WriteHeaderNow() {}

func (w *minimaxV2Writer) Write(b []byte) (int, error) { return w.buf.Write(b) }

func (w *minimaxV2Writer) WriteString(s string) (int, error) { return w.buf.WriteString(s) }

func (w *minimaxV2Writer) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *minimaxV2Writer) Size() int { return w.buf.Len() }

func (w *minimaxV2Writer) Written() bool { return w.wroteHeader || w.buf.Len() > 0 }

// Flush 同样吞掉:缓冲期真 flush 会把未改写的内容送出去。这几个端点都是一次性 JSON,
// 没有流式需求。
func (w *minimaxV2Writer) Flush() {}

func (w *minimaxV2Writer) commit(c *gin.Context) {
	target := w.ResponseWriter
	status := w.Status()
	body := w.buf.Bytes()
	requestID := c.GetString(common.RequestIdKey)

	switch {
	case status >= 200 && status < 300 && len(bytes.TrimSpace(body)) == 0:
		// 处理链什么都没写就结束了(理论上不该发生)。给出合法的官方错误而不是空 200,
		// 免得调用方拿着一个解析不了的空响应去猜。
		status = http.StatusInternalServerError
		body = minimaxv2.BuildErrorBody(requestID, status, minimaxv2.ErrTypeServer, "empty response from upstream handler")
	case status >= 200 && status < 300:
		if w.transformSuccess != nil {
			converted, err := w.transformSuccess(body)
			if err != nil {
				status = http.StatusInternalServerError
				body = minimaxv2.BuildErrorBody(requestID, status, minimaxv2.ErrTypeServer, err.Error())
			} else {
				body = converted
			}
		}
	case minimaxv2.IsErrorEnvelope(body):
		// 已经是官方信封(本兼容层自己产生的错误):别二次包装。
	default:
		body = minimaxv2.BuildErrorBody(requestID, status, "", extractErrorMessage(body, status))
	}

	header := target.Header()
	header.Set("Content-Type", "application/json; charset=utf-8")
	header.Del("Content-Length")
	target.WriteHeader(status)
	_, _ = target.Write(body)
}

// wrapMiniMaxV2Response 装上缓冲 writer,并在处理链结束后提交改写结果。
//
// panic 路径**不提交**缓冲内容:main.go 的 CustomRecovery 会在我们这一帧展开之后
// 往真 writer 写它自己的 500。若我们也写一份,两段 body 会拼在一起变成非法 JSON。
// 代价是 panic 时的错误不是官方形态 —— panic 是 bug 不是协议状态,可以接受。
func wrapMiniMaxV2Response(c *gin.Context, transformSuccess func([]byte) ([]byte, error)) {
	w := &minimaxV2Writer{ResponseWriter: c.Writer, transformSuccess: transformSuccess}
	c.Writer = w
	completed := false
	defer func() {
		c.Writer = w.ResponseWriter
		if completed {
			w.commit(c)
		}
	}()
	c.Next()
	completed = true
}

// extractErrorMessage 从本仓各种错误形态里捞出人话:
// dto.TaskError 是 {"code","message"},abortWithOpenAiMessage 是 {"error":{"message"}}。
// 都对不上就退回原始 body(截断),总比丢掉上游的错误信息强。
func extractErrorMessage(body []byte, status int) string {
	if len(bytes.TrimSpace(body)) == 0 {
		return http.StatusText(status)
	}
	var probe struct {
		Message string `json:"message"`
		Error   *struct {
			Message string `json:"message"`
		} `json:"error"`
		Description string `json:"description"`
	}
	if err := common.Unmarshal(body, &probe); err == nil {
		if probe.Error != nil && strings.TrimSpace(probe.Error.Message) != "" {
			return probe.Error.Message
		}
		if strings.TrimSpace(probe.Message) != "" {
			return probe.Message
		}
		if strings.TrimSpace(probe.Description) != "" {
			return probe.Description
		}
	}
	const maxRawLen = 1024
	raw := strings.TrimSpace(string(body))
	if len(raw) > maxRawLen {
		raw = raw[:maxRawLen]
	}
	return raw
}

// MiniMaxV2CreateConvert 处理 POST /v2/video_generation:官方 body → 统一任务契约 body,
// 路径改写到 /v1/video/generations 后交给 controller.RelayTask;响应改写成 {"task_id"}。
//
// 必须排在 TokenAuth 之前(与 Kling / 即梦一致):这样鉴权失败的响应也走同一个信封。
func MiniMaxV2CreateConvert() gin.HandlerFunc {
	return func(c *gin.Context) {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			minimaxv2.AbortWithError(c, minimaxv2.NewBadRequest("failed to read request body: "+err.Error()))
			return
		}
		raw, err := storage.Bytes()
		if err != nil {
			minimaxv2.AbortWithError(c, minimaxv2.NewBadRequest("failed to read request body: "+err.Error()))
			return
		}

		body, snapshot, apiErr := minimaxv2.ConvertCreateRequest(raw)
		if apiErr != nil {
			minimaxv2.AbortWithError(c, apiErr)
			return
		}
		jsonData, err := common.Marshal(body)
		if err != nil {
			minimaxv2.AbortWithError(c, minimaxv2.NewServerError("failed to build upstream request: "+err.Error()))
			return
		}
		// 必须走 ReplaceRequestBody:它会关掉旧的 BodyStorage 并换成新的。只 c.Set
		// KeyRequestBody 是不够的 —— GetRequestBody 优先读 KeyBodyStorage,那里还留着
		// 上面读过的原始 body,下游拿到的会是官方形态而不是转换结果。
		if err := common.ReplaceRequestBody(c, jsonData); err != nil {
			minimaxv2.AbortWithError(c, minimaxv2.NewServerError("failed to replace request body: "+err.Error()))
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(jsonData))
		c.Request.ContentLength = int64(len(jsonData))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.URL.Path = "/v1/video/generations"
		minimaxv2.StoreSnapshot(c, snapshot)

		wrapMiniMaxV2Response(c, minimaxv2.CreateSuccessBody)
	}
}

// MiniMaxV2Envelope 只做错误信封统一,成功响应原样透出。用于查询 / 列表 / 删除等
// body 本来就已经是官方形态的端点。
func MiniMaxV2Envelope() gin.HandlerFunc {
	return func(c *gin.Context) {
		wrapMiniMaxV2Response(c, nil)
	}
}

// MiniMaxV2FetchMode 为 GET /v2/query/video_generation/{task_id} 指定 relay_mode。
//
// 这条路径不经 Distribute(查询只读本地任务表,不需要选渠道),而 relay_mode 平时由
// Distribute 设置;不显式指定的话 RelayTaskFetch 拿不到 builder。
func MiniMaxV2FetchMode() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("relay_mode", relayconstant.RelayModeVideoFetchByID)
		c.Next()
	}
}
