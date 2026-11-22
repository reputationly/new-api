package middleware

// 方舟图片协议（/api/v3/images/generations）的 gin 接线。
//
// 与视频那条一样，协议实现在 relay/arkv3，这里只负责：改写 body 与路径、把成功响应
// 原样透出、把错误统一成方舟信封。
//
// 为什么成功响应不需要改写：我们自己的同步生图响应就是 {data:[{url}], created, usage}，
// 方舟客户端读的就是 data[].url（见内置 volcengine-ark 适配器的 parseSubmit）。

import (
	"bytes"
	"io"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/arkv3"

	"github.com/gin-gonic/gin"
)

// ArkV3ImageConvert 处理 POST /api/v3/images/generations：方舟 body → OpenAI 图片 body，
// 路径改写到 /v1/images/generations，然后交给既有的图片入口（imageEntry）。
//
// 必须排在 TokenAuth 之前：401 也要走方舟信封，否则 SDK 在鉴权失败时解析不了响应。
func ArkV3ImageConvert() gin.HandlerFunc {
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

		body, apiErr := arkv3.ConvertImageRequest(raw)
		if apiErr != nil {
			arkv3.AbortWithError(c, apiErr)
			return
		}
		jsonData, err := common.Marshal(body)
		if err != nil {
			arkv3.AbortWithError(c, arkv3.NewServerError("failed to build upstream request: "+err.Error()))
			return
		}
		// 必须走 ReplaceRequestBody：只 c.Set 的话下游 GetRequestBody 仍会读到
		// KeyBodyStorage 里那份方舟原始 body（见 minimax_v2_adapter 踩过的同一条坑）。
		if err := common.ReplaceRequestBody(c, jsonData); err != nil {
			arkv3.AbortWithError(c, arkv3.NewServerError("failed to replace request body: "+err.Error()))
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(jsonData))
		c.Request.ContentLength = int64(len(jsonData))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.URL.Path = "/v1/images/generations"

		wrapProtocolResponse(c, arkEnvelopeSpec(nil))
	}
}
