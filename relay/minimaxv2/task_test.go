package minimaxv2

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

func v2Task(taskID string, status model.TaskStatus, modelName string, props *model.MiniMaxV2Properties) *model.Task {
	return &model.Task{
		TaskID:    taskID,
		Status:    status,
		CreatedAt: 1000,
		UpdatedAt: 2000,
		Properties: model.Properties{
			OriginModelName: modelName,
			MiniMaxV2:       props,
		},
	}
}

func TestBuildTaskStatusMapping(t *testing.T) {
	cases := map[model.TaskStatus]string{
		model.TaskStatusNotStart:   StatusQueued,
		model.TaskStatusSubmitted:  StatusQueued,
		model.TaskStatusQueued:     StatusQueued,
		model.TaskStatusInProgress: StatusRunning,
		model.TaskStatusSuccess:    StatusSucceeded,
		model.TaskStatusFailure:    StatusFailed,
	}
	for in, want := range cases {
		got := BuildTask(v2Task("task_1", in, "MiniMax-H3", &model.MiniMaxV2Properties{Duration: 6})).Status
		if got != want {
			t.Fatalf("status %s → %s, want %s", in, got, want)
		}
	}
}

func TestBuildTaskSucceeded(t *testing.T) {
	task := v2Task("task_abc", model.TaskStatusSuccess, "MiniMax-H3", &model.MiniMaxV2Properties{
		Resolution:      "768P",
		Ratio:           "16:9",
		Duration:        6,
		InputImageCount: 2,
	})
	out := BuildTask(task)

	if out.ID != "task_abc" || out.Model != "MiniMax-H3" {
		t.Fatalf("unexpected identity: %#v", out)
	}
	// 官方那个是限时 CDN 链接,我们给自己的成品代理地址(不过期)。
	if out.Content == nil || !strings.HasSuffix(out.Content.URL, "/v1/videos/task_abc/content") {
		t.Fatalf("content = %#v", out.Content)
	}
	if out.Resolution != "768P" || out.Ratio != "16:9" || out.Duration != 6 {
		t.Fatalf("echo fields = %#v", out)
	}
	if out.TaskType != TaskTypeGeneration || out.Modality != ModalityVideo {
		t.Fatalf("task_type/modality = %s/%s", out.TaskType, out.Modality)
	}
	if out.Usage == nil {
		t.Fatalf("usage missing")
	}
	// usage 是用量不是价格:四个字段全部从提交请求推出,与定价配置无关。
	if out.Usage.OutputSeconds != 6 || out.Usage.TotalSeconds != 6 ||
		out.Usage.InputSeconds != 0 || out.Usage.InputImageCount != 2 {
		t.Fatalf("usage = %#v", out.Usage)
	}
	if out.Error != nil {
		t.Fatalf("succeeded task must not carry error")
	}
}

func TestBuildTaskOmitsUsageWhenReferenceVideoPresent(t *testing.T) {
	// input_seconds 官方定义是「参考视频的计费秒数」,而我们没有探测视频时长。
	// 有参考视频就整个省掉 usage —— 不编一个看起来精确其实是猜的数。
	out := BuildTask(v2Task("task_v", model.TaskStatusSuccess, "MiniMax-H3", &model.MiniMaxV2Properties{
		Duration:        8,
		InputImageCount: 1,
		InputVideoCount: 1,
	}))
	if out.Usage != nil {
		t.Fatalf("usage should be omitted when reference videos are present: %#v", out.Usage)
	}
	if out.Duration != 8 {
		t.Fatalf("duration echo should survive: %d", out.Duration)
	}
}

func TestBuildTaskFailure(t *testing.T) {
	task := v2Task("task_f", model.TaskStatusFailure, "MiniMax-H3", &model.MiniMaxV2Properties{Duration: 5})
	task.FailReason = "engine oom"
	out := BuildTask(task)
	if out.Error == nil || out.Error.Message != "engine oom" {
		t.Fatalf("error = %#v", out.Error)
	}
	if out.Content != nil {
		t.Fatalf("failed task must not carry content")
	}
}

