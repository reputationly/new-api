package system_setting

import "sync/atomic"

// outboundUserAgent 发往上游供应商请求的默认 User-Agent（选项 OutboundUserAgent）。
// 空串表示不发 User-Agent 头；渠道请求头覆盖或适配器显式设置的值优先。
// relay 请求并发读、后台保存时写，所以用 atomic。
var outboundUserAgent atomic.Value

func GetOutboundUserAgent() string {
	v, _ := outboundUserAgent.Load().(string)
	return v
}

func SetOutboundUserAgent(ua string) {
	outboundUserAgent.Store(ua)
}
