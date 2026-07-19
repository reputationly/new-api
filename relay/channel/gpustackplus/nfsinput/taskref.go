// taskref.go — task:<task_id> 产物引用解析(画布编排串联,见 docs/canvas-orchestration-design.md §3.8)。
//
// 下游任务的输入直接引用同用户某个已成功任务的产物,免去"前端拉 Blob → base64 →
// 后端解码"的三次搬运。解析优先级:
//  1. ResultURL 为 obs://<key>:先试 <NFSRoot>/<key> 同盘直读(落盘 key 与 NFS 相对
//     路径同构,见 service/media_ingest.go PersistTaskResultToOBS,零网络);
//  2. NFS 读不到(janitor TTL 已清/未挂载)→ 退化为 OBS 实时签名 URL 下载(授信自家
//     OBS host,与 controller/video_proxy.go 同精神);
//  3. 明文 http(s) ResultURL(第三方渠道/旧数据)→ 走通用 downloadURL(SSRF 校验,
//     同样授信自家 OBS host,覆盖旧数据明文签名 OBS URL);
//  4. data: base64 ResultURL(第三方渠道产物未落 OBS)→ 直接解码;
//  5. NFS 绝对路径塞在 ResultURL 的历史兜底 → 校验后直读。
//
// 归属校验:任务必须属于当前请求用户,防跨用户引用他人产物;状态校验:必须 SUCCESS 终态。
//
// 包级 ResolveTaskRefBytes 同时服务两条链路:gpustackplus 物化层(Materializer.AddString,
// NFS 直读收益最大)与 relay 通用层(非 gpustackplus 渠道把 task: 展开成 base64 交给
// 第三方适配器,见 relay/relay_task.go)。

package nfsinput

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/mediastore"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// TaskRefScheme 任务产物引用前缀:task:<task_id>。
const TaskRefScheme = "task:"

// IsTaskRef 判断一个输入字符串是否为任务产物引用。
func IsTaskRef(raw string) bool {
	return strings.HasPrefix(strings.TrimSpace(raw), TaskRefScheme)
}

// resolveTaskRef Materializer 入口:userID 来自物化上下文(字符串形态)。
func (m *Materializer) resolveTaskRef(ctx context.Context, raw string) ([]byte, string, error) {
	userID, err := strconv.Atoi(m.userID)
	if err != nil || userID <= 0 {
		return nil, "", fmt.Errorf("任务引用缺少有效用户上下文")
	}
	return ResolveTaskRefBytes(ctx, userID, raw, m.maxBytes)
}

