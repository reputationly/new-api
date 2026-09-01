package common

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"

	"github.com/gin-gonic/gin"
)

// 同步生图链路的成品引用在请求上下文里的传递。
//
// 为什么要绕一趟 context：落 OBS 发生在各渠道自己的响应处理里（自建走 gpustackplus
// 适配器的 PersistImageNFSToOBS，第三方走 OpenaiHandlerWithUsage 的
// RewriteImageResponseToOBS），而写任务记录发生在 relay.ImageHelper 的收口处——
// 中间隔着 Adaptor.DoResponse 这层接口，返回值只有 usage，塞不下第二个产物。
//
// 存 key 而不是签名 URL：签名 URL 有有效期，存进库过期即失效。任务表统一存
// obs://<key> 占位符，查询时实时签发（与异步链路同一套，见 relay/image_task_response.go）。

// AppendSyncImageOBSKeys 追加本次落盘的 OBS 对象 key。
// 空 key 直接忽略，调用方不必自己判空。
func AppendSyncImageOBSKeys(c *gin.Context, keys ...string) {
	if c == nil {
		return
	}
	existing := common.GetContextKeyStringSlice(c, constant.ContextKeySyncImageOBSKeys)
	for _, k := range keys {
		if k != "" {
			existing = append(existing, k)
		}
	}
	if len(existing) == 0 {
		return
	}
	common.SetContextKey(c, constant.ContextKeySyncImageOBSKeys, existing)
}

// GetSyncImageOBSKeys 取本次请求落盘的全部 OBS 对象 key，没有则返回 nil。
func GetSyncImageOBSKeys(c *gin.Context) []string {
	if c == nil {
		return nil
	}
	return common.GetContextKeyStringSlice(c, constant.ContextKeySyncImageOBSKeys)
}

// ResetSyncImageOBSKeys 清空上一次尝试留下的 key，必须在每次 relay 尝试开始时调用。
//
// 重试循环（controller/relay.go）复用同一个 gin.Context，而落盘发生在返回错误**之前**
// ——gpustackplus 适配器落完 OBS 才组响应，那一步的 Marshal 失败返回的是可重试错误
// （relay/channel/gpustackplus/adaptor.go:628）。不清的话第二次尝试成功时 context 里
// 躺着两个 key，任务记录取 keys[0] 指向的是第一次尝试的图；跨渠道重试时这两张图甚至
// 来自不同渠道，于是任务日志里预览到的和客户端实际拿到的不是同一张。
func ResetSyncImageOBSKeys(c *gin.Context) {
	if c == nil {
		return
	}
	common.SetContextKey(c, constant.ContextKeySyncImageOBSKeys, []string(nil))
}
