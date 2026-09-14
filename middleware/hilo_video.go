package middleware

import (
	"bytes"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/relay/hilo"
	"github.com/QuantumNous/new-api/setting"
)

// HiloVideoConvert 处理 MiniMax Design 客户端的视频生成：
// 官方形状 body → 统一任务契约 → 改写路径交给 controller.RelayTask。
//
// # 为什么不自己调聚合流水线
//
// 转成统一契约之后走既有链路，**聚合展开、分段计费、日志、限流全部白拿**。
// 自己调的话每一样都要重写，而计费那份尤其危险：主链路的结算还有分层计费、
// 视频计费矩阵等分支，抄一份出来将来主逻辑加了新维度这里不会跟上，
// 而且不报错 —— 只是账悄悄算少了。
//
// 分段计费也正是这么来的：展开之后每一段都是一次普通调用，各自预扣、
// 各自记账、各自出现在日志里，不需要为聚合模型单独定价。
//
// # 位置：必须在 TokenAuth 之前
//
// 和 MiniMaxV2CreateConvert 同一个理由 —— 这样鉴权失败的响应也走同一个
// 信封。而且**改写 model 必须早于 Distribute**：那里才做聚合展开和选渠道。
//
// # 分派
//
// 客户端对两种玩法只发一个接口，把玩法编码在「用哪个字段装图」上
// （first_frame_image / reference_images，实测）。而我们平台上首尾帧族和
// 参考族是**两个 checkpoint、两条流水线**，所以这里要按素材反推玩法，
// 再查目录拿到对应的平台模型名。
func HiloVideoConvert() gin.HandlerFunc {
	return func(c *gin.Context) {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			abortHilo(c, http.StatusBadRequest, "failed to read request body: "+err.Error())
			return
		}
		raw, err := storage.Bytes()
		if err != nil {
			abortHilo(c, http.StatusBadRequest, "failed to read request body: "+err.Error())
			return
		}

		var req hilo.VideoRequest
		if err := common.Unmarshal(raw, &req); err != nil {
			abortHilo(c, http.StatusBadRequest, "request body is not valid JSON: "+err.Error())
			return
		}

		// 客户端发回来的是目录里的 `model_name`（没配则是 `id`），不是我们
		// 内部的平台模型名 —— 那个它不知道。
		entry, ok := setting.FindHiloEntry(req.Model)
		if !ok {
			abortHilo(c, http.StatusBadRequest,
				"unknown model "+req.Model+"：它不在当前下发的模型目录里")
			return
		}
		mode := req.DetectMode()
		platformModel := entry.PlatformModelFor(string(mode))

		body, err := req.ToTaskSubmit(platformModel)
		if err != nil {
			abortHilo(c, http.StatusBadRequest, err.Error())
			return
		}
		jsonData, err := common.Marshal(body)
		if err != nil {
			abortHilo(c, http.StatusInternalServerError, "failed to build upstream request: "+err.Error())
			return
		}

		// **必须走 ReplaceRequestBody。** 只 c.Set 是不够的 —— GetRequestBody
		// 优先读 KeyBodyStorage，那里还留着上面读过的原始 body，下游拿到的会是
		// 官方形态而不是转换结果。（这条是 minimax_v2_adapter 踩出来的。）
		if err := common.ReplaceRequestBody(c, jsonData); err != nil {
			abortHilo(c, http.StatusInternalServerError, "failed to replace request body: "+err.Error())
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(jsonData))
		c.Request.ContentLength = int64(len(jsonData))
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.URL.Path = "/v1/video/generations"

		common.SysLog("[hilo] 视频生成 model=" + req.Model + " mode=" + string(mode) +
			" → " + platformModel)
		c.Next()
	}
}

// abortHilo 用客户端认得的形状回错误。
//
// 官方 gateway 的 BackendHttpClient 会把非 2xx 的 body 原样塞进错误信息里
// （实测："Qwen API 404: {...}"），所以这里的 message 会一路显示到画布的
// 失败节点上 —— 值得写人话。
func abortHilo(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"ok": false, "error": message})
	c.Abort()
}
