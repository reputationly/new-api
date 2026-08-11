package minimaxv2

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"

	"github.com/gin-gonic/gin"
)

// ContextKeySnapshot 是提交侧把 Snapshot 交给任务落库侧的 gin context 键。
const ContextKeySnapshot = "minimax_v2_snapshot"

// StoreSnapshot 由请求转换中间件调用。
func StoreSnapshot(c *gin.Context, s *Snapshot) {
	c.Set(ContextKeySnapshot, s)
}

// ApplyTaskSnapshot 把 v2 提交快照写进任务的 Properties(JSON 列,加字段无需迁移,
// 老行反序列化成零值)。**非 v2 端点提交的任务是 no-op** —— 快照的语义由 v2 协议定义,
// 给别的入口也写一份既没人读,又会让「这个任务是不是 v2 提交的」失去判据(列表接口正是
// 靠它筛选)。
func ApplyTaskSnapshot(c *gin.Context, task *model.Task) {
	if c == nil || task == nil {
		return
	}
	v, ok := c.Get(ContextKeySnapshot)
	if !ok {
		return
	}
	s, ok := v.(*Snapshot)
	if !ok || s == nil {
		return
	}
	task.Properties.MiniMaxV2 = &model.MiniMaxV2Properties{
		Resolution:      s.Resolution,
		Ratio:           s.Ratio,
		Duration:        s.Duration,
		InputImageCount: s.InputImageCount,
		InputVideoCount: s.InputVideoCount,
	}
}

// IsV2Task 判断任务在 v2 协议下是否可见:经 v2 端点提交,且没有被软删。
//
// 这是三个端点共用的唯一判据 —— 查询、列表、删除都靠它。软删过的任务在本协议下
// 「就是不存在」,与从没提交过的任务不可区分,这正是官方 DELETE 之后的可观测行为。
func IsV2Task(task *model.Task) bool {
	return task != nil && task.Properties.MiniMaxV2 != nil && !task.Properties.MiniMaxV2.Deleted
}

// BuildTask 把任务记录渲染成官方 task 对象。
func BuildTask(task *model.Task) Task {
	out := Task{
		ID:        task.TaskID,
		Model:     firstNonEmpty(task.Properties.OriginModelName, task.Properties.UpstreamModelName),
		Status:    taskStatusToV2(task.Status),
		CreatedAt: task.CreatedAt,
		UpdatedAt: task.UpdatedAt,
		// 我们只提供生成这一门类:官方 task_type 区分的是生成 / 提示词增强 / 2K 重生成,
		// 后两者本网关都不做(见 §「必须明确拒绝的」)。
		TaskType: TaskTypeGeneration,
		Modality: ModalityVideo,
	}
	if out.CreatedAt == 0 {
		out.CreatedAt = task.SubmitTime
	}
	if out.UpdatedAt == 0 {
		out.UpdatedAt = out.CreatedAt
	}
	if task.Status == model.TaskStatusFailure {
		out.Error = &TaskError{Code: "task_failed", Message: task.FailReason}
	}
	if task.Status == model.TaskStatusSuccess {
		// 官方那个是限时 CDN 链接,我们给的是自己的成品代理地址、不过期
		// (需要带同一个 API key 访问)。
		out.Content = &Content{URL: taskcommon.BuildProxyURL(task.TaskID)}
	}

	props := task.Properties.MiniMaxV2
	if props == nil {
		// 防御性兜底,正常到不了这里:两个调用方(查询分支与 FilterAndPage)都已经先过
		// IsV2Task 了。真到了也只留空、不猜 —— usage 各字段在官方 schema 里都不是必填。
		return out
	}
	out.Resolution = props.Resolution
	out.Ratio = props.Ratio
	out.Duration = props.Duration
	out.Usage = buildUsage(props)
	return out
}

// buildUsage 组装用量。
//
// ⚠️ input_seconds 有个真缺口:官方定义是「参考视频的计费秒数」,而我们没有探测视频时长
// (nfsinput 只有 checkAudioDuration,视频没有对应探测)。处理方式是:
//
//	无参考视频 → input_seconds: 0(准确);
//	有参考视频 → 整个 usage 字段省略。
//
// 不编一个看起来精确其实是猜的数。官方 schema 里 usage 各字段都不是必填。
func buildUsage(props *model.MiniMaxV2Properties) *Usage {
	if props.InputVideoCount > 0 {
		return nil
	}
	return &Usage{
		TotalSeconds:    props.Duration,
		InputSeconds:    0,
		OutputSeconds:   props.Duration,
		InputImageCount: props.InputImageCount,
	}
}