// ResolveTaskRefBytes 把 task:<task_id> 解析为产物字节 + 真实扩展名。
// maxBytes 为 per-model 上限(0=仅受全局 MaxObjectSizeMB 约束)。扩展名从 OBS key /
// URL 路径提取(引擎 save_result_path 的扩展名经 KeyFromNFSPath 原样保留,如 ACE-Step
// 音乐产物 .mp3):不能丢——下游引擎按扩展名识别容器(与 extForData 同精神);
// 无法识别时返回 "",由调用方回退默认扩展名。
func ResolveTaskRefBytes(ctx context.Context, userID int, raw string, maxBytes int64) ([]byte, string, error) {
	taskID := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), TaskRefScheme))
	if taskID == "" {
		return nil, "", fmt.Errorf("任务引用为空,期望形如 task:<task_id>")
	}
	task, exists, err := model.GetByTaskId(userID, taskID)
	if err != nil {
		return nil, "", fmt.Errorf("查询被引用任务 %s 失败: %w", taskID, err)
	}
	if !exists || task == nil {
		return nil, "", fmt.Errorf("被引用任务 %s 不存在或不属于当前用户", taskID)
	}
	if task.Status != model.TaskStatusSuccess {
		return nil, "", fmt.Errorf("被引用任务 %s 未成功完成(当前状态 %s),无法引用其产物", taskID, task.Status)
	}
	resultURL := strings.TrimSpace(task.GetResultURL())
	if resultURL == "" {
		return nil, "", fmt.Errorf("被引用任务 %s 无产物记录", taskID)
	}

	// 自家 OBS host 授信:与 controller/video_proxy.go 同精神——旧数据 ResultURL 可能是
	// 明文签名 OBS URL(非 obs:// 占位符),隔离环境下解析到私网会被 SSRF 拒;
	// 仅放松私网这一条,scheme/端口仍强制。OBS 退化下载与明文 URL 两条路径共用。
	var trusted []string
	if h := mediastore.OwnOBSHost(); h != "" {
		trusted = append(trusted, h)
	}
	root := system_setting.GetMediaStorageSettings().NFSRoot()

	if mediastore.IsOBSRef(resultURL) {
		key := mediastore.KeyFromRef(resultURL)
		ext := refMediaExt(key)
		data, nfsErr := readAbsNFSPathLimited(root, filepath.Join(root, filepath.FromSlash(key)), maxBytes)
		if nfsErr == nil {
			return data, ext, nil
		}
		// NFS 读不到(已清理/未挂载/越权拒绝)→ OBS 签名 URL 退化。
		// 超限错误同样走退化没有意义(OBS 是同一份字节),但 downloadURL 会以
		// LimitReader 快速再拒,无需在此区分错误类别。
		signed, sErr := mediastore.Sign(ctx, key)
		if sErr != nil {
			return nil, "", fmt.Errorf("被引用任务 %s 产物本地不可读(%v)且 OBS 签名失败: %v", taskID, nfsErr, sErr)
		}
		data, dErr := downloadURL(ctx, signed, maxBytes, trusted...)
		return data, ext, dErr
	}
	// 自家 VideoProxy 代理 URL:上游适配器未给直连 URL 时,ResultURL 被存成
	// {ServerAddress}/v1/videos/{id}/content(relay_task.go:493-495)。该端点要
	// TokenOrUserAuth,服务端匿名 GET 会 401——改为在进程内按渠道解析真实上游产物 URL
	// (带渠道 key),绕过 self-call 鉴权。
	if resultURL == proxyTaskContentURL(taskID) {
		realURL, header, proxy, rErr := resolveProxiedTaskContent(task)
		if rErr != nil {
			return nil, "", rErr
		}
		data, dErr := downloadURLWithHeader(ctx, realURL, maxBytes, header, proxy, trusted...)
		return data, refMediaExt(realURL), dErr
	}
	if strings.HasPrefix(resultURL, "http://") || strings.HasPrefix(resultURL, "https://") {
		data, dErr := downloadURL(ctx, resultURL, maxBytes, trusted...)
		return data, refMediaExt(resultURL), dErr
	}
	// 第三方渠道 base64 产物未落 OBS:ResultURL 为 data: URI,直接解码。
	if strings.HasPrefix(resultURL, "data:") {
		ext := extForData(resultURL)
		b64 := resultURL
		if i := strings.Index(resultURL, ","); i >= 0 {
			b64 = resultURL[i+1:]
		}
		data, decErr := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
		if decErr != nil {
			return nil, "", fmt.Errorf("被引用任务 %s 的 base64 产物解码失败: %v", taskID, decErr)
		}
		// 与 NFS / downloadURL 同口径:全局 MaxObjectSizeMB 与 per-model maxBytes 取较小正值。
		// data URI 已整块在内存(ResultURL 本身来自任务表,大小可控),这里是超限即拒的护栏。
		if limit := effectiveSizeLimit(maxBytes); limit > 0 && int64(len(data)) > limit {
			return nil, "", fmt.Errorf("被引用任务 %s 产物超过大小上限 %d MB", taskID, limit/1024/1024)
		}
		return data, ext, nil
	}
	// 历史兜底:NFS 绝对路径直接塞在 ResultURL(见 media_ingest.go §2 启发式)。
	if strings.HasPrefix(resultURL, "/") {
		if data, err := readAbsNFSPathLimited(root, resultURL, maxBytes); err == nil {
			return data, refMediaExt(resultURL), nil
		}
	}
	return nil, "", fmt.Errorf("被引用任务 %s 的产物形态无法识别", taskID)
}

// proxyTaskContentURL 复刻 taskcommon.BuildProxyURL 的形态(不 import taskcommon 避免包环),
// 用于识别 ResultURL 是否为自家 VideoProxy 代理入口。
func proxyTaskContentURL(taskID string) string {
	return fmt.Sprintf("%s/v1/videos/%s/content", system_setting.ServerAddress, taskID)
}

