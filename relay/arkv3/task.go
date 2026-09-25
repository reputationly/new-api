package arkv3

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"

	"github.com/gin-gonic/gin"
)

// ContextKeySnapshot 是提交侧把 Snapshot 交给任务落库侧的 gin context 键。
const ContextKeySnapshot = "ark_v3_snapshot"

// StoreSnapshot 由请求转换中间件调用。
func StoreSnapshot(c *gin.Context, s *Snapshot) {
	c.Set(ContextKeySnapshot, s)
}

// ApplyTaskSnapshot 把方舟提交快照写进任务的 Properties。**非方舟端点提交的任务是
// no-op** —— 快照的存在本身就是「这是一个方舟任务」的判据（查询/列表/删除都靠它），
// 给别的入口也写一份会让这个判据失效。
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
	clone := *s
	task.Properties.ArkV3 = &clone
}

// IsArkTask 判断任务在方舟协议下是否可见：经方舟端点提交，且没有被软删。
func IsArkTask(task *model.Task) bool {
	return task != nil && task.Properties.ArkV3 != nil && !task.Properties.ArkV3.Deleted
}

// upstreamEcho 是上游查询回执里我们要回显的那部分。
//
// 轮询会把上游的**查询响应**原样存进 Task.Data（service/task_polling.go），对 doubao
// 渠道来说那就是一份完整的方舟回执，直接读即可。任务刚提交、还没被轮询过时，Data 里
// 只有提交响应（一个 id），解出来全是零值，于是下面一路回落到提交快照 —— 这正是
// 快照存在的理由。非方舟渠道的 Data 同理，解不出就用快照。
type upstreamEcho struct {
	Status  string `json:"status"`
	Content struct {
		VideoURL     string `json:"video_url"`
		LastFrameURL string `json:"last_frame_url"`
	} `json:"content"`
	Seed            *int   `json:"seed"`
	Resolution      string `json:"resolution"`
	Ratio           string `json:"ratio"`
	Duration        int    `json:"duration"`
	Frames          int    `json:"frames"`
	FramesPerSecond int    `json:"framespersecond"`
	GenerateAudio   *bool  `json:"generate_audio"`
	Tools           []Tool `json:"tools"`
	ServiceTier     string `json:"service_tier"`
	OutputFormat    string `json:"output_format"`
	// Output.Duration：方舟文档把实际时长放顶层，部分兼容网关放在 output 下。两处都读，
	// 只读一处的话换个上游就静默取到 0（与 doubao adaptor 的 actualDuration 同一理由）。
	Output struct {
		Duration int `json:"duration"`
	} `json:"output"`
	Usage struct {
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
		ToolUsage        struct {
			WebSearch int `json:"web_search"`
		} `json:"tool_usage"`
	} `json:"usage"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func parseUpstreamEcho(task *model.Task) *upstreamEcho {
	if len(task.Data) == 0 {
		return &upstreamEcho{}
	}
	echo := &upstreamEcho{}
	if err := common.Unmarshal(task.Data, echo); err != nil {
		// Data 不是 JSON 对象（别的渠道可能存别的形态）。不是错误，回落到快照即可。
		return &upstreamEcho{}
	}
	return echo
}

// BuildTask 把任务记录渲染成方舟的 task 对象。
//
// 取值优先级是**上游回执 > 提交快照**：ratio=adaptive 时的实际比例、duration 省略时
// 模型自选的秒数，都只有回执知道。回执缺的（还没轮询到）才用请求值兜底。
//
// 唯独 status 反过来 —— 恒以**我们的任务状态**为准，绝不用回执里的。理由：任务可能被
// 我们判超时失败、被调用方取消，聚合流水线还可能有第二段在跑，这些上游都不知道。
func BuildTask(task *model.Task) Task {
	echo := parseUpstreamEcho(task)
	props := task.Properties.ArkV3
	if props == nil {
		// 防御性兜底，正常到不了这里：所有调用方都先过了 IsArkTask。
		props = &model.ArkV3Properties{}
	}

	out := Task{
		ID: task.TaskID,
		// 回显与 filter.model 比对的都必须是**调用方提交的那个模型名**，聚合模型不能
		// 露出展开后的生成段模型。规则见 model.Task.PublicModelName。
		Model:     task.PublicModelName(),
		Status:    taskStatusToArk(task, echo),
		CreatedAt: task.CreatedAt,
		UpdatedAt: task.UpdatedAt,

		Resolution:      firstNonEmpty(echo.Resolution, props.Resolution),
		Ratio:           firstNonEmpty(echo.Ratio, props.Ratio),
		Duration:        firstNonZero(echo.Duration, echo.Output.Duration, props.Duration),
		Frames:          firstNonZero(echo.Frames, props.Frames),
		FramesPerSecond: echo.FramesPerSecond,
		ServiceTier:     firstNonEmpty(echo.ServiceTier, props.ServiceTier),
		OutputFormat:    firstNonEmpty(echo.OutputFormat, props.OutputFormat),

		SafetyIdentifier:      props.SafetyIdentifier,
		Priority:              props.Priority,
		Draft:                 props.Draft,
		ExecutionExpiresAfter: props.ExecutionExpiresAfter,
	}
	if out.CreatedAt == 0 {
		out.CreatedAt = task.SubmitTime
	}
	if out.UpdatedAt == 0 {
		out.UpdatedAt = out.CreatedAt
	}
	if echo.Seed != nil {
		out.Seed = echo.Seed
	} else {
		out.Seed = props.Seed
	}
	if echo.GenerateAudio != nil {
		out.GenerateAudio = echo.GenerateAudio
	} else {
		out.GenerateAudio = props.GenerateAudio
	}
	if len(echo.Tools) > 0 {
		out.Tools = echo.Tools
	} else if len(props.Tools) > 0 {
		out.Tools = toolsFromTypes(props.Tools)
	}

	// 只有上游真的报了用量才透出。视频按 token 计价，编一个数会让调用方对账对到一个
	// 不存在的量上（官方 schema 里 usage 本就不是必填）。
	if echo.Usage.CompletionTokens > 0 || echo.Usage.TotalTokens > 0 {
		out.Usage = &Usage{
			CompletionTokens: echo.Usage.CompletionTokens,
			TotalTokens:      echo.Usage.TotalTokens,
		}
		if echo.Usage.ToolUsage.WebSearch > 0 {
			out.Usage.ToolUsage = &ToolUsage{WebSearch: echo.Usage.ToolUsage.WebSearch}
		}
	}

	switch out.Status {
	case StatusSucceeded:
		// 官方给的是 24 小时限时 CDN 链接，我们给的是自己的成品代理地址、不过期
		// （需要带同一把 API key 访问）。
		out.Content = &Content{VideoURL: taskcommon.BuildProxyURL(task.TaskID)}
		// 尾帧是上游的限时直链，我们没有转存它，原样透出并只在确有时才带上。
		if echo.Content.LastFrameURL != "" {
			out.Content.LastFrameURL = echo.Content.LastFrameURL
		}
	case StatusFailed, StatusExpired:
		out.Error = &TaskError{
			Code:    firstNonEmpty(echo.Error.Code, "InternalServiceError"),
			Message: firstNonEmpty(echo.Error.Message, task.FailReason),
		}
	case StatusCancelled:
		out.Error = &TaskError{Code: CodeNotSupported, Message: firstNonEmpty(task.FailReason, "task cancelled by the caller")}
	}
	return out
}

// taskStatusToArk 把内部任务状态映射成官方状态词。
//
// 两处特判，都是**有据可依**而不是猜：
//   - 取消：任务表没有 CANCELLED 态，取消复用 FAILURE + PrivateData.Cancelled 这个
//     标记渲染（与异步图片同一套做法，见 relay.BuildImageJob）。判定必须先于 failed。
//   - 过期：execution_expires_after 到期是**上游**判的，回执里明写 status=expired，
//     而 doubao 适配器把它归进了 FAILURE。回执说了就照回执回显，不猜。
func taskStatusToArk(task *model.Task, echo *upstreamEcho) string {
	switch task.Status {
	case model.TaskStatusSuccess:
		return StatusSucceeded
	case model.TaskStatusFailure:
		if task.PrivateData.Cancelled {
			return StatusCancelled
		}
		if strings.EqualFold(strings.TrimSpace(echo.Status), StatusExpired) {
			return StatusExpired
		}
		return StatusFailed
	case model.TaskStatusInProgress:
		return StatusRunning
	default:
		// NOT_START / SUBMITTED / QUEUED / UNKNOWN 都还没开跑。
		return StatusQueued
	}
}

// BuildQueryBody 渲染 GET /api/v3/contents/generations/tasks/{task_id} 的响应体。
func BuildQueryBody(task *model.Task) ([]byte, error) {
	return common.Marshal(BuildTask(task))
}

// CreateSuccessBody 把统一契约的提交成功响应（OpenAI 风格 video 对象）改写成官方形态
// —— 方舟提交接口只回一个 id。safetyIdentifier 由调用方原样回显（官方行为）。
func CreateSuccessBody(safetyIdentifier string) func([]byte) ([]byte, error) {
	return func(body []byte) ([]byte, error) {
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
		return common.Marshal(CreateResponse{ID: taskID, SafetyIdentifier: safetyIdentifier})
	}
}

// ── 列表 ─────────────────────────────────────────────────────────────────────

// 官方分页边界：page_num / page_size 均为 1–500，page_size 缺省 20。
const (
	DefaultPageSize = 20
	MinPage         = 1
	MaxPage         = 500

	// MaxTaskIDs 是 filter.task_ids 的条数上限。不封顶的话超出驱动的绑定变量上限
	// （sqlite 32766 / PostgreSQL 65535）会变成一句「参数太多」的 500，不如就地
	// 给一个说得清的 400。500 与分页上限同阶，正常用法碰不到。
	MaxTaskIDs = 500
)

// ListFilter 是 GET /api/v3/contents/generations/tasks 的查询条件。
type ListFilter struct {
	PageNum     int
	PageSize    int
	Status      string
	TaskIDs     []string
	Model       string
	ServiceTier string
}

// ValidateListFilter 校验并归一分页与筛选条件。
func ValidateListFilter(f *ListFilter) *APIError {
	if f.PageNum == 0 {
		f.PageNum = MinPage
	}
	if f.PageSize == 0 {
		f.PageSize = DefaultPageSize
	}
	if f.PageNum < MinPage || f.PageNum > MaxPage {
		return badRequest(fmt.Sprintf("page_num must be between %d and %d, got %d", MinPage, MaxPage, f.PageNum))
	}
	if f.PageSize < MinPage || f.PageSize > MaxPage {
		return badRequest(fmt.Sprintf("page_size must be between %d and %d, got %d", MinPage, MaxPage, f.PageSize))
	}
	if len(f.TaskIDs) > MaxTaskIDs {
		return badRequest(fmt.Sprintf("at most %d filter.task_ids are allowed, got %d", MaxTaskIDs, len(f.TaskIDs)))
	}
	if f.Status != "" && !officialStatuses[f.Status] {
		return badRequest(fmt.Sprintf(
			"filter.status=%q is not supported (expected queued / running / cancelled / succeeded / failed / expired)", f.Status))
	}
	if f.ServiceTier != "" && f.ServiceTier != ServiceTierDefault && f.ServiceTier != ServiceTierFlex {
		return badRequest(fmt.Sprintf("filter.service_tier=%q is not supported (expected default / flex)", f.ServiceTier))
	}
	return nil
}

// InternalStatusesFor 把官方状态词反解成内部状态集合，供列表接口在 SQL 里筛。
//
// **反查 taskStatusToArk 而不是另写一张表**：官方状态与内部状态是一对多的
// （queued ← NOT_START/SUBMITTED/QUEUED/UNKNOWN），两处各写一份必然漂移。
//
// cancelled / expired 都落在 FAILURE 上，SQL 只能筛到 FAILURE，真正的区分由
// FilterAndPage 在 Go 里完成 —— 所以这两个词返回的集合是「候选」而非「结果」，
// 第二个返回值告诉调用方还需不需要在 Go 里复筛。
func InternalStatusesFor(arkStatus string) (statuses []model.TaskStatus, needsGoFilter bool) {
	switch arkStatus {
	case "":
		return nil, false // 不限状态
	case StatusSucceeded:
		return []model.TaskStatus{model.TaskStatusSuccess}, false
	case StatusRunning:
		return []model.TaskStatus{model.TaskStatusInProgress}, false
	case StatusQueued:
		return []model.TaskStatus{
			model.TaskStatusNotStart, model.TaskStatusSubmitted,
			model.TaskStatusQueued, model.TaskStatusUnknown,
		}, false
	case StatusFailed, StatusCancelled, StatusExpired:
		return []model.TaskStatus{model.TaskStatusFailure}, true
	}
	return nil, false
}

// BuildListPage 渲染已经由 SQL 完成筛选与分页的一页任务。
func BuildListPage(tasks []*model.Task, total int64) ListResponse {
	items := make([]Task, 0, len(tasks))
	for _, t := range tasks {
		items = append(items, BuildTask(t))
	}
	return ListResponse{Items: items, Total: int(total)}
}

// FilterAndPage 在候选任务上应用筛选并切页。
//
// 只服务候选集天然有界的路径：按精确 task_ids 查（索引查询）、带 filter.model 或
// 需要区分 failed/cancelled/expired 的查询（这些维度分别在 Properties 这个 JSON 列
// 和 PrivateData 里，跨库筛不了，只能取回来在 Go 里筛）。
func FilterAndPage(tasks []*model.Task, f ListFilter) ListResponse {
	matched := make([]Task, 0, len(tasks))
	wanted := map[string]bool{}
	for _, id := range f.TaskIDs {
		wanted[id] = true
	}
	for _, t := range tasks {
		// 只列方舟端点提交的任务：任务表里还有体验区、Suno、MJ 等各种任务，混进来
		// 既会泄露无关记录，又只能靠编造回显字段来填。
		if !IsArkTask(t) {
			continue
		}
		if len(wanted) > 0 && !wanted[t.TaskID] {
			continue
		}
		ark := BuildTask(t)
		if f.Status != "" && ark.Status != f.Status {
			continue
		}
		if f.Model != "" && ark.Model != f.Model {
			continue
		}
		if !matchesServiceTier(ark.ServiceTier, f.ServiceTier) {
			continue
		}
		matched = append(matched, ark)
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

// matchesServiceTier 比对服务等级。**空值等同 default**：service_tier 是可选参数，
// 绝大多数请求不带它，按字面比对的话 filter.service_tier=default 会一条都筛不到。
func matchesServiceTier(taskTier, want string) bool {
	if want == "" {
		return true
	}
	if taskTier == "" {
		taskTier = ServiceTierDefault
	}
	return taskTier == want
}

// ── 取消 / 删除 ──────────────────────────────────────────────────────────────

// 官方 DELETE 的两种结果。
const (
	ActionCancel = "cancel"
	ActionDelete = "delete"
)

// DeleteAction 判定 DELETE /api/v3/contents/generations/tasks/{task_id} 该做什么，
// 严格对齐官方的状态表：
//
//	queued     → 取消排队，任务变 cancelled（我们还要退款）
//	running    → 不支持
//	cancelled  → 不支持（官方：取消状态保留 24 小时后自动删除）
//	succeeded / failed / expired → 删记录
//
// 「删记录」在我们这儿是**协议侧软删**（Properties.ArkV3.Deleted），不是删行 ——
// 同一行还背着产物代理、下载、分享链接、remix 原任务等与本协议无关的功能，删行等于
// 替调用方把这些一并掐断，而官方那边「删记录」只是从任务列表里移除。
func DeleteAction(task *model.Task) (action string, apiErr *APIError) {
	switch task.Status {
	case model.TaskStatusSuccess:
		return ActionDelete, nil
	case model.TaskStatusFailure:
		if task.PrivateData.Cancelled {
			return "", newError(http.StatusBadRequest, CodeNotSupported, TypeBadRequest, fmt.Sprintf(
				"task %s is already cancelled and cannot be deleted; cancelled records are removed automatically", task.TaskID))
		}
		return ActionDelete, nil
	case model.TaskStatusInProgress:
		return "", newError(http.StatusBadRequest, CodeNotSupported, TypeBadRequest, fmt.Sprintf(
			"task %s is running and can neither be cancelled nor deleted", task.TaskID))
	default:
		// NOT_START / SUBMITTED / QUEUED / UNKNOWN —— 尚未开跑，可以取消。
		return ActionCancel, nil
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

func firstNonZero(vals ...int) int {
	for _, v := range vals {
		if v != 0 {
			return v
		}
	}
	return 0
}

func toolsFromTypes(types []string) []Tool {
	out := make([]Tool, 0, len(types))
	for _, t := range types {
		out = append(out, Tool{Type: t})
	}
	return out
}
