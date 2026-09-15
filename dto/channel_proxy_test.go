package dto

import (
	"strings"
	"testing"
)

// **真实事故**：有人把 "root" 填进界面上的「代理地址」。
// 保存成功，之后该渠道每个请求都 500，日志里只有一句
// `unsupported proxy scheme: , must be ...` —— scheme 那格是空的，
// 既不知道是哪个渠道，也不知道填了什么。
func TestValidateProxy_RejectsBareWord(t *testing.T) {
	err := ValidateProxy("root")
	if err == nil {
		t.Fatal("root 被放过了，保存时不报、发请求时才炸")
	}
	// 报错必须带上用户填的原值，否则运维还是得去翻数据库才知道错在哪。
	if !strings.Contains(err.Error(), `"root"`) {
		t.Errorf("报错里没有用户填的原值: %v", err)
	}
	if !strings.Contains(err.Error(), "socks5") {
		t.Errorf("报错里没说正确格式: %v", err)
	}
}

func TestValidateProxy(t *testing.T) {
	ok := []string{
		"",
		"http://127.0.0.1:8080",
		"https://proxy.example.com:443",
		"socks5://user:pass@10.0.0.1:1080",
		"socks5h://10.0.0.1:1080",
	}
	for _, v := range ok {
		if err := ValidateProxy(v); err != nil {
			t.Errorf("ValidateProxy(%q) 该通过却报错: %v", v, err)
		}
	}

	bad := []string{
		"root",
		"root:8080",       // 解析成 Scheme="root"，另一条路径
		"ftp://host:21",   // 协议不在名单里
		"socks4://h:1080", // 相近但确实不支持
		"socks5://",       // 有协议没主机，不拦的话错误发生在拨号阶段
		"127.0.0.1:1080",  // 漏了协议头，最常见的手误

		// 首尾空白：trim 一下放行等于存一个"存得进、用不了"的值 ——
		// 真正发请求的 NewProxyHttpClient 拿到的是未 trim 的原串，它不 trim。
		" socks5://127.0.0.1:1080",
		"socks5://127.0.0.1:1080 ",
		"socks5://127.0.0.1:1080\n",
		"   ", // 全空白也不等于空串，同样会走到 url.Parse
	}
	for _, v := range bad {
		if err := ValidateProxy(v); err == nil {
			t.Errorf("ValidateProxy(%q) 该被拦下", v)
		}
	}
}

// 注:「名单与 NewProxyHttpClient 的 switch 对齐」这条不变量**不在这里测** ——
// dto 不能 import service(会成环),在本包里把 ProxySchemes 喂回 ValidateProxy
// 只是自己验自己,加一个 service 不认识的协议照样绿。真正的对齐检查在
// service/http_client_proxy_test.go。