// resolveProxiedTaskContent 把"产物存成代理 URL"的任务解析为可服务端直取的真实上游
// content URL + 所需请求头(与 controller/video_proxy.go 的渠道分派同精神)。
// 仅覆盖 OpenAI/Sora(拼上游 content 端点 + 渠道 key);Gemini/Vertex 等需渠道 SDK 实时
// 解析的形态返回明确错误(建议对上游启用媒体存储 OBS 落盘,即可走 obs:// 快路径)。
func resolveProxiedTaskContent(task *model.Task) (string, http.Header, string, error) {
	channel, err := model.CacheGetChannel(task.ChannelId)
	if err != nil || channel == nil {
		return "", nil, "", fmt.Errorf("被引用任务 %s 的渠道信息不可用: %v", task.TaskID, err)
	}
	switch channel.Type {
	case constant.ChannelTypeOpenAI, constant.ChannelTypeSora:
		baseURL := channel.GetBaseURL()
		if baseURL == "" {
			baseURL = "https://api.openai.com"
		}
		realURL := fmt.Sprintf("%s/v1/videos/%s/content", strings.TrimRight(baseURL, "/"), task.GetUpstreamTaskID())
		header := http.Header{}
		header.Set("Authorization", "Bearer "+channel.Key)
		// 渠道配置的代理(如 OpenAI/Sora 出海),与 VideoProxy 取产物时一致
		return realURL, header, channel.GetSetting().Proxy, nil
	default:
		return "", nil, "", fmt.Errorf("被引用任务 %s 的产物存储于渠道类型 %d 的代理端点,需该渠道实时鉴权解析,暂不支持作为跨渠道(非 gpustackplus)引用输入;建议对该上游启用媒体存储(OBS)落盘后再引用", task.TaskID, channel.Type)
	}
}

// allowedRefExts 任务产物扩展名白名单(与 extForData 的输出集合对齐,白名单输出,
// 不把任意后缀带进 NFS 输入文件名)。
var allowedRefExts = map[string]bool{
	".wav": true, ".mp3": true, ".m4a": true, ".ogg": true, ".flac": true,
	".mp4": true, ".mov": true, ".webm": true,
	".png": true, ".jpg": true, ".jpeg": true, ".webp": true,
}

// refMediaExt 从 key/URL/路径提取白名单内的媒体扩展名;query/fragment 先剥掉。
func refMediaExt(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	ext := strings.ToLower(filepath.Ext(p))
	if allowedRefExts[ext] {
		return ext
	}
	return ""
}

// readAbsNFSPathLimited 校验绝对路径落在 root 之下(含 symlink 复查)后读出字节。
// 大小上限与 downloadURL 同口径:全局 MaxObjectSizeMB 与 per-model maxBytes 取较小
// 正值(0=不限)——否则 per-model 未设置时 NFS 快路径会绕开全局限额,与 OBS 退化路径
// 行为不一致,且超大产物被整块读进内存。用 Stat 预检,超限不发生实际读取。
func readAbsNFSPathLimited(root, abs string, maxBytes int64) ([]byte, error) {
	resolved, err := mediastore.ValidateNFSPath(root, abs)
	if err != nil {
		return nil, err
	}
	if limit := effectiveSizeLimit(maxBytes); limit > 0 {
		if fi, statErr := os.Stat(resolved); statErr == nil && fi.Size() > limit {
			return nil, fmt.Errorf("被引用产物超过大小上限 %d MB", limit/1024/1024)
		}
	}
	return os.ReadFile(resolved)
}

// effectiveSizeLimit 有效大小上限(字节):全局 MaxObjectSizeMB 与 per-model maxBytes
// 取较小正值(均为 0 = 不限)。task: 引用的 NFS / data URI / downloadURL 三条路径统一口径。
func effectiveSizeLimit(maxBytes int64) int64 {
	limit := int64(system_setting.GetMediaStorageSettings().MaxObjectSizeMB) * 1024 * 1024
	if maxBytes > 0 && (limit <= 0 || maxBytes < limit) {
		limit = maxBytes
	}
	return limit
}
