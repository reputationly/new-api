package system_setting

import (
	"errors"
	"strings"
	"sync/atomic"

	"golang.org/x/net/http/httpguts"
)

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

// ValidateOutboundUserAgent 拒绝含换行等控制字符的值：net/http 会直接拒发带非法头的请求，
// 存进去所有没配渠道覆盖的上游请求都会失败。
func ValidateOutboundUserAgent(ua string) error {
	if !httpguts.ValidHeaderFieldValue(strings.TrimSpace(ua)) {
		return errors.New("出站 User-Agent 不能包含换行等控制字符")
	}
	return nil
}
