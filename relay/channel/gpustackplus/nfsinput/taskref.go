// taskref.go — task:<task_id> 产物引用解析(画布编排串联,见 docs/canvas-orchestration-design.md §3.8)。
//
// 下游任务的输入直接引用同用户某个已成功任务的产物,免去"前端拉 Blob → base64 →
// 后端解码"的三次搬运。解析优先级:
//  1. ResultURL 为 obs://<key>:先试 <NFSRoot>/<key> 同盘直读(落盘 key 与 NFS 相对
//     路径同构,见 service/media_ingest.go PersistTaskResultToOBS,零网络);
//  2. NFS 读不到(janitor TTL 已清/未挂载)→ 退化为 OBS 实时签名 URL 下载(授信自家
//     OBS host,与 controller/video_proxy.go 同精神);
//  3. 明文 http(s) ResultURL(第三方渠道/旧数据)→ 走通用 downloadURL(全量 SSRF 校验)。
//
// 归属校验:任务必须属于当前请求用户(Materializer.userID),防跨用户引用他人产物;
// 状态校验:必须 SUCCESS 终态。

package nfsinput

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

// resolveTaskRef 把 task:<task_id> 解析为产物字节 + 真实扩展名,供 AddString 进入统一
// addBytesExt 流程。扩展名从 OBS key / URL 路径提取(引擎 save_result_path 的扩展名经
// KeyFromNFSPath 原样保留,如 ACE-Step 音乐产物 .mp3):不能丢——下游引擎按扩展名识别
// 容器(与 extForData 同精神);无法识别时返回 "",由调用方回退字段默认扩展名。
func (m *Materializer) resolveTaskRef(ctx context.Context, raw string) ([]byte, string, error) {
	taskID := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), TaskRefScheme))
	if taskID == "" {
		return nil, "", fmt.Errorf("任务引用为空,期望形如 task:<task_id>")
	}
	userID, err := strconv.Atoi(m.userID)
	if err != nil || userID <= 0 {
		return nil, "", fmt.Errorf("任务引用缺少有效用户上下文")
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

	if mediastore.IsOBSRef(resultURL) {
		key := mediastore.KeyFromRef(resultURL)
		ext := refMediaExt(key)
		data, nfsErr := m.readTaskResultFromNFS(key)
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
		data, dErr := downloadURL(ctx, signed, m.maxBytes, trusted...)
		return data, ext, dErr
	}
	if strings.HasPrefix(resultURL, "http://") || strings.HasPrefix(resultURL, "https://") {
		data, dErr := downloadURL(ctx, resultURL, m.maxBytes, trusted...)
		return data, refMediaExt(resultURL), dErr
	}
	// 历史兜底:NFS 绝对路径直接塞在 ResultURL(见 media_ingest.go §2 启发式)。
	if strings.HasPrefix(resultURL, "/") {
		if data, err := m.readAbsNFSPath(resultURL); err == nil {
			return data, refMediaExt(resultURL), nil
		}
	}
	return nil, "", fmt.Errorf("被引用任务 %s 的产物形态无法识别", taskID)
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

// readTaskResultFromNFS 按 obs key 还原 NFS 绝对路径并直读(带越权/大小校验)。
func (m *Materializer) readTaskResultFromNFS(key string) ([]byte, error) {
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("产物 key 为空")
	}
	return m.readAbsNFSPath(filepath.Join(m.root, filepath.FromSlash(key)))
}

// readAbsNFSPath 校验绝对路径落在 NFSRoot 之下(含 symlink 复查)后读出字节。
// 大小上限与 downloadURL 同口径:全局 MaxObjectSizeMB 与 per-model m.maxBytes 取较小
// 正值(0=不限)——否则 per-model 未设置时 NFS 快路径会绕开全局限额,与 OBS 退化路径
// 行为不一致,且超大产物被整块读进内存。用 Stat 预检,超限不发生实际读取。
func (m *Materializer) readAbsNFSPath(abs string) ([]byte, error) {
	resolved, err := mediastore.ValidateNFSPath(m.root, abs)
	if err != nil {
		return nil, err
	}
	limit := int64(system_setting.GetMediaStorageSettings().MaxObjectSizeMB) * 1024 * 1024
	if m.maxBytes > 0 && (limit <= 0 || m.maxBytes < limit) {
		limit = m.maxBytes
	}
	if limit > 0 {
		if fi, statErr := os.Stat(resolved); statErr == nil && fi.Size() > limit {
			return nil, fmt.Errorf("被引用产物超过大小上限 %d MB", limit/1024/1024)
		}
	}
	return os.ReadFile(resolved)
}