// taskStatusToV2 把内部任务状态映射成官方状态词。
//
// 官方多一个 cancelled,我们产生不了:任务一提交就下发引擎,queued 窗口极短,
// 没有可取消的窗口(见 DELETE 接口的说明)。
func taskStatusToV2(status model.TaskStatus) string {
	switch status {
	case model.TaskStatusSuccess:
		return StatusSucceeded
	case model.TaskStatusFailure:
		return StatusFailed
	case model.TaskStatusInProgress:
		return StatusRunning
	default:
		// NOT_START / SUBMITTED / QUEUED / UNKNOWN 都还没开跑。
		return StatusQueued
	}
}

// MatchesOurTaskType 判断官方 task_type 的筛选值能否命中我们产出的任务。
// 我们只产出 generation 这一门类(h3_context_ir / regeneration 两个端点是如实 501 的),
// 所以筛别的值必然是空集 —— 调用方据此早退,别把它当成「不限门类」。
func MatchesOurTaskType(taskType string) bool {
	return taskType == "" || taskType == TaskTypeGeneration
}

// allInternalStatuses 是内部任务状态的全集,供反查官方状态词用。
var allInternalStatuses = []model.TaskStatus{
	model.TaskStatusNotStart,
	model.TaskStatusSubmitted,
	model.TaskStatusQueued,
	model.TaskStatusInProgress,
	model.TaskStatusFailure,
	model.TaskStatusSuccess,
	model.TaskStatusUnknown,
}

// InternalStatusesFor 把官方状态词反解成内部状态集合,供列表接口在 SQL 里筛。
//
// **反查 taskStatusToV2 而不是另写一张表**:官方状态与内部状态是一对多的
// (queued ← NOT_START/SUBMITTED/QUEUED/UNKNOWN),两处各写一份必然漂移 ——
// 将来加一个内部状态,正查会把它归入 queued,反查却漏掉它,列表就少一批任务。
//
// 返回 false 表示这个状态词我们**永远产生不了**(官方的 cancelled:任务一提交就下发
// 引擎,没有可取消的窗口),调用方应直接给空结果,而不是当成「不限状态」。
func InternalStatusesFor(v2Status string) ([]model.TaskStatus, bool) {
	if v2Status == "" {
		return nil, true // 不限状态
	}
	var out []model.TaskStatus
	for _, s := range allInternalStatuses {
		if taskStatusToV2(s) == v2Status {
			out = append(out, s)
		}
	}
	return out, len(out) > 0
}

// BuildListPage 渲染已经由 SQL 完成筛选与分页的一页任务。
// 与 FilterAndPage 的区别:这里不再筛不再切,total 由 SQL 的 COUNT 给出。
func BuildListPage(tasks []*model.Task, total int64) ListResponse {
	items := make([]Task, 0, len(tasks))
	for _, t := range tasks {
		items = append(items, BuildTask(t))
	}
	return ListResponse{Items: items, Total: int(total)}
}

// BuildQueryBody 渲染 GET /v2/query/video_generation/{task_id} 的响应体。
func BuildQueryBody(task *model.Task) ([]byte, error) {
	return common.Marshal(QueryResponse{Task: BuildTask(task)})
}

// ── 列表 ─────────────────────────────────────────────────────────────────────

// 列表分页默认与上限。官方没有公布 page_size 上限,这里按常规取 100 封顶。
const (
	DefaultPageSize = 20
	MaxPageSize     = 100

	// MaxTaskIDs 是 filter.task_ids 的条数上限。
	//
	// 不是驱动马上会炸(modernc/sqlite 绑定变量上限 32766、PostgreSQL 65535),而是
	// 不封顶的话超限会变成一句「参数太多」的 500 —— 不如就地给一个说得清的 400。
	// 1000 远低于任一上限,正常用法碰不到。
	MaxTaskIDs = 1000
)

// ListFilter 是 GET /v2/query/video_generation 的查询条件。
type ListFilter struct {
	PageNum  int
	PageSize int
	Status   string
	TaskIDs  []string
	Model    string
	TaskType string
}

