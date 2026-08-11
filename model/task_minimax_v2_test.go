package model

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/require"
)

// api_protocol 是 BeforeSave 从 Properties 镜像出来的真列（手法同 token_id）。
// 这组用例同时锁住镜像本身与依赖它的 SQL 查询——两者任一失效，v2 列表接口都会
// 静默返回错误的集合，而那正是当初必须引入这个列的原因。
func insertMiniMaxV2TestTask(t *testing.T, userId int, taskId string, status TaskStatus, modelName string, v2 bool) *Task {
	t.Helper()
	task := &Task{
		TaskID:   taskId,
		Platform: constant.TaskPlatform("gpustackplus"),
		UserId:   userId,
		Status:   status,
		Properties: Properties{
			OriginModelName: modelName,
		},
	}
	if v2 {
		task.Properties.MiniMaxV2 = &MiniMaxV2Properties{Resolution: "768P", Duration: 6}
	}
	require.NoError(t, task.Insert())
	return task
}

func TestBeforeSaveMirrorsAPIProtocol(t *testing.T) {
	const userId = 918001

	v2 := insertMiniMaxV2TestTask(t, userId, "v2_mirror_1", TaskStatusSuccess, "MiniMax-H3", true)
	require.Equal(t, TaskAPIProtocolMiniMaxV2, v2.APIProtocol)

	other := insertMiniMaxV2TestTask(t, userId, "v2_mirror_2", TaskStatusSuccess, "wan2.2-t2v", false)
	require.Empty(t, other.APIProtocol, "非 v2 提交的任务不该被打上协议标记")

	// 落盘后再读一次：镜像要真的进了列，而不只是内存里的字段。
	reloaded, exist, err := GetByTaskId(userId, "v2_mirror_1")
	require.NoError(t, err)
	require.True(t, exist)
	require.Equal(t, TaskAPIProtocolMiniMaxV2, reloaded.APIProtocol)
}

func TestListTasksByProtocol(t *testing.T) {
	const userId = 918002

	insertMiniMaxV2TestTask(t, userId, "v2_list_1", TaskStatusSuccess, "MiniMax-H3", true)
	insertMiniMaxV2TestTask(t, userId, "v2_list_2", TaskStatusFailure, "MiniMax-H3", true)
	insertMiniMaxV2TestTask(t, userId, "v2_list_3", TaskStatusQueued, "MiniMax-H3", true)
	// 同一用户名下的非 v2 任务：一条都不该出现，也不该占用分页名额。
	for _, id := range []string{"other_1", "other_2", "other_3", "other_4"} {
		insertMiniMaxV2TestTask(t, userId, id, TaskStatusSuccess, "wan2.2-t2v", false)
	}

	tasks, total, err := ListTasksByProtocol(userId, TaskAPIProtocolMiniMaxV2, nil, 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 3, total)
	require.Len(t, tasks, 3)
	// id 倒序 = 最近在前。
	require.Equal(t, "v2_list_3", tasks[0].TaskID)
	require.Equal(t, "v2_list_1", tasks[2].TaskID)

	// 分页：total 是全集的数量，不随页大小变。
	page1, total, err := ListTasksByProtocol(userId, TaskAPIProtocolMiniMaxV2, nil, 0, 2)
	require.NoError(t, err)
	require.EqualValues(t, 3, total)
	require.Len(t, page1, 2)
	page2, _, err := ListTasksByProtocol(userId, TaskAPIProtocolMiniMaxV2, nil, 2, 2)
	require.NoError(t, err)
	require.Len(t, page2, 1)
	require.Equal(t, "v2_list_1", page2[0].TaskID)

	// 越界页：给空集，不报错。
	beyond, total, err := ListTasksByProtocol(userId, TaskAPIProtocolMiniMaxV2, nil, 99, 2)
	require.NoError(t, err)
	require.EqualValues(t, 3, total)
	require.Empty(t, beyond)

	// 状态筛选取交集（官方状态词与内部状态是一对多，换算在调用方）。
	queued, total, err := ListTasksByProtocol(userId, TaskAPIProtocolMiniMaxV2,
		[]TaskStatus{TaskStatusNotStart, TaskStatusSubmitted, TaskStatusQueued, TaskStatusUnknown}, 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, queued, 1)
	require.Equal(t, "v2_list_3", queued[0].TaskID)

	// 其他用户的任务不可见。
	_, total, err = ListTasksByProtocol(userId+1, TaskAPIProtocolMiniMaxV2, nil, 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 0, total)
}

// 软删的三条不变量。这一组针对的是「删除的爆炸半径超出 v2 协议自己的地盘」那类问题——
// 上一轮正是缺了这类断言才让硬删走到评审。
func TestMiniMaxV2SoftDeleteKeepsRowUsable(t *testing.T) {
	const userId = 918004
	task := insertMiniMaxV2TestTask(t, userId, "v2_soft_del", TaskStatusSuccess, "MiniMax-H3", true)
	require.Equal(t, TaskAPIProtocolMiniMaxV2, task.APIProtocol)

	task.Properties.MiniMaxV2.Deleted = true
	require.NoError(t, task.Update())

	// 1. 行还在，状态还是 SUCCESS —— /v1/videos/{id}/content、任务下载、分享链接的
	//    公开解析、remix 原任务、task:<id> 产物引用全都靠这一条查询活着。
	reloaded, exist, err := GetByTaskId(userId, "v2_soft_del")
	require.NoError(t, err)
	require.True(t, exist, "软删不能让 content.url / 分享链接 / 产物引用失效")
	// 显式转型：这个 const 块里只有 TaskStatusNotStart 带类型，其余都是无类型字符串常量。
	require.Equal(t, TaskStatus(TaskStatusSuccess), reloaded.Status)

	// 2. api_protocol 被清空 —— SQL 侧的 v2 列表因此查不到它。
	require.Empty(t, reloaded.APIProtocol)
	_, total, err := ListTasksByProtocol(userId, TaskAPIProtocolMiniMaxV2, nil, 0, 10)
	require.NoError(t, err)
	require.EqualValues(t, 0, total)

	// 3. 再落一次盘不会「复活」：快照还在 Properties 里，BeforeSave 若只看
	//    `MiniMaxV2 != nil` 就会把列写回去，任务又出现在列表里。
	reloaded.Progress = "100%"
	require.NoError(t, reloaded.Update())
	again, _, err := GetByTaskId(userId, "v2_soft_del")
	require.NoError(t, err)
	require.Empty(t, again.APIProtocol, "软删后的任务不能被后续落盘重新打上协议标记")
	require.True(t, again.Properties.MiniMaxV2.Deleted)
}

func TestListAllTasksByProtocolRespectsLimit(t *testing.T) {
	const userId = 918003
	for _, id := range []string{"v2_all_1", "v2_all_2", "v2_all_3"} {
		insertMiniMaxV2TestTask(t, userId, id, TaskStatusSuccess, "MiniMax-H3", true)
	}
	tasks, err := ListAllTasksByProtocol(userId, TaskAPIProtocolMiniMaxV2, nil, 2)
	require.NoError(t, err)
	// 取满 limit 是调用方判定「窗口打满 → 明确报错」的依据，不能悄悄多给或少给。
	require.Len(t, tasks, 2)
	require.Equal(t, "v2_all_3", tasks[0].TaskID)
}
