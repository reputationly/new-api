package service

// 参考视频内联：让增强模型不必自己出网拉视频。
//
// # 为什么
//
// 参考族(r2va)的增强要把参考视频发给增强模型,我们一直直接发客户给的 URL。
// 而引擎(vLLM)自己拉这个 URL 会偶发挂死 —— 线上日志(qwen3.8-flash-fp8 副本,
// 2026-11-20):
//
//	WARNING [connections.py:220] HTTP fetch failed for https://cdn.hailuoai.com/…mp4
//	        (attempt 1/3, timeout=30.000s):  -- retrying in 1.000s with timeout=120.000s
//
// 重试链是 30s → 120s → 480s,于是请求拖到网关超时(实测 465 秒 504)。而**同一个
// 容器里**用 curl、aiohttp、乃至 vLLM 自己的 global_http_connection 拉同一个 URL
// 都是 0.1–0.2 秒 —— 网络没问题,是引擎侧连接偶发挂死。
//
// 所以改由我们自己把视频取下来、以 data URI 内联进请求:引擎拿到的是字节,
// 不需要出网。实测同一段 4.4MB 视频内联后 5.2 秒就正确描述了画面。
//
// # 边界
//
//   - 上限沿用 maxEnhanceVideoDataURI(8MB,按内联后的 data URI 长度算):超了就
//     **不发内联**,退回原 URL —— 少看一段素材也比起一次注定超时的调用好,
//     而且这条路跑在客户的关键路径上。原始字节上限见 maxInlineVideoBytes。
//   - 下载有自己的超时(enhanceVideoInlineTimeout),不受增强总预算牵制也不会
//     把它整段吃掉:增强总预算是几十秒到几分钟,单为一次下载耗掉一半不划算。
//   - 失败一律退回原 URL:内联是优化,不是前提。
//   - SSRF 校验与 service/download.go 同源(common.ValidateURLWithFetchSetting),
//     重定向由 GetHttpClient 的 CheckRedirect 兜住。

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/mediastore"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// enhanceVideoInlineTimeout 单段视频的下载预算。
//
// 取 20 秒:实测这类几 MB 的片段在 0.2–2 秒量级,20 秒足够吸收慢一点的对象存储,
// 又不会把增强的几十秒预算吃掉一半。
const enhanceVideoInlineTimeout = 20 * time.Second

// inlineDataURIPrefixReserve 给 "data:video/…;base64," 这段前缀留的余量。
const inlineDataURIPrefixReserve = 64

// maxInlineVideoBytes 能内联的原始字节上限。
//
// 判据是**内联之后的字符串长度**:enhanceVideoAllowed 拿 data URI 的长度和
// maxEnhanceVideoDataURI 比,而 base64 放大约 4/3,还要再加前缀。所以原始字节
// 要先扣掉前缀余量、再按 3/4 封顶 —— 否则一段正好 6MB 的视频内联成 8.0008MB,
// 会被发送那一关再丢掉,白下一遍(这条是实测撞出来的)。
const maxInlineVideoBytes = (maxEnhanceVideoDataURI - inlineDataURIPrefixReserve) / 4 * 3

// inlineEnhanceVideos 把远程参考视频换成 data URI。返回新切片,不改入参。
//
// 任何一段取不到都**原样保留它的 URL**,因为缺陷只影响"模型看不看得见这段素材",
// 不该让整次增强换个形态。
func inlineEnhanceVideos(ctx context.Context, urls []string) []string {
	if len(urls) == 0 {
		return urls
	}
	out := make([]string, len(urls))
	for i, u := range urls {
		out[i] = u
		inlined, reason := inlineOneEnhanceVideo(ctx, u)
		if reason != "" {
			common.SysLog("aggregate enhance: 参考视频未内联(" + reason + "),按原 URL 发送:" + common.MaskSensitiveInfo(u))
		}
		if inlined != "" {
			out[i] = inlined
		}
	}
	return out
}

// inlineOneEnhanceVideo 取一段视频并编成 data URI。
//
// 返回 (data URI, 未内联原因)。原因非空表示"退回原 URL",数据 URI 为空。
func inlineOneEnhanceVideo(ctx context.Context, raw string) (string, string) {
	u := strings.TrimSpace(raw)
	if u == "" {
		return "", ""
	}
	// 已经是内联的就别再取一遍(客户直接传 base64 的场景)。
	if strings.HasPrefix(u, "data:") {
		return "", ""
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		// mm_file:// 之类只能由上游自己解析,我们取不到。
		return "", ""
	}
	client := GetHttpClient()
	if client == nil {
		return "", "网关 HTTP 客户端未初始化"
	}

	fetchSetting := system_setting.GetFetchSetting()
	if err := common.ValidateURLWithFetchSetting(u, fetchSetting.EnableSSRFProtection,
		fetchSetting.AllowPrivateIp, fetchSetting.DomainFilterMode, fetchSetting.IpFilterMode,
		fetchSetting.DomainList, fetchSetting.IpList, fetchSetting.AllowedPorts,
		fetchSetting.ApplyIPFilterForDomain, mediastore.OwnOBSHost()); err != nil {
		return "", "URL 未通过校验: " + err.Error()
	}

	dctx, cancel := context.WithTimeout(ctx, enhanceVideoInlineTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(dctx, http.MethodGet, u, nil)
	if err != nil {
		return "", "构造下载请求失败: " + err.Error()
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "下载失败: " + err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Sprintf("下载返回 %d", resp.StatusCode)
	}
	// 多读一个字节就能判断是否超限,不必把整段都收进内存。
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxInlineVideoBytes+1))
	if err != nil {
		return "", "读取响应失败: " + err.Error()
	}
	if len(body) > maxInlineVideoBytes {
		return "", fmt.Sprintf("超过 %d 字节上限", maxInlineVideoBytes)
	}
	if len(body) == 0 {
		return "", "响应为空"
	}
	return videoDataURI(body, resp.Header.Get("Content-Type"), u), ""
}

// videoDataURI 编成 data:video/…;base64,…。
//
// 声明体类型是因为上游按它挑解码器:头里给的优先,其次按扩展名猜,最后退回 mp4
// (H3 只吃 mp4/mov,退回 mp4 是这三个里最可能对的一个)。
func videoDataURI(body []byte, contentType, url string) string {
	ct := strings.TrimSpace(strings.Split(contentType, ";")[0])
	if ct == "" || !strings.HasPrefix(ct, "video/") {
		if byExt := mime.TypeByExtension(strings.ToLower(path.Ext(url))); strings.HasPrefix(byExt, "video/") {
			ct = byExt
		} else {
			ct = "video/mp4"
		}
	}
	var buf bytes.Buffer
	buf.Grow(base64.StdEncoding.EncodedLen(len(body)) + len(ct) + 16)
	buf.WriteString("data:")
	buf.WriteString(ct)
	buf.WriteString(";base64,")
	buf.WriteString(base64.StdEncoding.EncodeToString(body))
	return buf.String()
}
