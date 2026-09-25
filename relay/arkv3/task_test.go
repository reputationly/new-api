package arkv3

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/require"
)

func arkTask(status model.TaskStatus, props *model.ArkV3Properties, data string) *model.Task {
	t := &model.Task{
		TaskID:     "task_abc",
		Status:     status,
		SubmitTime: 1700000000,
		Properties: model.Properties{OriginModelName: "doubao-seedance-2-0-260128", ArkV3: props},
	}
	if data != "" {
		t.Data = json.RawMessage(data)
	}
	return t
}

func ptrInt(v int) *int    { return &v }
func ptrBool(v bool) *bool { return &v }

// 刚提交、还没被轮询过的任务：Data 里只有上游的提交响应（一个 id），回显全靠快照。
// 这是常规用法（提交完立刻查），快照缺了的话调用方会看到一个什么参数都没有的任务。
func TestBuildTaskFallsBackToSnapshotBeforeFirstPoll(t *testing.T) {
	task := arkTask(model.TaskStatusSubmitted, &model.ArkV3Properties{
		Resolution: "720p", Ratio: "16:9", Duration: 5,
		GenerateAudio: ptrBool(false), Priority: ptrInt(7),
		SafetyIdentifier: "sha256-x", ServiceTier: "default",
		ExecutionExpiresAfter: 7200, Tools: []string{"web_search"},
	}, `{"id":"cgt-upstream-1"}`)

	out := BuildTask(task)
	require.Equal(t, "task_abc", out.ID)
	require.Equal(t, StatusQueued, out.Status)
	require.Equal(t, "720p", out.Resolution)
	require.Equal(t, "16:9", out.Ratio)
	require.Equal(t, 5, out.Duration)
	require.False(t, *out.GenerateAudio)
	require.Equal(t, 7, *out.Priority)
	require.Equal(t, "sha256-x", out.SafetyIdentifier)
	require.Equal(t, 7200, out.ExecutionExpiresAfter)
	require.Equal(t, []Tool{{Type: "web_search"}}, out.Tools)
	require.Nil(t, out.Usage, "上游还没报用量时不能编一个出来")
	// 上游任务 ID 绝不能出现在对外响应里。
	require.NotContains(t, out.ID, "cgt-")
}

// 轮询之后：上游回执里的**实际值**压过请求值。ratio=adaptive 时的实际比例、
// duration 省略时模型自选的秒数，都只有回执知道。
func TestBuildTaskPrefersUpstreamEchoOverSnapshot(t *testing.T) {
	task := arkTask(model.TaskStatusSuccess, &model.ArkV3Properties{
		Resolution: "720p", Ratio: "adaptive", Duration: 5,
	}, `{
		"id": "cgt-upstream-1",
		"status": "succeeded",
		"content": {"video_url": "https://upstream/v.mp4", "last_frame_url": "https://upstream/last.jpg"},
		"resolution": "1080p",
		"ratio": "9:16",
		"duration": 8,
		"framespersecond": 24,
		"seed": 42,
		"usage": {"completion_tokens": 216900, "total_tokens": 216900}
	}`)

	out := BuildTask(task)
	require.Equal(t, StatusSucceeded, out.Status)
	require.Equal(t, "1080p", out.Resolution)
	require.Equal(t, "9:16", out.Ratio)
	require.Equal(t, 8, out.Duration)
	require.Equal(t, 24, out.FramesPerSecond)
	require.Equal(t, 42, *out.Seed)
	require.Equal(t, 216900, out.Usage.CompletionTokens)

	// 视频地址必须换成我们自己的代理地址：上游那条是 24 小时限时链接，而且直接
	// 透出等于把上游产物地址（连同它的鉴权模型）暴露给调用方。
	require.NotEqual(t, "https://upstream/v.mp4", out.Content.VideoURL)
	require.Contains(t, out.Content.VideoURL, "/v1/videos/task_abc/content")
	// 尾帧我们没有转存，如实透传上游直链。
	require.Equal(t, "https://upstream/last.jpg", out.Content.LastFrameURL)
}

