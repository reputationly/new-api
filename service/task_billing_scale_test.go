package service

import (
	"context"
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/stretchr/testify/require"
)

// 异步任务的「按 token 差额结算」走的是倍率路径：quota = tokens × 倍率 × 100
// （倍率是 ¥/百万 tokens），与 QuotaPerUnit、汇率都无关。
func TestRecalculateTaskQuotaByTokensUsesRatioPathHundred(t *testing.T) {
	truncate(t)
	ratio_setting.InitRatioSettings()

	const uid = 9201
	seedUser(t, uid, 100_000_000)
	seedToken(t, uid, uid, "sk-scale-task", 100_000_000)
	seedChannel(t, uid)

	const modelName = "gpt-4o"
	modelRatio, ok, _ := ratio_setting.GetModelRatio(modelName)
	require.True(t, ok, "前置：%s 必须有默认倍率", modelName)

	const totalTokens = 1000
	const groupRatio = 1.0

	original := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = original })

	settle := func(qpu float64) int {
		common.QuotaPerUnit = qpu
		task := makeVideoTask(t, uid, uid, 100_000, nil) // 无 VideoBilling → 走 token 重算
		task.Properties.OriginModelName = modelName
		task.PrivateData.TokenId = uid
		task.PrivateData.BillingSource = BillingSourceWallet
		task.PrivateData.BillingContext = &model.TaskBillingContext{
			GroupRatio:      groupRatio,
			OriginModelName: modelName,
		}
		// makeVideoTask 内部已经入库
		require.True(t, RecalculateTaskQuotaByTokens(context.Background(), task, totalTokens))
		return task.Quota
	}

	want := int(math.Round(totalTokens * modelRatio * groupRatio * common.QuotaPerMillionTokens))
	require.Equal(t, want, settle(7_300_000), "新单位下的期望值")
	require.Equal(t, want, settle(730_000_000), "任务重算不得随 QuotaPerUnit 变化")
}
