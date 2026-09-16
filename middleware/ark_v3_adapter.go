package middleware

import (
	"bytes"
	"io"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/arkv3"

	"github.com/gin-gonic/gin"
)

// 火山方舟 v3 协议兼容层的 gin 接线。协议本身在 relay/arkv3 包里，这里只负责三件事：
// 改写请求 body 与路径、把提交响应改写成官方形态、统一错误信封。
//
// 请求侧沿用 KlingRequestConvert / MiniMaxV2CreateConvert 那条既有路径（改写 body +
// c.Request.URL.Path，再复用 controller.RelayTask）。

func arkEnvelopeSpec(transformSuccess func([]byte) ([]byte, error)) envelopeSpec {
	return envelopeSpec{
		buildError: func(c *gin.Context, status int, message string) []byte {
			return arkv3.BuildErrorBody(c.GetString(common.RequestIdKey), status, "", "", message)
		},
		isEnvelope:       arkv3.IsErrorEnvelope,
		transformSuccess: transformSuccess,
	}
}

// ArkV3CreateConvert 处理 POST /api/v3/contents/generations/tasks：官方 body → 统一
// 任务契约 body，路径改写到 /v1/video/generations 后交给 controller.RelayTask；
// 响应改写成 {"id": "task_xxx"}。
//
// 必须排在 TokenAuth 之前（与 Kling / 即梦 / MiniMax 一致）：这样鉴权失败的响应也走
// 同一个信封，否则方舟 SDK 会在 401 上解析失败。
func ArkV3CreateConvert() gin.HandlerFunc {
	return func(c *gin.Context) {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			arkv3.AbortWithError(c, arkv3.NewBadRequest("failed to read request body: "+err.Error()))
			return
		}
		raw, err := storage.Bytes()
		if err != nil {
			arkv3.AbortWithError(c, arkv3.NewBadRequest("failed to read request body: "+err.Error()))
			return
		}

		body, snapshot, apiErr := arkv3.ConvertCreateRequest(raw)
		if apiErr != nil {
			arkv3.AbortWithError(c, apiErr)
			return
		}
		jsonData, err := common.Marshal(body)
		if err != nil {
			arkv3.AbortWithError(c, arkv3.NewServerError("failed to build upstream request: "+err.Error()))
			return
		}
		// 必须走 ReplaceRequestBody：它会关掉旧的 BodyStorage 并换成新的。只 c.Set
		// KeyRequestBody 是不够的 —— GetRequestBody 优先读 KeyBodyStorage，那里还留着
		// 上面读过的原始 body，下游拿到的会是官方形态而不是转换结果。
		if err := common.ReplaceRequestBody(c, jsonData); err != nil {
			arkv3.AbortWithError(c, arkv3.NewServerError("failed to replace request body: "+err.Error()))
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(jsonData))
		c.Request.ContentLength = int64(len(jsonData))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.URL.Path = "/v1/video/generations"
		arkv3.StoreSnapshot(c, snapshot)

		wrapProtocolResponse(c, arkEnvelopeSpec(arkv3.CreateSuccessBody(snapshot.SafetyIdentifier)))
	}
}

// ArkV3Envelope 只做错误信封统一，成功响应原样透出。用于查询 / 列表 / 删除等
// body 本来就已经是官方形态的端点。
func ArkV3Envelope() gin.HandlerFunc {
	return func(c *gin.Context) {
		wrapProtocolResponse(c, arkEnvelopeSpec(nil))
	}
}
