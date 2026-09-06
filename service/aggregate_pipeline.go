package service

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// 聚合模型的视频流水线:生成段完成后自动接一段超分,客户全程只看到一个任务。
//
// ── 挂钩点 ──────────────────────────────────────────────────────────
//
// 在轮询发现生成段 completed 的那一刻拦截,**早于产物落 OBS、早于结算**:
// 此时任务还没进终态,把它改回 InProgress 并换上超分段的上游 id,轮询下一轮就自然
// 跟进第二段。客户侧从头到尾只有一个 task id,状态一直是 in_progress。
//
// 为什么必须早于落盘:落了 OBS 就等于对外交付了中间产物(未超分的那一版),
// 而客户要的是最终的 2K。中间产物只该作为超分段的输入,不该出现在结果里。
//
// ── 为什么是 self-call ─────────────────────────────────────────────
//
// 与增强段同一个理由:走一遍 /v1/videos 就自动拿到渠道路由、预扣费、日志、退款,
// 而手工构造上游提交请求意味着把这些各抄一份 —— 抄出来的那份会与主链路漂移,
// 且不报错。轮询是后台任务、没有客户的请求上下文,所以客户令牌在提交生成段时
// 就存进了任务的 PrivateData(见 model.TaskAggregateInfo.CallerKey)。
//
// ── 失败语义 ───────────────────────────────────────────────────────
//
// 超分段提交失败 → 整单**按已有逻辑正常结束在生成段**:产物照常落盘交付,
// 客户拿到未超分的那一版。这是深思后的选择,不是偷懒:
//   - 生成段已经真实消耗了 GPU 并已计费,判整单失败要退这笔钱,我们白烧一次算力;
//   - 客户按分段计费付的是"生成 + 超分"两笔,超分没跑成就只扣生成那笔,他没吃亏;
//   - 最重要的是:交付一个分辨率低一些的成品,比交付一个错误强得多。
// 这与你定的"各段独立退款"一致 —— 没跑的段不收钱,跑了的段照收。

// lookupTaskByPublicID 回查自调用产生的那条任务。做成变量供测试构造成功路径,
// 免去为一条状态机测试去建表。
var lookupTaskByPublicID = model.GetByOnlyTaskId

// upscaleSubmitTimeout 提交超分段的超时。后台轮询里的一次自调用,不能拖住整个轮询批次。
const upscaleSubmitTimeout = 30 * time.Second

// videosEndpoint 视频任务提交地址。做成变量供测试指向本地假服务端。
var videosEndpoint = func() string {
	base := strings.TrimRight(strings.TrimSpace(system_setting.ServerAddress), "/")
	if base == "" {
		return ""
	}
	return base + "/v1/videos"
}

// TryAdvanceAggregatePipeline 在生成段完成时尝试推进到超分段。
//
// 返回 true 表示已成功提交超分段、任务应保持 InProgress(调用方须跳过落盘与结算);
// 返回 false 表示这条任务没有后续段、或超分提交失败 —— 两种情况都按原有逻辑正常收尾。
//
// nfsPath 为生成段产物在共享 NFS 上的路径,作为超分段的输入。
func TryAdvanceAggregatePipeline(ctx context.Context, task *model.Task, nfsPath string) bool {
	agg := task.PrivateData.Aggregate
	if !agg.HasUpscale() || agg.Stage != 1 {
		return false
	}
	// 没有产物路径就无从超分。这不该发生(生成段成功必有产物),但真发生时按
	// "没有后续段"处理 —— 交付生成段的成品,好过把一个已完成的任务判失败。
	if strings.TrimSpace(nfsPath) == "" {
		common.SysLog(fmt.Sprintf("aggregate pipeline: task %s 生成段无 nfs_path,跳过超分段", task.TaskID))
		return false
	}
	if strings.TrimSpace(agg.CallerKey) == "" {
		common.SysLog(fmt.Sprintf("aggregate pipeline: task %s 缺少调用者身份,跳过超分段", task.TaskID))
		return false
	}

	reqCtx, cancel := context.WithTimeout(ctx, upscaleSubmitTimeout)
	defer cancel()
	upstreamID, channelID, err := submitUpscaleTask(reqCtx, agg, nfsPath)
	if err != nil {
		// 提交失败不改变任务状态:调用方继续走落盘与结算,客户拿到未超分的成品。
		common.SysError(fmt.Sprintf("aggregate pipeline: task %s 超分段提交失败,按生成段成品交付: %v",
			task.TaskID, err))
		return false
	}

	agg.Stage1TaskID = task.PrivateData.UpstreamTaskID
	agg.Stage = 2
	task.PrivateData.UpstreamTaskID = upstreamID
	if channelID > 0 {
		task.ChannelId = channelID
	}
	// 令牌用完即擦:第二段已经提交,它不再需要客户凭证,没有理由继续留在库里。
	agg.CallerKey = ""
	common.SysLog(fmt.Sprintf("aggregate pipeline: task %s 进入超分段(upstream=%s)", task.TaskID, upstreamID))
	return true
}

// submitUpscaleTask 以客户身份提交一次超分任务,返回上游任务 id 与承接渠道 id。
//
// 输入用 metadata.video 传生成段产物的 NFS 绝对路径:nfsinput 的物化层原生支持这种
// 形态(见 relay/channel/gpustackplus/nfsinput/taskref.go 的绝对路径分支),
// 因此**不需要把产物读出来再传一遍** —— 那对一个几十 MB 的视频是纯浪费。
func submitUpscaleTask(ctx context.Context, agg *model.TaskAggregateInfo, nfsPath string) (string, int, error) {
	endpoint := videosEndpoint()
	if endpoint == "" {
		return "", 0, fmt.Errorf("站点未配置服务器地址(ServerAddress)")
	}
	body := map[string]any{
		"model": agg.UpscaleModel,
		// 超分不需要提示词,但部分端点要求该字段存在。
		"prompt": "",
		"metadata": map[string]any{
			"task_type": "sr",
			"video":     nfsPath,
		},
	}
	if agg.UpscaleTarget != "" {
		body["size"] = agg.UpscaleTarget
	}
	payload, err := common.Marshal(body)
	if err != nil {
		return "", 0, fmt.Errorf("构造超分请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", 0, fmt.Errorf("构造超分请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+agg.CallerKey)

	client := GetHttpClient()
	if client == nil {
		client = &http.Client{}
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("超分调用失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", 0, fmt.Errorf("超分提交返回 %d", resp.StatusCode)
	}

	var parsed struct {
		TaskID string `json:"task_id"`
		ID     string `json:"id"`
	}
	if err := common.DecodeJson(resp.Body, &parsed); err != nil {
		return "", 0, fmt.Errorf("解析超分响应失败: %w", err)
	}
	publicID := parsed.TaskID
	if publicID == "" {
		publicID = parsed.ID
	}
	if publicID == "" {
		return "", 0, fmt.Errorf("超分响应没有任务 id")
	}

	// 自调用拿到的是**我们自己**的任务 id,而轮询要按上游 id 去问渠道。
	// 回查那条新任务,取出它的上游 id 与承接渠道。
	sub, exists, err := lookupTaskByPublicID(publicID)
	if err != nil || !exists || sub == nil {
		return "", 0, fmt.Errorf("超分任务 %s 回查失败: %v", publicID, err)
	}
	return sub.PrivateData.UpstreamTaskID, sub.ChannelId, nil
}