func TestBuildTaskWithoutSnapshotIsDefensiveOnly(t *testing.T) {
	// 调用方都先过 IsV2Task,正常到不了这条分支;真到了也只留空、不编字段。
	// IsV2Task 本身是「非 v2 任务在本协议下不存在」这条判定的唯一依据。
	task := v2Task("task_x", model.TaskStatusSuccess, "wan2.2-t2v", nil)
	if IsV2Task(task) {
		t.Fatalf("task without snapshot must not be treated as a v2 task")
	}
	out := BuildTask(task)
	if out.Resolution != "" || out.Ratio != "" || out.Duration != 0 || out.Usage != nil {
		t.Fatalf("expected empty echo fields: %#v", out)
	}
	if out.Status != StatusSucceeded {
		t.Fatalf("status = %s", out.Status)
	}
}

func TestFilterAndPage(t *testing.T) {
	props := func() *model.MiniMaxV2Properties { return &model.MiniMaxV2Properties{Duration: 5} }
	tasks := []*model.Task{
		v2Task("task_1", model.TaskStatusSuccess, "MiniMax-H3", props()),
		v2Task("task_2", model.TaskStatusFailure, "MiniMax-H3", props()),
		v2Task("task_3", model.TaskStatusQueued, "other-model", props()),
		// 非 v2 提交:不该出现在列表里(否则只能靠编造回显字段来填)。
		v2Task("task_4", model.TaskStatusSuccess, "wan2.2-t2v", nil),
	}

	all := FilterAndPage(tasks, ListFilter{PageNum: 1, PageSize: 10})
	if all.Total != 3 || len(all.Items) != 3 {
		t.Fatalf("total = %d, items = %d, want 3/3", all.Total, len(all.Items))
	}

	byStatus := FilterAndPage(tasks, ListFilter{PageNum: 1, PageSize: 10, Status: StatusSucceeded})
	if byStatus.Total != 1 || byStatus.Items[0].ID != "task_1" {
		t.Fatalf("status filter = %#v", byStatus)
	}

	byModel := FilterAndPage(tasks, ListFilter{PageNum: 1, PageSize: 10, Model: "MiniMax-H3"})
	if byModel.Total != 2 {
		t.Fatalf("model filter total = %d, want 2", byModel.Total)
	}

	byID := FilterAndPage(tasks, ListFilter{PageNum: 1, PageSize: 10, TaskIDs: []string{"task_2", "task_4"}})
	if byID.Total != 1 || byID.Items[0].ID != "task_2" {
		t.Fatalf("task_ids filter = %#v", byID)
	}

	// 我们只产出 generation 这一门类。
	byType := FilterAndPage(tasks, ListFilter{PageNum: 1, PageSize: 10, TaskType: TaskTypeRegeneration})
	if byType.Total != 0 || len(byType.Items) != 0 {
		t.Fatalf("task_type filter = %#v", byType)
	}

	page2 := FilterAndPage(tasks, ListFilter{PageNum: 2, PageSize: 2})
	if page2.Total != 3 || len(page2.Items) != 1 || page2.Items[0].ID != "task_3" {
		t.Fatalf("page 2 = %#v", page2)
	}
	// 越界页返回空数组而不是 panic。
	page9 := FilterAndPage(tasks, ListFilter{PageNum: 9, PageSize: 2})
	if page9.Total != 3 || len(page9.Items) != 0 {
		t.Fatalf("page 9 = %#v", page9)
	}
}