// 聚合（编排）模型：回显与筛选都必须用调用方提交的那个名字。
//
// Distribute 会把聚合模型展开成生成段模型并写进 Properties.OriginModelName（计费与
// 日志按它走），对外那个名字只留在 PrivateData.Aggregate.PublicModel 里。两处症状都
// 不报错：回显泄露一个调用方从没提交过的内部流水线模型名，以及按提交名筛列表一条都
// 筛不到、静默返回空集。
func TestBuildTaskEchoesPublicAggregateModel(t *testing.T) {
	task := arkTask(model.TaskStatusSuccess, &model.ArkV3Properties{}, "")
	task.Properties.OriginModelName = "doubao-seedance-2-0-260128" // 展开后的生成段
	task.PrivateData.Aggregate = &model.TaskAggregateInfo{PublicModel: "seedance-2-0-1080p"}

	out := BuildTask(task)
	require.Equal(t, "seedance-2-0-1080p", out.Model,
		"回显必须是调用方提交的聚合模型名，不能泄露展开后的生成段模型")

	// 按提交名筛得到它，按内部生成段模型名筛不到 —— 后者是内部实现，不该成为对外的筛选维度。
	byPublic := FilterAndPage([]*model.Task{task}, ListFilter{PageNum: 1, PageSize: 20, Model: "seedance-2-0-1080p"})
	require.Equal(t, 1, byPublic.Total, "按提交的聚合模型名筛应当命中，否则列表静默返回空集")
	byInternal := FilterAndPage([]*model.Task{task}, ListFilter{PageNum: 1, PageSize: 20, Model: "doubao-seedance-2-0-260128"})
	require.Equal(t, 0, byInternal.Total)

	// 非聚合任务不受影响。
	plain := arkTask(model.TaskStatusSuccess, &model.ArkV3Properties{}, "")
	require.Equal(t, "doubao-seedance-2-0-260128", BuildTask(plain).Model)
}

// output.duration 是部分兼容网关的写法。只读顶层的话换个上游就静默取到 0。
func TestBuildTaskReadsDurationFromOutputToo(t *testing.T) {
	task := arkTask(model.TaskStatusSuccess, &model.ArkV3Properties{},
		`{"status":"succeeded","output":{"duration":12}}`)
	require.Equal(t, 12, BuildTask(task).Duration)
}

// 状态映射。cancelled 与 expired 都落在内部的 FAILURE 上，靠两个**有据可依**的
// 判据区分开：取消看我们自己打的标记，过期看上游回执里明写的 status。
func TestTaskStatusMapping(t *testing.T) {
	cases := []struct {
		name     string
		status   model.TaskStatus
		data     string
		cancel   bool
		expected string
	}{
		{"未开始", model.TaskStatusNotStart, "", false, StatusQueued},
		{"已提交", model.TaskStatusSubmitted, "", false, StatusQueued},
		{"排队中", model.TaskStatusQueued, "", false, StatusQueued},
		{"未知也算还没跑", model.TaskStatusUnknown, "", false, StatusQueued},
		{"进行中", model.TaskStatusInProgress, "", false, StatusRunning},
		{"成功", model.TaskStatusSuccess, "", false, StatusSucceeded},
		{"失败", model.TaskStatusFailure, "", false, StatusFailed},
		{"被取消", model.TaskStatusFailure, "", true, StatusCancelled},
		{"上游判过期", model.TaskStatusFailure, `{"status":"expired"}`, false, StatusExpired},
		{"取消优先于过期", model.TaskStatusFailure, `{"status":"expired"}`, true, StatusCancelled},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := arkTask(tc.status, &model.ArkV3Properties{}, tc.data)
			task.PrivateData.Cancelled = tc.cancel
			require.Equal(t, tc.expected, BuildTask(task).Status)
		})
	}
}

// 终态必须带上错误原因，否则调用方只看到一个没有解释的 failed。
func TestBuildTaskCarriesFailureReason(t *testing.T) {
	task := arkTask(model.TaskStatusFailure, &model.ArkV3Properties{},
		`{"status":"failed","error":{"code":"SensitiveContentDetected","message":"内容审核未通过"}}`)
	out := BuildTask(task)
	require.Equal(t, "SensitiveContentDetected", out.Error.Code)
	require.Equal(t, "内容审核未通过", out.Error.Message)

	// 上游没给原因时退回我们自己记的失败原因。
	task = arkTask(model.TaskStatusFailure, &model.ArkV3Properties{}, "")
	task.FailReason = "上游超时"
	require.Equal(t, "上游超时", BuildTask(task).Error.Message)
}

// Data 不是 Ark 形态（别的渠道）也不能炸，回落到快照即可。
func TestBuildTaskToleratesForeignData(t *testing.T) {
	task := arkTask(model.TaskStatusSuccess, &model.ArkV3Properties{Resolution: "480p"},
		`{"task_status":"SUCCEED","some_other_shape":[1,2,3]}`)
	out := BuildTask(task)
	require.Equal(t, StatusSucceeded, out.Status)
	require.Equal(t, "480p", out.Resolution)
}

