package system_setting

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"golang.org/x/net/http/httpguts"
)

// DefaultOutboundUserAgent 未配置时的出站 UA：Node.js 内置 fetch 的默认值，不指向任何具体产品。
const DefaultOutboundUserAgent = "node"

// maxOutboundUserAgentLen 与请求头的实际上限对齐：几 KB 的 UA 会让 HTTP/2 的 header list
// 超限（或上游直接 431），同样等于打挂所有没配渠道覆盖的上游请求。
const maxOutboundUserAgentLen = 512

// outboundUserAgent 发往上游供应商请求的默认 User-Agent（选项 OutboundUserAgent）。
// 空串表示不发 User-Agent 头；渠道请求头覆盖或适配器显式设置的值优先。
// relay 请求并发读、后台保存时写，所以用 atomic。
var outboundUserAgent atomic.Value

func init() {
	outboundUserAgent.Store(DefaultOutboundUserAgent)
}

var outboundUAInvalidLogged atomic.Bool

func GetOutboundUserAgent() string {
	v, _ := outboundUserAgent.Load().(string)
	ua := strings.TrimSpace(v)
	if ua == "" {
		return ""
	}
	// 库里可能存着被直接写入的非法值（手工改库、或校验上线前写入的旧值）。net/http 会因此
	// 拒绝**每一个**没配渠道覆盖的上游请求——宁可退回「不发 UA」，也不能让整站上游全挂。
	if err := ValidateOutboundUserAgent(ua); err != nil {
		if outboundUAInvalidLogged.CompareAndSwap(false, true) {
			common.SysLog("WARNING: ignoring invalid OutboundUserAgent: " + err.Error())
		}
		return ""
	}
	return ua
}

func SetOutboundUserAgent(ua string) {
	outboundUserAgent.Store(ua)
}

// ValidateOutboundUserAgent 拒绝含换行等控制字符的值：net/http 会直接拒发带非法头的请求，
// 存进去所有没配渠道覆盖的上游请求都会失败。
func ValidateOutboundUserAgent(ua string) error {
	trimmed := strings.TrimSpace(ua)
	if !httpguts.ValidHeaderFieldValue(trimmed) {
		return errors.New("出站 User-Agent 不能包含换行等控制字符")
	}
	if utf8.RuneCountInString(trimmed) > maxOutboundUserAgentLen {
		return fmt.Errorf("出站 User-Agent 不能超过 %d 个字符", maxOutboundUserAgentLen)
	}
	return nil
}