func TestInternalStatusesForHasNoDrift(t *testing.T) {
	// 列表接口在 SQL 里按内部状态筛,而渲染时用 taskStatusToV2 正查。两者一旦漂移,
	// 就会出现「查得到详情、却不出现在列表里」。这里断言每个内部状态恰好被一个官方
	// 状态词覆盖 —— 将来加内部状态而忘了同步,这条会红。
	seen := map[model.TaskStatus]int{}
	for _, v2 := range []string{StatusQueued, StatusRunning, StatusSucceeded, StatusFailed} {
		statuses, ok := InternalStatusesFor(v2)
		if !ok || len(statuses) == 0 {
			t.Fatalf("%s should map to at least one internal status", v2)
		}
		for _, s := range statuses {
			seen[s]++
		}
	}
	for _, s := range allInternalStatuses {
		if seen[s] != 1 {
			t.Fatalf("internal status %s covered %d times, want exactly 1", s, seen[s])
		}
	}
	// cancelled 官方有、我们产生不了:必须报「无法匹配」,不能退化成「不限状态」——
	// 那会把全部任务都列出来,正好是筛选语义的反面。
	if _, ok := InternalStatusesFor(StatusCancelled); ok {
		t.Fatalf("cancelled must be reported as unmatchable")
	}
	// 空 = 不限。
	if got, ok := InternalStatusesFor(""); !ok || got != nil {
		t.Fatalf("empty status = %v / %v, want nil / true", got, ok)
	}
}

func TestMatchesOurTaskType(t *testing.T) {
	if !MatchesOurTaskType("") || !MatchesOurTaskType(TaskTypeGeneration) {
		t.Fatalf("generation and empty must match")
	}
	if MatchesOurTaskType(TaskTypeRegeneration) || MatchesOurTaskType(TaskTypeContextIR) {
		t.Fatalf("we only ever produce generation tasks")
	}
}

func TestBuildListPage(t *testing.T) {
	props := &model.MiniMaxV2Properties{Duration: 5}
	// SQL 已经筛过分过页:这里只渲染,total 用 COUNT 给的值,不重新计算。
	page := BuildListPage([]*model.Task{
		v2Task("task_1", model.TaskStatusSuccess, "MiniMax-H3", props),
	}, 137)
	if page.Total != 137 || len(page.Items) != 1 || page.Items[0].ID != "task_1" {
		t.Fatalf("page = %#v", page)
	}
	// 空页要序列化成 [] 而不是 null。
	empty := BuildListPage(nil, 0)
	if empty.Items == nil || len(empty.Items) != 0 || empty.Total != 0 {
		t.Fatalf("empty page = %#v", empty)
	}
	raw, err := common.Marshal(empty)
	if err != nil {
		t.Fatalf("marshal: %s", err)
	}
	if !strings.Contains(string(raw), `"items":[]`) {
		t.Fatalf("empty items must marshal to []: %s", string(raw))
	}
}

func TestValidateListFilter(t *testing.T) {
	f := ListFilter{}
	if err := ValidateListFilter(&f); err != nil {
		t.Fatalf("unexpected error: %s", err.Message)
	}
	if f.PageNum != 1 || f.PageSize != DefaultPageSize {
		t.Fatalf("defaults = %#v", f)
	}
	f = ListFilter{PageSize: MaxPageSize + 500}
	_ = ValidateListFilter(&f)
	if f.PageSize != MaxPageSize {
		t.Fatalf("page size cap = %d", f.PageSize)
	}
	if err := ValidateListFilter(&ListFilter{Status: "done"}); err == nil {
		t.Fatalf("expected error for unknown status")
	}
	if err := ValidateListFilter(&ListFilter{TaskType: "video"}); err == nil {
		t.Fatalf("expected error for unknown task_type")
	}
	// task_ids 封顶:超限时给一句说得清的 400,而不是让驱动抛「参数太多」的 500。
	if err := ValidateListFilter(&ListFilter{TaskIDs: make([]string, MaxTaskIDs+1)}); err == nil {
		t.Fatalf("expected error when task_ids exceeds the cap")
	}
	if err := ValidateListFilter(&ListFilter{TaskIDs: make([]string, MaxTaskIDs)}); err != nil {
		t.Fatalf("unexpected error at cap: %s", err.Message)
	}
}

