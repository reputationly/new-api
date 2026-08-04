package mediastore

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// pngBytes 一个最小合法 PNG 的前若干字节（含 magic），够 http.DetectContentType 识别。
var pngBytes = []byte{
	0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, // \x89PNG\r\n\x1a\n
	0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
}

func dataURL(mime string, data []byte) string {
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func TestIsDataURL(t *testing.T) {
	require.True(t, IsDataURL("data:image/png;base64,AAAA"))
	require.False(t, IsDataURL("https://example.com/a.png"))
	require.False(t, IsDataURL("task:123"))
	require.False(t, IsDataURL(""))
}

func TestParseDataURL_DeclaredMIME(t *testing.T) {
	got, err := ParseDataURL(dataURL("image/png", pngBytes), 0)
	require.NoError(t, err)
	require.Equal(t, "image/png", got.MIME)
	require.Equal(t, "png", got.Ext)
	require.Equal(t, pngBytes, got.Data)
}

func TestParseDataURL_WithCharsetParam(t *testing.T) {
	raw := "data:image/jpeg;charset=utf-8;base64," + base64.StdEncoding.EncodeToString(pngBytes)
	got, err := ParseDataURL(raw, 0)
	require.NoError(t, err)
	require.Equal(t, "image/jpeg", got.MIME, "客户端声明优先，不被内容嗅探覆盖")
	require.Equal(t, "jpg", got.Ext)
}

// 声明缺失或退化成 octet-stream 时按内容嗅探兜底。
func TestParseDataURL_SniffFallback(t *testing.T) {
	for _, declared := range []string{"", "application/octet-stream"} {
		got, err := ParseDataURL(dataURL(declared, pngBytes), 0)
		require.NoError(t, err, declared)
		require.Equal(t, "image/png", got.MIME, "declared=%q", declared)
		require.Equal(t, "png", got.Ext, "declared=%q", declared)
	}
}

// 认不出的内容不报错，落到 bin —— 本路径是传输优化，不该改变"用户被允许发什么"。
func TestParseDataURL_UnknownFallsBackToBin(t *testing.T) {
	got, err := ParseDataURL(dataURL("application/x-weird", []byte{0x01, 0x02, 0x03, 0x04}), 0)
	require.NoError(t, err)
	require.Equal(t, "bin", got.Ext)
}

func TestParseDataURL_RawBase64NoPadding(t *testing.T) {
	raw := "data:image/png;base64," + base64.RawStdEncoding.EncodeToString(pngBytes)
	got, err := ParseDataURL(raw, 0)
	require.NoError(t, err)
	require.Equal(t, pngBytes, got.Data)
}

func TestParseDataURL_LimitRejectsBeforeDecode(t *testing.T) {
	// 4 MB 的 base64 串，limit 设 1 KB：应在解码前就被拒。
	big := dataURL("image/png", make([]byte, 4*1024*1024))
	_, err := ParseDataURL(big, 1024)
	require.ErrorIs(t, err, ErrObjectTooLarge)
}

// 恰好顶到限额的文件必须放行。解码前的预判若不扣掉尾部 padding 会高估 1–2 字节，
// 与解码后的 len(data) > limit 那道闸在边界上不一致，此处把三种 padding 形态都钉住。
func TestParseDataURL_LimitAllowsExactBoundary(t *testing.T) {
	for _, limit := range []int64{3000, 3001, 3002} { // 覆盖 limit%3 == 0/1/2
		payload := make([]byte, limit)
		copy(payload, pngBytes)
		got, err := ParseDataURL(dataURL("image/png", payload), limit)
		require.NoErrorf(t, err, "limit=%d", limit)
		require.Lenf(t, got.Data, int(limit), "limit=%d", limit)
	}
}

func TestParseDataURL_LimitRejectsOneByteOver(t *testing.T) {
	for _, limit := range []int64{3000, 3001, 3002} {
		_, err := ParseDataURL(dataURL("image/png", make([]byte, limit+1)), limit)
		require.ErrorIsf(t, err, ErrObjectTooLarge, "limit=%d", limit)
	}
}

func TestParseDataURL_LimitAllowsUnderSize(t *testing.T) {
	got, err := ParseDataURL(dataURL("image/png", pngBytes), 1024)
	require.NoError(t, err)
	require.Equal(t, pngBytes, got.Data)
}

func TestParseDataURL_Rejects(t *testing.T) {
	cases := map[string]string{
		"非 data url":  "https://example.com/a.png",
		"无逗号":         "data:image/png;base64",
		"空 payload":   "data:image/png;base64,",
		"非 base64 形态": "data:text/plain,hello",
		"坏 base64":    "data:image/png;base64,!!!!not-base64!!!!",
	}
	for name, raw := range cases {
		_, err := ParseDataURL(raw, 0)
		require.Error(t, err, name)
	}
}

func TestExtForMIME(t *testing.T) {
	require.Equal(t, "png", ExtForMIME("image/png"))
	require.Equal(t, "jpg", ExtForMIME("IMAGE/JPEG"), "大小写不敏感")
	require.Equal(t, "mp4", ExtForMIME(" video/mp4 "), "两端空白容忍")
	require.Equal(t, "m4a", ExtForMIME("audio/mp4"))
	require.Equal(t, "", ExtForMIME("application/x-unknown"), "未知返回空串，由调用方兜底")
}

// ExtForMIME 与 InferContentType 是互为反向的两张表，必须保持同步：
// 落 OBS 时用前者定扩展名，上游 GET 时看到的 Content-Type 由后者按扩展名推出。
// 二者若漂移，一张 png 会以 application/octet-stream 送达上游。
func TestExtForMIME_RoundTripsWithInferContentType(t *testing.T) {
	for _, mime := range []string{
		"image/png", "image/jpeg", "image/webp", "image/gif", "image/bmp",
		"video/mp4", "video/webm", "video/quicktime", "video/x-matroska",
		"audio/wav", "audio/mpeg", "audio/flac", "audio/ogg",
	} {
		ext := ExtForMIME(mime)
		require.NotEmpty(t, ext, mime)
		require.Equal(t, mime, InferContentType("x."+ext), "round-trip %s", mime)
	}
}

// heic/avif/m4a/aac 只在 ExtForMIME 里有，InferContentType 认不出会回落
// application/octet-stream。这不是 bug（Persist 会用调用方传入的 ContentType），
// 但把它钉成断言，免得将来有人以为 round-trip 是全集成立的。
func TestExtForMIME_KnownAsymmetry(t *testing.T) {
	for _, ext := range []string{"heic", "avif", "m4a", "aac"} {
		require.Equal(t, "application/octet-stream", InferContentType("x."+ext), ext)
	}
}

func TestParseDataURL_LargeRealisticImage(t *testing.T) {
	// 复现故障场景的量级：790 KB 原图 → base64 后 ~1.05 MB。
	raw := dataURL("image/jpeg", make([]byte, 790*1024))
	require.Greater(t, len(raw), 1048576, "该输入正是顶穿上游 1 MiB 限制的那一类")
	got, err := ParseDataURL(raw, 200*1024*1024)
	require.NoError(t, err)
	require.Len(t, got.Data, 790*1024)
	require.True(t, strings.HasPrefix(got.MIME, "image/"))
}
