package system_setting

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultOutboundUserAgent(t *testing.T) {
	require.Equal(t, "node", GetOutboundUserAgent())
}

func TestValidateOutboundUserAgent(t *testing.T) {
	require.NoError(t, ValidateOutboundUserAgent(""))
	require.NoError(t, ValidateOutboundUserAgent("  Mozilla/5.0 (X11; Linux x86_64)  "))
	require.Error(t, ValidateOutboundUserAgent("a\nb"))
	require.Error(t, ValidateOutboundUserAgent("a\r\nX-Evil: 1"))
	require.NoError(t, ValidateOutboundUserAgent(strings.Repeat("a", 512)))
	require.Error(t, ValidateOutboundUserAgent(strings.Repeat("a", 513)))
}

// 库里的脏值（直接改库 / 校验上线前写入）不能被发到上游：那会让所有不带显式 UA 的
// 上游请求被 net/http 拒绝。读取侧必须自己兜底成「不发」。
func TestGetOutboundUserAgentIgnoresStoredInvalidValue(t *testing.T) {
	prev := GetOutboundUserAgent()
	t.Cleanup(func() { SetOutboundUserAgent(prev) })

	SetOutboundUserAgent("ok/1.0\nX-Evil: 1")
	require.Empty(t, GetOutboundUserAgent())

	SetOutboundUserAgent("  ok/1.0  ")
	require.Equal(t, "ok/1.0", GetOutboundUserAgent(), "返回值要 trim，与设置页显示一致")
}