func TestSoftDeletedTaskIsInvisibleToV2(t *testing.T) {
	// 软删过的任务在本协议下「就是不存在」:查询、列表、重复删除三处共用 IsV2Task
	// 这一个判据,所以只需锁住它。这正是官方 DELETE 之后的可观测行为。
	task := v2Task("task_del", model.TaskStatusSuccess, "MiniMax-H3",
		&model.MiniMaxV2Properties{Duration: 6})
	if !IsV2Task(task) {
		t.Fatalf("task should be visible before deletion")
	}
	task.Properties.MiniMaxV2.Deleted = true
	if IsV2Task(task) {
		t.Fatalf("soft-deleted task must be invisible to the v2 protocol")
	}
	// 列表侧同样看不到(FilterAndPage 先过 IsV2Task)。
	page := FilterAndPage([]*model.Task{task}, ListFilter{PageNum: 1, PageSize: 10})
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("soft-deleted task must not appear in listings: %#v", page)
	}
}

func TestDeleteActionRequiresV2Task(t *testing.T) {
	// 删除入口必须先过 IsV2Task:任务表里还躺着体验区 / Suno / MJ 的记录,
	// 而全仓此前没有任何用户可达的删除接口,不校验就等于开了一个删任意历史的口子。
	// (闸门在 controller.MiniMaxV2DeleteTask,这里锁住它依赖的判据。)
	nonV2 := v2Task("task_mj", model.TaskStatusSuccess, "midjourney", nil)
	if IsV2Task(nonV2) {
		t.Fatalf("non-v2 task must not pass the delete gate")
	}
	v2 := v2Task("task_v2", model.TaskStatusSuccess, "MiniMax-H3", &model.MiniMaxV2Properties{Duration: 5})
	if !IsV2Task(v2) {
		t.Fatalf("v2 task must pass the delete gate")
	}
}

func TestCreateSuccessBody(t *testing.T) {
	// 统一契约回的是 OpenAI 风格的完整 video 对象,官方只回一个 task_id。
	out, err := CreateSuccessBody([]byte(`{"id":"task_abc","object":"video","status":"queued"}`))
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}
	if string(out) != `{"task_id":"task_abc"}` {
		t.Fatalf("body = %s", string(out))
	}
	if _, err := CreateSuccessBody([]byte(`{"object":"video"}`)); err == nil {
		t.Fatalf("expected error when no task id present")
	}
}

func TestDeleteAction(t *testing.T) {
	for _, status := range []model.TaskStatus{model.TaskStatusSuccess, model.TaskStatusFailure} {
		action, apiErr := DeleteAction(v2Task("task_d", status, "MiniMax-H3", nil))
		if apiErr != nil || action != "deleted" {
			t.Fatalf("status %s → %q / %#v", status, action, apiErr)
		}
	}
	// 提交后立刻下发引擎,没有可取消的排队窗口 —— 如实报错,不假装取消成功。
	for _, status := range []model.TaskStatus{model.TaskStatusQueued, model.TaskStatusInProgress, model.TaskStatusSubmitted} {
		_, apiErr := DeleteAction(v2Task("task_d", status, "MiniMax-H3", nil))
		if apiErr == nil || !strings.Contains(apiErr.Message, "cannot be cancelled") {
			t.Fatalf("status %s → %#v", status, apiErr)
		}
	}
}

func TestIsErrorEnvelope(t *testing.T) {
	body := BuildErrorBody("req-1", 400, ErrTypeBadRequest, "boom")
	if !IsErrorEnvelope(body) {
		t.Fatalf("expected envelope: %s", string(body))
	}
	if !strings.Contains(string(body), `"http_code":"400"`) {
		t.Fatalf("http_code missing: %s", string(body))
	}
	if IsErrorEnvelope([]byte(`{"code":"x","message":"y"}`)) {
		t.Fatalf("task error must not be treated as envelope")
	}
	if IsErrorEnvelope(nil) {
		t.Fatalf("empty body is not an envelope")
	}
}
