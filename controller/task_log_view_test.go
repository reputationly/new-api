package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// advancedParent 造一条已推进到超分段的聚合父任务。整体进度与整体成片都属于超分阶段,
// 日志里这一行却代表生成段,两者必须分开看。
func advancedParent(stage1URL string) *model.Task {
	t := &model.Task{
		TaskID:   "task_parent",
		Action:   "t2v",
		Status:   model.TaskStatusInProgress,
		Progress: "62%",
	}
	t.PrivateData.Aggregate = &model.TaskAggregateInfo{
		PublicModel:     "h3-2k",
		Stage:           2,
		Stage2TaskID:    "task_sub",
		UpscaleModel:    "seedvr2",
		Stage1ResultURL: stage1URL,
	}
	return t
}

func TestAdvancedPastStage1Predicate(t *testing.T) {
	var nilAgg *model.TaskAggregateInfo
	require.False(t, nilAgg.AdvancedPastStage1(), "没有聚合信息的普通任务")
	require.False(t, (&model.TaskAggregateInfo{Stage: 1}).AdvancedPastStage1(), "还在生成段")
	require.False(t, (&model.TaskAggregateInfo{Stage: 2}).AdvancedPastStage1(), "没有子任务 id 不算推进")
	require.True(t, (&model.TaskAggregateInfo{Stage: 2, Stage2TaskID: "task_sub"}).AdvancedPastStage1())
}

// 推进中的父任务:日志里这一行显示生成段已完成,下载生成段成品;整体的失败原因不得泄漏进来。
func TestLogViewShowsStage1ForAdvancedParentInProgress(t *testing.T) {
	task := advancedParent("https://cdn.example/stage1.mp4")
	task.FailReason = "overall failed"

	d := taskToLogDto(task)

	require.Equal(t, "SUCCESS", d.Status, "生成段已经完成")
	require.Equal(t, "100%", d.Progress, "不能再显示整体的 62%")
	require.Equal(t, "https://cdn.example/stage1.mp4", d.ResultURL, "下载的是生成段成品")
	require.Empty(t, d.FailReason, "整体的失败原因不属于生成段")
}

// 整体任务已经结束(成片是超分后的)的父任务,日志里这一行仍然只代表生成段。
func TestLogViewShowsStage1ForFinishedAdvancedParent(t *testing.T) {
	task := advancedParent("https://cdn.example/stage1.mp4")
	task.Status = model.TaskStatusSuccess
	task.Progress = "100%"
	task.PrivateData.ResultURL = "https://cdn.example/final-upscaled.mp4"

	d := taskToLogDto(task)

	require.Equal(t, "https://cdn.example/stage1.mp4", d.ResultURL,
		"整体成片不属于生成段那一行,它在超分子任务那一行上")
}

// 生成段成品没能落盘:这一行不提供下载,也不能把别的地址或失败原因顶上来。
func TestLogViewWithoutStage1RefHasNoDownload(t *testing.T) {
	task := advancedParent("")
	task.FailReason = "boom"
	task.PrivateData.ResultURL = "https://cdn.example/final-upscaled.mp4"

	d := taskToLogDto(task)

	require.Empty(t, d.ResultURL, "没有生成段成品就不提供下载")
	require.Empty(t, d.FailReason)
}

// 尚未推进的生成段:保持原样,进度仍是它自己的。
func TestLogViewLeavesNotYetAdvancedParentAlone(t *testing.T) {
	task := advancedParent("")
	task.PrivateData.Aggregate.Stage = 1
	task.PrivateData.Aggregate.Stage2TaskID = ""

	d := taskToLogDto(task)

	require.Equal(t, string(model.TaskStatusInProgress), d.Status)
	require.Equal(t, "62%", d.Progress)
}

// 普通任务:完全不受影响。
func TestLogViewLeavesPlainTaskAlone(t *testing.T) {
	task := &model.Task{TaskID: "task_plain", Status: model.TaskStatusSuccess, Progress: "100%"}
	task.PrivateData.ResultURL = "https://cdn.example/plain.mp4"

	d := taskToLogDto(task)

	require.Equal(t, "https://cdn.example/plain.mp4", d.ResultURL)
	require.Equal(t, "100%", d.Progress)
}

// 日志下载:推进过的父任务下载生成段成品。
func TestLogDownloadUsesStage1ForAdvancedParent(t *testing.T) {
	task := advancedParent("https://cdn.example/stage1.mp4")
	task.PrivateData.ResultURL = "https://cdn.example/final-upscaled.mp4"

	body := callLogDownload(t, task)

	require.Equal(t, true, body["success"])
	data := body["data"].(map[string]any)
	require.Equal(t, "https://cdn.example/stage1.mp4", data["url"])
}

// 没有生成段成品时,下载直接报错,而不是退回去下载整体成片。
func TestLogDownloadRefusesAdvancedParentWithoutStage1(t *testing.T) {
	task := advancedParent("")
	task.PrivateData.ResultURL = "https://cdn.example/final-upscaled.mp4"

	body := callLogDownload(t, task)

	require.Equal(t, false, body["success"], "不得静默退回到整体成片")
}

// 普通任务的下载保持原样。
func TestLogDownloadKeepsFinalForPlainTask(t *testing.T) {
	task := &model.Task{TaskID: "task_plain", Status: model.TaskStatusSuccess}
	task.PrivateData.ResultURL = "https://cdn.example/plain.mp4"

	body := callLogDownload(t, task)

	require.Equal(t, true, body["success"])
	require.Equal(t, "https://cdn.example/plain.mp4", body["data"].(map[string]any)["url"])
}

// callLogDownload 走真正的日志下载处理函数,解析它的 JSON 响应。
func callLogDownload(t *testing.T, task *model.Task) map[string]any {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/task/self/x/download", nil)

	respondTaskDownloadURL(c, task, true)

	var body map[string]any
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &body))
	return body
}
