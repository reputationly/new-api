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

// 异步任务的「按 token 差额结算」走的是倍率路径，必须跟着单位乘 scale。
func TestRecalculateTaskQuotaByTokensScalesRatioPath(t *testing.T) {
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

	common.QuotaPerUnit = 500000
	require.Equal(t,
		int(math.Round(totalTokens*modelRatio*groupRatio)),
		settle(500000))

	const qpuNew = 7300000.0
	require.Equal(t,
		int(math.Round(totalTokens*modelRatio*groupRatio*(qpuNew/common.RatioQuotaBase))),
		settle(qpuNew))
}
