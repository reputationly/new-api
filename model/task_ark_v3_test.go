package model

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/require"
)

// 方舟兼容层复用 MiniMax v2 那套 api_protocol 镜像机制（BeforeSave 从 Properties
// 镜像到真列）。这组用例锁住的是镜像本身与软删的清列 —— 两者任一失效，方舟列表
// 接口都会静默返回错误的集合。
func insertArkV3TestTask(t *testing.T, userId int, taskId string, status TaskStatus, modelName string, ark bool) *Task {
	t.Helper()
	task := &Task{
		TaskID:     taskId,
		Platform:   constant.TaskPlatform("1010"),
		UserId:     userId,
		Status:     status,
		Properties: Properties{OriginModelName: modelName},
	}
	if ark {
		task.Properties.ArkV3 = &ArkV3Properties{Resolution: "720p", Duration: 5}
	}
	require.NoError(t, task.Insert())
	return task
}

func TestBeforeSaveMirrorsArkV3Protocol(t *testing.T) {
	const userId = 918101

	ark := insertArkV3TestTask(t, userId, "ark_mirror_1", TaskStatusSuccess, "doubao-seedance-2-0-260128", true)
	require.Equal(t, TaskAPIProtocolArkV3, ark.APIProtocol)

	other := insertArkV3TestTask(t, userId, "ark_mirror_2", TaskStatusSuccess, "doubao-seedance-2-0-260128", false)
	require.Empty(t, other.APIProtocol, "非方舟端点提交的任务不该被打上协议标记")

	// 落盘后再读一次：镜像要真的进了列，而不只是内存里的字段。
	reloaded, exist, err := GetByTaskId(userId, "ark_mirror_1")
	require.NoError(t, err)
	require.True(t, exist)
	require.Equal(t, TaskAPIProtocolArkV3, reloaded.APIProtocol)

	// 同一个 api_protocol 列被两个兼容层共用，互不串味。
	tasks, total, err := ListTasksByProtocol(userId, TaskAPIProtocolArkV3, nil, 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, tasks, 1)
	require.Equal(t, "ark_mirror_1", tasks[0].TaskID)
}

// 软删必须在 BeforeSave 里清列。只在删除处清列是不够的 —— 快照还在 Properties 里，
// 下一次落盘会被钩子照着 `!= nil` 重新写回去，任务就从方舟列表里「复活」了。
func TestArkV3SoftDeleteKeepsRowUsable(t *testing.T) {
	const userId = 918102
	task := insertArkV3TestTask(t, userId, "ark_soft_del", TaskStatusSuccess, "doubao-seedance-2-0-260128", true)
	require.Equal(t, TaskAPIProtocolArkV3, task.APIProtocol)

	task.Properties.ArkV3.Deleted = true
	require.NoError(t, task.Update())

	reloaded, exist, err := GetByTaskId(userId, "ark_soft_del")
	require.NoError(t, err)
	require.True(t, exist, "软删只是协议侧不可见，行必须还在 —— 产物代理、下载、分享链接都还挂在它身上")
	require.Empty(t, reloaded.APIProtocol)
	require.True(t, reloaded.Properties.ArkV3.Deleted)

	_, total, err := ListTasksByProtocol(userId, TaskAPIProtocolArkV3, nil, 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 0, total)

	// 再落一次盘也不能复活。
	require.NoError(t, reloaded.Update())
	again, exist, err := GetByTaskId(userId, "ark_soft_del")
	require.NoError(t, err)
	require.True(t, exist)
	require.Empty(t, again.APIProtocol, "软删后的任务不能在下一次落盘时被钩子写回协议列")
}

// PublicModelName 是各协议兼容层回显与筛选模型名的共用规则。
//
// 聚合模型被 Distribute 展开后，Properties.OriginModelName 存的是内部生成段模型；
// 读错了不会报错，只会让调用方看到一个它从没提交过的名字，以及按提交名筛列表时静默
// 返回空集。
func TestPublicModelName(t *testing.T) {
	aggregate := &Task{Properties: Properties{OriginModelName: "doubao-seedance-2-0-260128"}}
	aggregate.PrivateData.Aggregate = &TaskAggregateInfo{PublicModel: "seedance-2-0-1080p"}
	require.Equal(t, "seedance-2-0-1080p", aggregate.PublicModelName())

	plain := &Task{Properties: Properties{OriginModelName: "doubao-seedance-2-0-260128"}}
	require.Equal(t, "doubao-seedance-2-0-260128", plain.PublicModelName(), "非聚合任务行为不变")

	// 聚合记录存在但没有公开名（历史数据 / 只带增强段的记录）时要回落，不能回空串。
	noPublic := &Task{Properties: Properties{OriginModelName: "m"}}
	noPublic.PrivateData.Aggregate = &TaskAggregateInfo{}
	require.Equal(t, "m", noPublic.PublicModelName())

	upstreamOnly := &Task{Properties: Properties{UpstreamModelName: "u"}}
	require.Equal(t, "u", upstreamOnly.PublicModelName())
}

// 取消的任务终态是 FAILURE，对外要渲染成 cancelled。判定必须先于失败分支，
// 否则调用方只看到 failed，分不清是生成失败还是自己取消的。
func TestToOpenAIVideoRendersCancelled(t *testing.T) {
	task := &Task{TaskID: "task_x", Status: TaskStatusFailure, FailReason: "用户取消"}
	task.PrivateData.Cancelled = true

	video := task.ToOpenAIVideo()
	require.Equal(t, "cancelled", video.Status)
	require.NotNil(t, video.Error)
	require.Equal(t, "用户取消", video.Error.Message)

	// 没被取消的失败任务不受影响。
	normal := &Task{TaskID: "task_y", Status: TaskStatusFailure, FailReason: "上游超时"}
	require.Equal(t, "failed", normal.ToOpenAIVideo().Status)
}
