package arkasset

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// 火山 OpenAPI 的 HMAC-SHA256 签名，参数固定为方舟素材库的 service=ark、region=cn-beijing。
// 规则逐字照方舟官方文档与界云「素材库认证与签名」的 Go 示例：签四个头
// content-type;host;x-content-sha256;x-date，query 按 key 排序、空格编码为 %20。
//
// 不复用 relay/channel/jimeng/sign.go：那份绑死了 gin.Context、"ak|sk" 渠道 key 格式
// 与即梦的 service=cv / region=cn-north-1。
const (
	signRegion  = "cn-beijing"
	signService = "ark"
)

const signedHeaders = "content-type;host;x-content-sha256;x-date"

// signRequest 给 req 写上 X-Date / X-Content-Sha256 / Authorization。body 必须是随后
// 真正发送的那份字节——签名后再改 JSON 就对不上了。
func signRequest(req *http.Request, body []byte, accessKey, secretKey string, now time.Time) {
	xDate := now.UTC().Format("20060102T150405Z")
	shortDate := xDate[:8]
	payloadHash := sha256Hex(body)
	contentType := req.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
		req.Header.Set("Content-Type", contentType)
	}

	path := req.URL.EscapedPath()
	if path == "" {
		path = "/"
	}
	canonicalQuery := canonicalQueryString(req.URL.Query())
	canonicalHeaders := "content-type:" + contentType + "\n" +
		"host:" + req.URL.Host + "\n" +
		"x-content-sha256:" + payloadHash + "\n" +
		"x-date:" + xDate + "\n"
	canonicalRequest := strings.Join([]string{
		req.Method, path, canonicalQuery, canonicalHeaders, signedHeaders, payloadHash,
	}, "\n")

	credentialScope := fmt.Sprintf("%s/%s/%s/request", shortDate, signRegion, signService)
	stringToSign := strings.Join([]string{
		"HMAC-SHA256", xDate, credentialScope, sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	key := hmacSHA256([]byte(secretKey), shortDate)
	key = hmacSHA256(key, signRegion)
	key = hmacSHA256(key, signService)
	key = hmacSHA256(key, "request")
	signature := hex.EncodeToString(hmacSHA256(key, stringToSign))

	req.Header.Set("X-Date", xDate)
	req.Header.Set("X-Content-Sha256", payloadHash)
	req.Header.Set("Authorization", fmt.Sprintf("HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, credentialScope, signedHeaders, signature))
}

func canonicalQueryString(q url.Values) string {
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		values := append([]string(nil), q[k]...)
		sort.Strings(values)
		for _, v := range values {
			parts = append(parts, queryEscape(k)+"="+queryEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

func queryEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(value))
	return mac.Sum(nil)
}
