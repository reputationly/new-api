package service

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

// seedGroupRatios 铺设三层倍率配置并在用例后恢复。
func seedGroupRatios(t *testing.T, groupRatio, groupGroupRatio, groupModelRatio string) {
	t.Helper()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(groupRatio))
	require.NoError(t, ratio_setting.UpdateGroupGroupRatioByJSONString(groupGroupRatio))
	require.NoError(t, ratio_setting.UpdateGroupModelRatioByJSONString(groupModelRatio))
	t.Cleanup(func() {
		_ = ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`)
		_ = ratio_setting.UpdateGroupGroupRatioByJSONString(`{}`)
		_ = ratio_setting.UpdateGroupModelRatioByJSONString(`{}`)
	})
}

func taskWithBillingContext(group, modelName string, frozenRatio float64) *model.Task {
	task := &model.Task{
		Group:      group,
		Properties: model.Properties{OriginModelName: modelName},
	}
	if frozenRatio >= 0 {
		task.PrivateData.BillingContext = &model.TaskBillingContext{
			GroupRatio:      frozenRatio,
			OriginModelName: modelName,
		}
	}
	return task
}

// TestTaskGroupRatio_PrefersFrozen 是本次改造最大风险点的护栏。
//
// 任务结算发生在提交后几百秒。若结算时**重新解析**倍率，管理员在这期间上下架一条
// 促销规则，就会出现「预扣按旧规则、结算按新规则」的对不上账；而模型级折扣的改动
// 频率天然高于分组基础倍率，这个窗口只会更常被撞上。
//
// 用例刻意把当前配置改成与冻结值完全不同的数：只要实现退回「重新解析」，结果必然
// 变成 3.0，测试见红。
func TestTaskGroupRatio_PrefersFrozen(t *testing.T) {
	seedGroupRatios(t, `{"premium":3}`, `{}`, `{"premium":{"GLM-5":{"mode":"override","value":9}}}`)

	task := taskWithBillingContext("premium", "GLM-5", 0.35)

	ratio, ok := taskGroupRatio(task)
	require.True(t, ok)
	require.InDelta(t, 0.35, ratio, 1e-9,
		"必须用提交时冻结的倍率；等于 3 说明退回了结算时重新解析")
	require.NotEqual(t, 9.0, ratio, "更不能命中结算时的当前模型规则")
}

// TestTaskGroupRatio_LegacyTaskFallsBack 没有 BillingContext 的老任务无从得知提交时
// 的倍率，只能重新解析。此时模型级折扣也应生效，而不是被静默跳过。
func TestTaskGroupRatio_LegacyTaskFallsBack(t *testing.T) {
	seedGroupRatios(t, `{"premium":2}`, `{}`, `{"premium":{"GLM-5":{"mode":"multiply","value":0.5}}}`)

	ratio, ok := taskGroupRatio(taskWithBillingContext("premium", "GLM-5", -1))
	require.True(t, ok)
	require.InDelta(t, 1.0, ratio, 1e-9, "2 × 0.5：老任务回退路径也要走模型级折扣")
}

// TestTaskGroupRatio_ZeroFrozenFallsBack 老数据可能有 BillingContext 但没写 GroupRatio。
// 零值不能当成「冻结倍率就是 0」（那会让整单免费），必须走回退。
func TestTaskGroupRatio_ZeroFrozenFallsBack(t *testing.T) {
	seedGroupRatios(t, `{"premium":2}`, `{}`, `{}`)

	ratio, ok := taskGroupRatio(taskWithBillingContext("premium", "GLM-5", 0))
	require.True(t, ok)
	require.InDelta(t, 2.0, ratio, 1e-9)
}
