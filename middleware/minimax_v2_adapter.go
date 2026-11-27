package middleware

import (
	"bytes"
	"fmt"
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
	// transformErrorMessage 非 nil 时用于改写**失败响应里的 message**（只影响文案，
	// 状态码与信封类型不变）。nil = 原样透出。
	transformErrorMessage func(*gin.Context, string) string
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
		message := extractErrorMessage(body, status)
		if w.transformErrorMessage != nil {
			message = w.transformErrorMessage(c, message)
		}
		body = minimaxv2.BuildErrorBody(requestID, status, "", message)
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
func wrapMiniMaxV2Response(c *gin.Context, transformSuccess func([]byte) ([]byte, error), transformErrorMessage func(*gin.Context, string) string) {
	w := &minimaxV2Writer{
		ResponseWriter:        c.Writer,
		transformSuccess:      transformSuccess,
		transformErrorMessage: transformErrorMessage,
	}
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

// 引擎把「这个 task_type 该去另一个分区部署」报出来时的特征串。
//
// 原文（2026-11 线上实测，任务 tsk_7d17cf773ad5）：
//
//	task_type 'r2va' needs the MiniMax-H3 Ref2VA checkpoint, but model
//	'minimax-h3-fl2va' is deployed from the FL2VA partition (serves t2va/fl2va only).
//	Point this task_type at a Ref2VA deployment.
//
// 这句话对**引擎的运维**有用，对客户端一无用处：它既不能改我们的部署，也不知道
// 「Ref2VA 分区」是什么。按两个子串判，是给聚合分派兜底 —— 正常情况（MiniMax-H3
// 按 task_type 分派）不会再走到这里，走到这里说明有人直连了裸 checkpoint 或配置被改坏。
var (
	partitionErrorRef2VA = "Ref2VA checkpoint"
	partitionErrorFL2VA  = "FL2VA partition"
)

// preferredRoutingModelName 网关对外推荐的"按玩法自动分派"的聚合名。
const preferredRoutingModelName = "MiniMax-H3"

// rewritePartitionError 把分区报错改写成客户端能照做的一句话。其余错误原样返回。
//
// 状态码与环境其余部分不变，只换 message：这类错误的成因**永远是请求与部署对不上**，
// 换模型名就能解决，所以给出的动作是"改用那个自动分派的名字"。
// 引擎原文写进 SysLog —— 排障要看的是它，而客户看那句话只会更糊涂。
func rewritePartitionError(c *gin.Context, message string) string {
	if !strings.Contains(message, partitionErrorRef2VA) && !strings.Contains(message, partitionErrorFL2VA) {
		return message
	}
	snapshot := minimaxv2.LoadSnapshot(c)
	model, taskType := "", ""
	if snapshot != nil {
		model, taskType = snapshot.Model, snapshot.TaskType
	}
	common.SysLog(fmt.Sprintf(
		"[minimaxv2] 下游分区报错已改写（model=%s task_type=%s）:%s", model, taskType, message))
	if model == "" {
		// 拿不到客户原始模型名（理论上不该发生：提交接口必然存过快照）时不做改写，
		// 否则会拼出一句 "model \"\" cannot serve"，比原文更糟。
		return message
	}
	if taskType == "" {
		taskType = "unknown"
	}
	// **客户已经在用推荐的那个名字时，不能再叫它改用自己。**
	//
	// `MiniMax-H3` 本身就会按 task_type 分派；它仍然撞到分区错误，说明是网关侧的
	// 分派配置（AggregateModelConfig 少了 task_models、或生成段指错）出了问题，
	// 不是客户能通过改 model 解决的。这时给出"改用 MiniMax-H3"只会让客户觉得我们
	// 在敷衍，并让他去改一个本来就对的请求。
	if strings.EqualFold(strings.TrimSpace(model), preferredRoutingModelName) {
		hint := "keep the request_id from this response and contact gateway operations"
		if requestID := c.GetString(common.RequestIdKey); requestID != "" {
			hint = fmt.Sprintf("keep request_id %q and contact gateway operations", requestID)
		}
		return fmt.Sprintf("model %q cannot serve this request (task_type=%s): "+
			"the gateway's own routing did not reach a matching deployment, which is a "+
			"gateway configuration problem rather than a request problem — %s", model, taskType, hint)
	}
	return fmt.Sprintf("model %q cannot serve this request (task_type=%s); "+
		"use model %q, which routes every mode automatically", model, taskType, preferredRoutingModelName)
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

		wrapMiniMaxV2Response(c, minimaxv2.CreateSuccessBody, rewritePartitionError)
	}
}

// MiniMaxV2Envelope 只做错误信封统一,成功响应原样透出。用于查询 / 列表 / 删除等
// body 本来就已经是官方形态的端点。
func MiniMaxV2Envelope() gin.HandlerFunc {
	return func(c *gin.Context) {
		wrapMiniMaxV2Response(c, nil, nil)
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
