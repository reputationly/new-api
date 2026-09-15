package service

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
)

// **保存时的名单必须和建客户端时的 switch 对齐。**
//
// 这条检查只能放在 service 包:dto 不能 import service(成环),而在 dto 里把
// ProxySchemes 喂回 ValidateProxy 是自己验自己 —— 往名单里加一个 switch 不
// 认识的协议(比如 socks4),那种测试照样全绿,而线上表现是**保存时通过、
// 每个请求被拒**,只有在渠道真正被调用时才暴露。
func TestProxySchemesMatchClientSwitch(t *testing.T) {
	for _, scheme := range dto.ProxySchemes {
		_, err := NewProxyHttpClient(scheme + "://127.0.0.1:1080")
		if err != nil && strings.Contains(err.Error(), "unsupported proxy scheme") {
			t.Errorf("dto.ProxySchemes 里的 %q 建不出客户端: %v\n"+
				"  → 保存时会放行，但该渠道每个请求都会失败", scheme, err)
		}
	}
}

// 反向:名单外的协议必须真的被拒。没有这条,上面那条可能因为"什么都不拒"而空过。
func TestUnlistedProxySchemeRejected(t *testing.T) {
	for _, scheme := range []string{"socks4", "ftp", "ws"} {
		if _, err := NewProxyHttpClient(scheme + "://127.0.0.1:1080"); err == nil {
			t.Errorf("名单外的协议 %q 被放行了", scheme)
		}
	}
}

// **报错里不能带完整代理地址。**
//
// 这个 error 会被 relay/channel/api_request.go 包一层，最终由
// controller/relay.go 的 c.JSON 原样回给 API 调用方 —— 不是管理员。而
// common.MaskSensitiveInfo 的 URL 正则只认 http/https，socks 系整串不被当
// URL，userinfo 会原样穿过去。
func TestProxyErrorDoesNotLeakCredentials(t *testing.T) {
	_, err := NewProxyHttpClient("socks4://bob:hunter2@proxy.example.com:1080")
	if err == nil {
		t.Fatal("socks4 该被拒")
	}
	for _, secret := range []string{"hunter2", "bob", "proxy.example.com"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("回给调用方的报错里带了 %q: %v", secret, err)
		}
	}
	// 仍要说清楚问题出在哪，否则又回到"报错没信息"的老路
	if !strings.Contains(err.Error(), "socks4") {
		t.Errorf("报错没说清楚是哪个协议不支持: %v", err)
	}
}
