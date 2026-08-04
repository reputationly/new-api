package mediastore

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
)

// data-url 解析。收口在 mediastore 是因为这里的产物直接决定 OBS 对象的 Content-Type，
// 而那正是上游 GET 我方签名 URL 时看到的那个头——Ark / Kling 可能据此分流。
//
// 仓库里此前有 6 处各自实现的 data-url 剥头（nfsinput.extForData、geminitask.parseDataURI、
// jimeng.stripDataURLPrefix、service.DecodeBase64FileData 等），各自服务于不同上游的形态要求，
// 不在本次合并范围；这里只为「落 OBS」这一条路径提供 MIME + 扩展名 + 字节。

// ParsedDataURL data-url 的解析结果。
type ParsedDataURL struct {
	MIME string // 归一化后的 MIME（客户端声明优先，缺失/不可信时按内容嗅探）
	Ext  string // 不含点，与 BuildKey 的 ext 语义一致；认不出为 "bin"
	Data []byte
}

// IsDataURL 判断是否为 data: URI。不解码，供遍历时的廉价预筛。
func IsDataURL(raw string) bool {
	return strings.HasPrefix(raw, "data:")
}

// ParseDataURL 解析 base64 形态的 data-url。
//
// limit > 0 时在**解码前**按 base64 长度预判大小，超限直接返回 ErrObjectTooLarge——
// 避免为了扔掉一个 200 MB 的串而先把它解码成 150 MB 的字节切片。
// 非 base64 形态（纯文本 data-url）返回错误：媒体不会以那种形态出现。
func ParseDataURL(raw string, limit int64) (ParsedDataURL, error) {
	if !IsDataURL(raw) {
		return ParsedDataURL{}, fmt.Errorf("mediastore: not a data url")
	}
	comma := strings.Index(raw, ",")
	if comma < 0 {
		return ParsedDataURL{}, fmt.Errorf("mediastore: malformed data url (no comma)")
	}
	header, payload := raw[len("data:"):comma], raw[comma+1:]
	if payload == "" {
		return ParsedDataURL{}, fmt.Errorf("mediastore: empty data url payload")
	}

	// header 形如 "image/png;base64" 或 "image/png;charset=utf-8;base64" 或空（默认 text/plain）。
	isBase64 := false
	mime := ""
	for i, part := range strings.Split(header, ";") {
		part = strings.TrimSpace(part)
		if strings.EqualFold(part, "base64") {
			isBase64 = true
			continue
		}
		if i == 0 {
			mime = strings.ToLower(part)
		}
	}
	if !isBase64 {
		return ParsedDataURL{}, fmt.Errorf("mediastore: unsupported non-base64 data url")
	}

	// 解码前预判：base64 每 4 字符产出 3 字节，扣掉尾部 padding 才是精确的解码长度上界。
	// 不扣会高估 1–2 字节，使恰好顶到限额的文件在这里被误判超限，
	// 而下面解码后的 len(data) > limit 本会放行——两道闸的边界必须一致。
	if limit > 0 {
		tail := payload
		if len(tail) > 2 {
			tail = tail[len(tail)-2:]
		}
		if int64(len(payload))/4*3-int64(strings.Count(tail, "=")) > limit {
			return ParsedDataURL{}, ErrObjectTooLarge
		}
	}

	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		// 有的客户端发不带 padding 的 base64。
		data, err = base64.RawStdEncoding.DecodeString(payload)
		if err != nil {
			return ParsedDataURL{}, fmt.Errorf("mediastore: decode data url: %w", err)
		}
	}
	if len(data) == 0 {
		return ParsedDataURL{}, fmt.Errorf("mediastore: empty data url payload")
	}
	if limit > 0 && int64(len(data)) > limit {
		return ParsedDataURL{}, ErrObjectTooLarge
	}

	// 客户端声明的 MIME 是权威的；缺失或退化成 octet-stream 时才按内容嗅探。
	// 不做 magic-bytes 拒绝——这条路径是传输优化，不该改变"用户被允许发什么"。
	if mime == "" || mime == "application/octet-stream" {
		if sniffed := sniffMIME(data); sniffed != "" {
			mime = sniffed
		}
	}
	ext := ExtForMIME(mime)
	if ext == "" {
		if sniffed := sniffMIME(data); sniffed != "" {
			if ext = ExtForMIME(sniffed); ext != "" {
				mime = sniffed
			}
		}
	}
	if ext == "" {
		ext = "bin"
	}
	if mime == "" {
		mime = "application/octet-stream"
	}
	return ParsedDataURL{MIME: mime, Ext: ext, Data: data}, nil
}

// sniffMIME 用标准库按内容嗅探，去掉 "; charset=..." 参数。
// DetectContentType 只看前 512 字节，认不出会返回 application/octet-stream（此处折成 ""）。
func sniffMIME(data []byte) string {
	ct := http.DetectContentType(data)
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	ct = strings.ToLower(strings.TrimSpace(ct))
	if ct == "application/octet-stream" {
		return ""
	}
	return ct
}

// ExtForMIME 由 MIME 推扩展名（不含点）。认不出返回 ""，由调用方决定兜底。
// 这是 InferContentType 的反方向，两张表应保持同步。
func ExtForMIME(mime string) string {
	switch strings.ToLower(strings.TrimSpace(mime)) {
	case "image/png":
		return "png"
	case "image/jpeg", "image/jpg":
		return "jpg"
	case "image/webp":
		return "webp"
	case "image/gif":
		return "gif"
	case "image/bmp":
		return "bmp"
	case "image/heic":
		return "heic"
	case "image/avif":
		return "avif"
	case "video/mp4":
		return "mp4"
	case "video/webm":
		return "webm"
	case "video/quicktime":
		return "mov"
	case "video/x-matroska":
		return "mkv"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return "wav"
	case "audio/mpeg", "audio/mp3":
		return "mp3"
	case "audio/flac", "audio/x-flac":
		return "flac"
	case "audio/ogg", "application/ogg":
		return "ogg"
	case "audio/mp4", "audio/x-m4a":
		return "m4a"
	case "audio/aac":
		return "aac"
	default:
		return ""
	}
}
