package common

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
)

// 响应侧的模型名替换。
//
// 为什么需要：model_mapping / model_alias 改的是**发往上游的请求**，上游回来的
// 响应体里写的仍然是它自己的模型名。客户从响应里读到 "model": "gpt-4o"，
// 就等于看到了我们本不该暴露的内部名 —— 渠道重定向做了等于没做。
//
// 设计上有两个刻意的选择：
//
//  1. **只在发生改名时才动手**（ContextKeyPublicModelName 有值）。绝大多数渠道不改名，
//     它们必须继续走"原始字节直接写出"的零成本路径，顺带也避免了对正常响应做任何
//     语义上的改变。
//  2. **只改顶层 model 字段**，用 map[string]json.RawMessage 保留其它字段的原始字节
//     语义。绝不做全局字符串替换 —— 那会把用户内容（回答正文里出现的模型名）也改掉。

// PublicModelName 返回本次请求"对外"的模型名；没有发生改名时返回空串。
func PublicModelName(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return common.GetContextKeyString(c, constant.ContextKeyPublicModelName)
}

// UpstreamModelName 返回本次请求最终发往上游的模型名；没有发生改名时返回空串。
func UpstreamModelName(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return common.GetContextKeyString(c, constant.ContextKeyUpstreamModelName)
}

// RewriteResponseModelJSON 把 JSON 响应里的 model 字段替换成对外名。
//
// 两条路径，按代价从低到高：
//
//  1. **字节级精确替换**（绝大多数情况）：直接找 `"model"` 后面紧跟的那个字符串
//     值，等于上游名就地换掉。不解析 JSON、不重新序列化，因此其它字段的字节、
//     键顺序、浮点格式全部原样保留。
//     它不可能误伤内容正文：正文里的引号在 JSON 里是转义的（`\"model\"`），
//     而这里匹配的是未转义的 `"model"` + `:` + 字符串值。
//  2. **JSON 回退**：上游回给我们的 model 名和我们发过去的不一样时（有些网关会把
//     `gpt-4o` 规范化成 `gpt-4o-2024-08-06`），字节模式匹配不到，退回解析 + 重写。
//
// 解析失败、没有 model 字段、值已经是对外名时都原样返回：这个函数是收尾，
// 不能因为上游返回了奇怪的东西就把正常响应搞坏。
func RewriteResponseModelJSON(c *gin.Context, data []byte) []byte {
	publicName := PublicModelName(c)
	if publicName == "" || len(data) == 0 {
		return data
	}
	// 快路径只在"看起来是 JSON 对象"时启用：IOCopyBytesGracefully 是所有非流式响应
	// 的共同出口，其中包含 STT 这类可能返回纯文本/二进制的路径。手写扫描器不校验
	// 语法，只按 `"model"` + `:` + `"..."` 的形状匹配，所以必须先排除非 JSON 体。
	// （回退路径用标准解析，非 JSON 会直接失败返回原文，不需要这层保护。）
	if looksLikeJSONObject(data) {
		if upstreamName := UpstreamModelName(c); upstreamName != "" && upstreamName != publicName {
			if rewritten, ok := replaceModelFieldValue(data, upstreamName, publicName); ok {
				return rewritten
			}
		}
	}
	return rewriteModelFieldViaJSON(c, data, publicName)
}

var modelFieldKeyBytes = []byte(`"model"`)

// replaceModelFieldValue 在原始字节里定位 model 字段的字符串值并就地换掉。
//
// 手写扫描而不是 bytes.Replace：键和值之间可能有空白（Python 的 json.dumps 默认
// 就是 `"model": "gpt-4o"`），固定模式串会漏。这里只做"跳空白 + 找冒号 + 读字符串"
// 三件事，代价是一次线性扫描，不分配（除了最终要产出的那份新字节）。
func replaceModelFieldValue(data []byte, upstreamName, publicName string) ([]byte, bool) {
	// 替换**所有**等于上游名的 model 字段，而不是只替换第一处：转换/聚合类的响应
	// 可能既有顶层 model、又有嵌套的 response.model，只改一处等于留了个漏。
	var out []byte
	last := 0
	replaced := false
	searchFrom := 0
	for searchFrom < len(data) {
		rel := bytes.Index(data[searchFrom:], modelFieldKeyBytes)
		if rel < 0 {
			break
		}
		pos := searchFrom + rel + len(modelFieldKeyBytes)
		pos = skipJSONSpace(data, pos)
		if pos >= len(data) || data[pos] != ':' {
			searchFrom = pos
			continue
		}
		pos = skipJSONSpace(data, pos+1)
		if pos >= len(data) || data[pos] != '"' {
			searchFrom = pos
			continue
		}
		valueStart := pos + 1
		valueEnd := valueStart
		for valueEnd < len(data) && data[valueEnd] != '"' {
			if data[valueEnd] == '\\' {
				valueEnd++
			}
			valueEnd++
		}
		if valueEnd >= len(data) {
			break
		}
		if string(data[valueStart:valueEnd]) == upstreamName {
			if out == nil {
				out = make([]byte, 0, len(data)+len(publicName)-len(upstreamName))
			}
			out = append(out, data[last:valueStart]...)
			out = append(out, publicName...)
			last = valueEnd
			replaced = true
		}
		searchFrom = valueEnd + 1
	}
	if !replaced {
		return nil, false
	}
	out = append(out, data[last:]...)
	return out, true
}