// ValidateListFilter 校验并归一分页与筛选条件。
func ValidateListFilter(f *ListFilter) *APIError {
	if f.PageNum <= 0 {
		f.PageNum = 1
	}
	if f.PageSize <= 0 {
		f.PageSize = DefaultPageSize
	}
	if f.PageSize > MaxPageSize {
		f.PageSize = MaxPageSize
	}
	if len(f.TaskIDs) > MaxTaskIDs {
		return badRequest(fmt.Sprintf(
			"invalid params: at most %d filter.task_ids are allowed, got %d", MaxTaskIDs, len(f.TaskIDs)))
	}
	if f.Status != "" && !officialStatuses[f.Status] {
		return badRequest(fmt.Sprintf(
			"invalid params: filter.status=%q is not supported (expected queued / running / succeeded / failed / cancelled)", f.Status))
	}
	if f.TaskType != "" && !officialTaskTypes[f.TaskType] {
		return badRequest(fmt.Sprintf(
			"invalid params: filter.task_type=%q is not supported (expected generation / h3_context_ir / regeneration)", f.TaskType))
	}
	return nil
}

// FilterAndPage 在候选任务上应用筛选并切页。
//
// 只服务两条**候选集天然有界**的路径:按精确 task_ids 查(索引查询,结果完整),
// 以及带 filter.model 的查询(模型名在 Properties 这个 JSON 列里,跨库筛不了,
// 只能取回来在 Go 里筛)。常规列表走 SQL 分页 + BuildListPage,不经过这里。
func FilterAndPage(tasks []*model.Task, f ListFilter) ListResponse {
	matched := make([]Task, 0, len(tasks))
	wanted := map[string]bool{}
	for _, id := range f.TaskIDs {
		wanted[id] = true
	}
	for _, t := range tasks {
		// 只列 v2 端点提交的任务:任务表里还有体验区、Suno、MJ 等各种任务,把它们混进来
		// 既会泄露无关记录,又只能靠编造回显字段来填 —— 那不是兼容,是造假。
		if !IsV2Task(t) {
			continue
		}
		if len(wanted) > 0 && !wanted[t.TaskID] {
			continue
		}
		v2 := BuildTask(t)
		if f.Status != "" && v2.Status != f.Status {
			continue
		}
		if f.Model != "" && v2.Model != f.Model {
			continue
		}
		if f.TaskType != "" && v2.TaskType != f.TaskType {
			continue
		}
		matched = append(matched, v2)
	}

	total := len(matched)
	start := (f.PageNum - 1) * f.PageSize
	if start > total {
		start = total
	}
	end := start + f.PageSize
	if end > total {
		end = total
	}
	return ListResponse{Items: matched[start:end], Total: total}
}

// ── 提交响应改写 ─────────────────────────────────────────────────────────────

// CreateSuccessBody 把统一契约的提交成功响应(OpenAI 风格 video 对象)改写成官方形态
// —— 官方提交接口只回一个 task_id。
func CreateSuccessBody(body []byte) ([]byte, error) {
	var ov struct {
		ID     string `json:"id"`
		TaskID string `json:"task_id"`
	}
	if err := common.Unmarshal(body, &ov); err != nil {
		return nil, fmt.Errorf("parse submit response failed: %w", err)
	}
	taskID := firstNonEmpty(ov.ID, ov.TaskID)
	if taskID == "" {
		return nil, fmt.Errorf("submit response carries no task id: %s", string(body))
	}
	return common.Marshal(CreateResponse{TaskID: taskID})
}

// ── 任务删除 ─────────────────────────────────────────────────────────────────

// DeleteAction 判定 DELETE /v2/video_generation/{task_id} 该做什么。
//
// 官方的语义是「queued 取消(不计费)、succeeded/failed 删记录、running/cancelled 报错」。
// **我们只能做到删记录**:任务提交后立刻下发引擎,queued 窗口极短,取消基本无效,
// 而且已经扣过费了 —— 假装取消成功却照样出片照样计费,比明确说做不到更糟。
//
// 「删记录」在我们这儿是**协议侧软删**(Properties.MiniMaxV2.Deleted),不是删行 ——
// 原因见 model.MiniMaxV2Properties.Deleted 的说明。只放行两个终态还有一层好处:
// 轮询只捞未完成任务(GetAllUnFinishSyncTasks 排除 SUCCESS/FAILURE),软删因此
// 不可能与轮询/结算的写入撞车。
func DeleteAction(task *model.Task) (action string, apiErr *APIError) {
	switch task.Status {
	case model.TaskStatusSuccess, model.TaskStatusFailure:
		return "deleted", nil
	default:
		return "", newError(http.StatusBadRequest, ErrTypeBadRequest, fmt.Sprintf(
			"task %s is in status %q and cannot be cancelled: this gateway dispatches submissions to the engine immediately, so there is no cancellable queue window; only succeeded or failed task records can be deleted",
			task.TaskID, taskStatusToV2(task.Status)))
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