func TestIsArkTask(t *testing.T) {
	require.False(t, IsArkTask(nil))
	require.False(t, IsArkTask(&model.Task{}), "非方舟端点提交的任务在本协议下就是不存在")
	require.True(t, IsArkTask(arkTask(model.TaskStatusSuccess, &model.ArkV3Properties{}, "")))
	require.False(t, IsArkTask(arkTask(model.TaskStatusSuccess, &model.ArkV3Properties{Deleted: true}, "")),
		"软删过的任务与从没提交过的任务不可区分，这正是官方 DELETE 之后的可观测行为")
}

// DELETE 的状态表必须与官方逐行对齐 —— 这是切过来的调用方最容易撞上的一处。
func TestDeleteAction(t *testing.T) {
	cases := []struct {
		name       string
		status     model.TaskStatus
		cancelled  bool
		wantAction string
		wantErr    bool
	}{
		{"排队中可取消", model.TaskStatusQueued, false, ActionCancel, false},
		{"未开始可取消", model.TaskStatusNotStart, false, ActionCancel, false},
		{"已提交可取消", model.TaskStatusSubmitted, false, ActionCancel, false},
		{"运行中不支持", model.TaskStatusInProgress, false, "", true},
		{"成功可删记录", model.TaskStatusSuccess, false, ActionDelete, false},
		{"失败可删记录", model.TaskStatusFailure, false, ActionDelete, false},
		{"已取消不支持", model.TaskStatusFailure, true, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task := arkTask(tc.status, &model.ArkV3Properties{}, "")
			task.PrivateData.Cancelled = tc.cancelled
			action, apiErr := DeleteAction(task)
			if tc.wantErr {
				require.NotNil(t, apiErr)
				require.Equal(t, CodeNotSupported, apiErr.Code)
				return
			}
			require.Nil(t, apiErr)
			require.Equal(t, tc.wantAction, action)
		})
	}
}

func TestValidateListFilter(t *testing.T) {
	t.Run("缺省值", func(t *testing.T) {
		f := ListFilter{}
		require.Nil(t, ValidateListFilter(&f))
		require.Equal(t, 1, f.PageNum)
		require.Equal(t, DefaultPageSize, f.PageSize)
	})

	t.Run("越界报错而不是夹取", func(t *testing.T) {
		// 官方把 page_size 的范围写死在 1–500。悄悄夹到 500 的话，调用方按自己传的
		// 1000 去算页数，会漏掉一半任务且毫无察觉。
		f := ListFilter{PageSize: 1000}
		require.NotNil(t, ValidateListFilter(&f))
		f = ListFilter{PageNum: 0xFFFF}
		require.NotNil(t, ValidateListFilter(&f))
		f = ListFilter{PageNum: -1}
		require.NotNil(t, ValidateListFilter(&f))
	})

	t.Run("枚举", func(t *testing.T) {
		f := ListFilter{Status: "pending"}
		require.NotNil(t, ValidateListFilter(&f))
		f = ListFilter{ServiceTier: "turbo"}
		require.NotNil(t, ValidateListFilter(&f))
		for _, s := range []string{StatusQueued, StatusRunning, StatusSucceeded, StatusFailed, StatusCancelled, StatusExpired} {
			f = ListFilter{Status: s}
			require.Nil(t, ValidateListFilter(&f), "官方状态词 %s 应当被接受", s)
		}
	})
}

// InternalStatusesFor 必须是 taskStatusToArk 的反函数 —— 两处各写一份必然漂移，
// 而漂移的表现是列表少一批任务，不报错。
func TestInternalStatusesForIsTheInverseOfTheForwardMapping(t *testing.T) {
	all := []model.TaskStatus{
		model.TaskStatusNotStart, model.TaskStatusSubmitted, model.TaskStatusQueued,
		model.TaskStatusInProgress, model.TaskStatusFailure, model.TaskStatusSuccess,
		model.TaskStatusUnknown,
	}
	for _, arkStatus := range []string{StatusQueued, StatusRunning, StatusSucceeded, StatusFailed} {
		statuses, _ := InternalStatusesFor(arkStatus)
		got := map[model.TaskStatus]bool{}
		for _, s := range statuses {
			got[s] = true
		}
		for _, s := range all {
			task := arkTask(s, &model.ArkV3Properties{}, "")
			forward := taskStatusToArk(task, &upstreamEcho{})
			require.Equal(t, forward == arkStatus, got[s],
				"内部状态 %s 正查得到 %s，反查 %s 的结果却对不上", s, forward, arkStatus)
		}
	}

	// cancelled / expired 在 SQL 侧只能收窄到 FAILURE，真正的区分要在 Go 里做。
	for _, s := range []string{StatusCancelled, StatusExpired} {
		statuses, needsGoFilter := InternalStatusesFor(s)
		require.Equal(t, []model.TaskStatus{model.TaskStatusFailure}, statuses)
		require.True(t, needsGoFilter)
	}

	statuses, needsGoFilter := InternalStatusesFor("")
	require.Nil(t, statuses, "不限状态")
	require.False(t, needsGoFilter)
}

