package dto

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// ProxySchemes 是 Proxy 字段允许的协议。
//
// **这里是唯一的一份。** service 里建代理客户端的 switch 必须跟着它走，
// 否则会出现"保存时通过、发请求时被拒"或者反过来的情况——两种都只能在
// 线上暴露。
var ProxySchemes = []string{"http", "https", "socks5", "socks5h"}

// ValidateProxy 检查渠道的代理地址。空串表示不走代理，合法。
//
// # 为什么必须在保存时拦
//
// 不拦的话，一个填错的值会一直静默地躺在配置里，直到有请求真正打到这个
// 渠道才炸，而且错误长这样：
//
//	new proxy http client failed: unsupported proxy scheme: , must be http, https, socks5 or socks5h
//
// scheme 那一格是**空的**——因为 url.Parse("root") 解出来 Scheme="" 、
// Path="root"。于是这句报错既没说是哪个渠道，也没说用户到底填了什么，
// 只告诉你"空的不行"。实际发生过：有人把 "root" 填进了界面上的「代理地址」，
// 现象是该渠道所有请求 500，日志里就这一句。
func ValidateProxy(proxy string) error {
	if proxy == "" {
		return nil
	}
	// **不能只 trim 自己手里这一份。** 校验通过后存进库、发请求时交给
	// service.NewProxyHttpClient 的是**原始那一份**,它不 trim;连它开头
	// 那句 `proxyURL == ""` 的空值判断也不 trim。所以 trim 一下放行等于
	// 亲手放一个"存得进、用不了"的值进去 ——
	//
	//	" socks5://h:1080"  → url.Parse 得到 Scheme=""(就是报错里那个空的)
	//	"socks5://h:1080 "  → url.Parse 直接失败 invalid port "1080 "
	//	"   "               → 不等于空串,一路走到 Parse 再报 scheme 为空
	//
	// 从文档里粘贴带尾随换行是常事,所以这条得明说,而不是替他悄悄改掉:
	// 悄悄改意味着库里存的和他在界面上看到的不是一个东西。
	if trimmed := strings.TrimSpace(proxy); trimmed != proxy {
		if trimmed == "" {
			return fmt.Errorf("代理地址只填了空白字符;不走代理请清空该字段")
		}
		return fmt.Errorf("代理地址 %q 首尾有空白字符,请去掉后再保存(粘贴时容易带上)", proxy)
	}

	parsed, err := url.Parse(proxy)
	if err != nil {
		return fmt.Errorf("代理地址 %q 不是合法的 URL：%w（需要 %s 开头）",
			proxy, err, strings.Join(ProxySchemes, " / "))
	}
	// **不能只看 Scheme。** "root" 会解析成 Scheme="" ——报错里那一格是空的
	// 正是这么来的。"root:8080" 又会解析成 Scheme="root"、Host=""。
	// 两种都得在这里说清楚用户填了什么。
	if !proxySchemeAllowed(parsed.Scheme) {
		return fmt.Errorf("代理地址 %q 的协议是 %q，只支持 %s；"+
			"格式形如 socks5://user:pass@host:port（不填则不走代理）",
			proxy, parsed.Scheme, strings.Join(ProxySchemes, " / "))
	}
	// 有 scheme 没 host 同样连不上，且报错发生在拨号阶段、更难定位。
	if parsed.Host == "" {
		return fmt.Errorf("代理地址 %q 缺少主机名，格式形如 socks5://host:port", proxy)
	}
	return nil
}

func proxySchemeAllowed(scheme string) bool {
	return slices.Contains(ProxySchemes, scheme)
}