// looksLikeJSONObject 判断首个非空白字节是否为 `{`。
//
// 只用于决定要不要走手写扫描器，不用于判断"是不是合法 JSON"：合法性的最终判据
// 仍是回退路径里的标准解析。
func looksLikeJSONObject(data []byte) bool {
	for _, b := range data {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '{':
			return true
		default:
			return false
		}
	}
	return false
}

func skipJSONSpace(data []byte, pos int) int {
	for pos < len(data) {
		switch data[pos] {
		case ' ', '\t', '\n', '\r':
			pos++
		default:
			return pos
		}
	}
	return pos
}

// rewriteModelFieldViaJSON 是回退路径：上游的 model 值与我们发过去的不同时用它。
func rewriteModelFieldViaJSON(c *gin.Context, data []byte, publicName string) []byte {
	// 图片、音频、纯错误体都不带 model，一次子串检查就能跳过。
	if !bytes.Contains(data, modelFieldKeyBytes) {
		return data
	}
	payload := make(map[string]json.RawMessage)
	if err := common.Unmarshal(data, &payload); err != nil {
		return data
	}
	current, ok := payload["model"]
	if !ok {
		return data
	}
	var currentName string
	if err := common.Unmarshal(current, &currentName); err != nil {
		return data
	}
	if currentName == publicName {
		return data
	}
	encoded, err := common.Marshal(publicName)
	if err != nil {
		return data
	}
	payload["model"] = encoded
	rewritten, err := common.Marshal(payload)
	if err != nil {
		return data
	}
	return rewritten
}

// RewriteResponseModelString 是 RewriteResponseModelJSON 的 SSE 版本。
//
// 流式响应每个 chunk 都会过一次，所以先做零分配的字节预筛：连 `"model"` 都不含的
// chunk（ping、注释、[DONE]）直接原样返回，一次拷贝都不做。
func RewriteResponseModelString(c *gin.Context, data string) string {
	if data == "" || PublicModelName(c) == "" {
		return data
	}
	if !strings.Contains(data, `"model"`) {
		return data
	}
	rewritten := RewriteResponseModelJSON(c, []byte(data))
	if len(rewritten) == 0 {
		return data
	}
	return string(rewritten)
}

// ScrubLeakyResponseHeaders 删掉"值里带着上游模型名"的响应头。
//
// 上游确实会在少数情况下回显模型名（兼容网关的 x-model 之类）。头是逐字节透传的
// （见 service.IOCopyBytesGracefully），所以这里按"值包含上游名"一刀切地丢，
// 而不是维护一份永远会漏的名单。只在发生改名时才启用。
func ScrubLeakyResponseHeaders(c *gin.Context, header http.Header) {
	upstreamName := UpstreamModelName(c)
	if upstreamName == "" || header == nil {
		return
	}
	for name, values := range header {
		leaky := false
		for _, value := range values {
			if strings.Contains(value, upstreamName) {
				leaky = true
				break
			}
		}
		if leaky {
			delete(header, name)
		}
	}
}

// RewriteUpstreamText 把一段面向用户的文本（错误消息、错误日志）里的上游名换回对外名。
//
// 这是"完全替换"里最容易被忽略的一环：上游的 400/404 往往直接把真实模型名写在
// message 里（"The model `gpt-4o` does not exist"），而错误恰恰是探测模型关系最省事的
// 手段。只替换发生改名时已知的那两个名字，不做任何模糊匹配。
func RewriteUpstreamText(c *gin.Context, text string) string {
	if text == "" {
		return text
	}
	upstreamName := UpstreamModelName(c)
	publicName := PublicModelName(c)
	if upstreamName == "" || publicName == "" || upstreamName == publicName {
		return text
	}
	if !strings.Contains(text, upstreamName) {
		return text
	}
	return strings.ReplaceAll(text, upstreamName, publicName)
}
