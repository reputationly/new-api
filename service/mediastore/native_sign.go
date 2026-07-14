package mediastore

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 原生 OBS URL 签名（HCSO 接口参考 §3.2.3 "URL 中携带签名"）。
//
// 弃用 aws-sdk SigV4 presign 改用原生签名的原因：SigV4 URL 含签名时刻 X-Amz-Date，
// 每次签出的 URL 都不同，浏览器 HTTP 缓存永远 miss，重复预览重复全量下载；
// 且 HCSO 的 OBS 文档只承诺原生签名协议，SigV4 属未文档化兼容。原生签名只依赖
// 绝对失效时间 Expires——把 Expires 对齐到时间桶（桶终点 + TTL），同桶内签出的
// URL 逐字节相同，缓存自然命中；有效期恒 ≥ TTL。协议与 response-cache-control
// 支持均已在 HCSO 实机验证（2026-07 探针：PUT/GET/Range/DELETE 全通过）。

// signBucketWindow Expires 对齐的时间桶。桶内 URL 恒定（缓存命中窗口），跨桶轮换。
// 浏览器缓存的实际命中范围就是这个窗口（URL 一轮换旧缓存即失效），故取 24h 使
// "当天内重看"命中，并与 max-age 对齐；再放大收益骤减（磁盘缓存会淘汰大媒体）而
// URL 共享窗口继续变长。Truncate 按 Unix 纪元对齐 → 桶界为 UTC 零点，跨实例一致。
const signBucketWindow = 24 * time.Hour

// signedResponseCacheControl 通过 response-cache-control 子资源让 OBS 响应携带的
// Cache-Control。不设置时 OBS 响应无 Cache-Control，浏览器只做启发式缓存（约
// Last-Modified 年龄的 10%，刚生成的媒体几乎不缓存）。private：内容鉴权访问，
// 不允许共享缓存存储；immutable：对象不可变，免 reload 再验证。
const signedResponseCacheControl = "private, max-age=86400, immutable"

// maxSignTTL TTL 上限。OBS 拒绝超过当前时间 + 20 年的 Expires，取 10 年留足冗余。
const maxSignTTL = 10 * 365 * 24 * time.Hour

// nativeSignedGetURL 为 GET 对象签发原生签名 URL（virtual-hosted 风格）。
// now 仅用于计算桶对齐的 Expires，由调用方传入以便测试。
func nativeSignedGetURL(cfg obsConfig, key string, ttl time.Duration, o SignOptions, now time.Time) (string, error) {
	scheme, host, err := endpointSchemeHost(cfg.Endpoint)
	if err != nil {
		return "", err
	}
	if cfg.Bucket == "" {
		return "", fmt.Errorf("mediastore: bucket required for signing")
	}
	// 空 key 会签出 /bucket/ 的合法 URL——若 AK 有列桶权限即泄露桶列表，必须拒绝。
	if key == "" {
		return "", fmt.Errorf("mediastore: object key required for signing")
	}
	if ttl <= 0 {
		return "", fmt.Errorf("mediastore: sign ttl must be positive")
	}
	// OBS 要求 Expires < 当前时间 + 20 年；上限校验同时排除 signBucketWindow+ttl 的
	// duration 加法溢出（校验在加法之前）。
	if ttl > maxSignTTL {
		return "", fmt.Errorf("mediastore: sign ttl exceeds %v", maxSignTTL)
	}
	// 非整秒 TTL 按秒向上取整：Expires 是整秒时间戳，否则亚秒部分被截断，
	// 破坏"剩余有效期 ≥ TTL"的承诺。
	if r := ttl % time.Second; r != 0 {
		ttl += time.Second - r
	}

	// Expires = 当前桶终点 + TTL：桶内恒定（URL 不变），剩余有效期恒在 (TTL, TTL+桶]
	//（精确桶界时刻取到上界）。TTL 是下限而非上限——把 TTL 当严格失效上限的场景
	// 需自行减去一个桶。
	expires := now.Truncate(signBucketWindow).Add(signBucketWindow + ttl).Unix()

	subres := map[string]string{
		"response-cache-control": signedResponseCacheControl,
	}
	if o.DownloadName != "" {
		subres["response-content-disposition"] = fmt.Sprintf("attachment; filename=%q", o.DownloadName)
	}
	names := make([]string, 0, len(subres))
	for k := range subres {
		names = append(names, k)
	}
	sort.Strings(names)

	encodedKey := encodeObjectKey(key)
	stringToSign := buildGetStringToSign(cfg.Bucket, encodedKey, expires, names, subres)
	signature := hmacSHA1Base64(cfg.SecretAccessKey, stringToSign)

	var q strings.Builder
	for _, k := range names {
		q.WriteString(k)
		q.WriteByte('=')
		q.WriteString(encodeURIComponent(subres[k]))
		q.WriteByte('&')
	}
	q.WriteString("AccessKeyId=")
	q.WriteString(encodeURIComponent(cfg.AccessKeyID))
	q.WriteString("&Expires=")
	q.WriteString(strconv.FormatInt(expires, 10))
	q.WriteString("&Signature=")
	q.WriteString(encodeURIComponent(signature))

	return fmt.Sprintf("%s://%s.%s/%s?%s", scheme, cfg.Bucket, host, encodedKey, q.String()), nil
}

// buildGetStringToSign 构造待签串：GET\n\n\nExpires\n/bucket/key[?子资源(字典序，原始值)]。
// 子资源值用原始值（未 URL 编码）——与文档 Java 示例一致，实机验证通过。
func buildGetStringToSign(bucket, encodedKey string, expires int64, sortedNames []string, subres map[string]string) string {
	var sb strings.Builder
	sb.WriteString("GET\n\n\n")
	sb.WriteString(strconv.FormatInt(expires, 10))
	sb.WriteString("\n/")
	sb.WriteString(bucket)
	sb.WriteByte('/')
	sb.WriteString(encodedKey)
	for i, k := range sortedNames {
		if i == 0 {
			sb.WriteByte('?')
		} else {
			sb.WriteByte('&')
		}
		sb.WriteString(k)
		if v := subres[k]; v != "" {
			sb.WriteByte('=')
			sb.WriteString(v)
		}
	}
	return sb.String()
}

func hmacSHA1Base64(sk, stringToSign string) string {
	h := hmac.New(sha1.New, []byte(sk))
	h.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// encodeURIComponent 按文档 Java 示例的编码规则：标准 URL 编码后
// "+"→"%20"、"*"→"%2A"、"%7E"→"~"。
func encodeURIComponent(s string) string {
	e := url.QueryEscape(s)
	e = strings.ReplaceAll(e, "+", "%20")
	e = strings.ReplaceAll(e, "*", "%2A")
	e = strings.ReplaceAll(e, "%7E", "~")
	return e
}

// encodeObjectKey 对象名按 "/" 分段编码，保留路径层级。
func encodeObjectKey(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = encodeURIComponent(p)
	}
	return strings.Join(parts, "/")
}

// endpointSchemeHost 解析 endpoint 的 scheme 与 host（无 scheme 时默认 https）。
func endpointSchemeHost(endpoint string) (string, string, error) {
	raw := strings.TrimSpace(endpoint)
	if raw == "" {
		return "", "", fmt.Errorf("mediastore: endpoint required for signing")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", "", fmt.Errorf("mediastore: invalid endpoint %q", endpoint)
	}
	return u.Scheme, u.Host, nil
}