func TestFilterAndPage(t *testing.T) {
	tasks := []*model.Task{
		arkTask(model.TaskStatusSuccess, &model.ArkV3Properties{ServiceTier: "flex"}, ""),
		arkTask(model.TaskStatusFailure, &model.ArkV3Properties{}, ""),
		arkTask(model.TaskStatusQueued, &model.ArkV3Properties{}, ""),
		// 非方舟任务：绝不能混进列表。
		{TaskID: "task_other", Status: model.TaskStatusSuccess},
	}
	tasks[1].PrivateData.Cancelled = true

	t.Run("只列方舟任务", func(t *testing.T) {
		out := FilterAndPage(tasks, ListFilter{PageNum: 1, PageSize: 20})
		require.Equal(t, 3, out.Total)
	})

	t.Run("按状态筛能区分 cancelled 与 failed", func(t *testing.T) {
		out := FilterAndPage(tasks, ListFilter{PageNum: 1, PageSize: 20, Status: StatusCancelled})
		require.Equal(t, 1, out.Total)
		out = FilterAndPage(tasks, ListFilter{PageNum: 1, PageSize: 20, Status: StatusFailed})
		require.Equal(t, 0, out.Total, "被取消的任务不该出现在 failed 里")
	})

	t.Run("service_tier 空值等同 default", func(t *testing.T) {
		// 按字面比对的话 filter.service_tier=default 会一条都筛不到 —— 绝大多数请求
		// 根本不带 service_tier。
		out := FilterAndPage(tasks, ListFilter{PageNum: 1, PageSize: 20, ServiceTier: ServiceTierDefault})
		require.Equal(t, 2, out.Total)
		out = FilterAndPage(tasks, ListFilter{PageNum: 1, PageSize: 20, ServiceTier: ServiceTierFlex})
		require.Equal(t, 1, out.Total)
	})

	t.Run("切页", func(t *testing.T) {
		out := FilterAndPage(tasks, ListFilter{PageNum: 2, PageSize: 2})
		require.Equal(t, 3, out.Total)
		require.Len(t, out.Items, 1)
		// 越界的页给空集，不能 panic。
		out = FilterAndPage(tasks, ListFilter{PageNum: 99, PageSize: 2})
		require.Len(t, out.Items, 0)
	})
}

func TestCreateSuccessBody(t *testing.T) {
	out, err := CreateSuccessBody("sha256-x")([]byte(`{"id":"task_abc","object":"video","status":"queued"}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"task_abc","safety_identifier":"sha256-x"}`, string(out))

	// 没给 safety_identifier 时不能凭空加一个空串字段。
	out, err = CreateSuccessBody("")([]byte(`{"id":"task_abc"}`))
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"task_abc"}`, string(out))

	// 拿不到任务 ID 就报错，不能回一个没有 id 的成功响应。
	_, err = CreateSuccessBody("")([]byte(`{"object":"video"}`))
	require.Error(t, err)
}

func TestErrorEnvelope(t *testing.T) {
	body := BuildErrorBody("req-1", 401, "", "", "invalid api key")
	require.JSONEq(t, `{"error":{"code":"AuthenticationError","message":"invalid api key","param":"","type":"Unauthorized","request_id":"req-1"}}`, string(body))
	require.True(t, IsErrorEnvelope(body))

	// 本仓自己的 OpenAI 风格错误**不是**方舟信封，必须被改写而不是原样放出去。
	require.False(t, IsErrorEnvelope([]byte(`{"error":{"message":"boom","type":"invalid_request_error"}}`)))
	require.False(t, IsErrorEnvelope([]byte(`{"code":"x","message":"y"}`)))
	require.False(t, IsErrorEnvelope(nil))
}

// output_format 与其它回显字段同一优先级：上游回执 > 提交快照。
func TestBuildTaskEchoesOutputFormat(t *testing.T) {
	props := &model.ArkV3Properties{OutputFormat: "mov"}
	require.Equal(t, "mov", BuildTask(arkTask(model.TaskStatusSubmitted, props, `{"id":"cgt-1"}`)).OutputFormat)
	require.Equal(t, "mp4", BuildTask(arkTask(model.TaskStatusSuccess, props,
		`{"id":"cgt-1","status":"succeeded","output_format":"mp4"}`)).OutputFormat)
}
