package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 异步任务的结算 / 退款发生在轮询阶段,没有请求上下文,IP 只能来自提交时冻结在
// TaskPrivateData.ClientIp 的值。

const frozenClientIp = "198.51.100.9"

func TestRefundTaskQuota_RecordsFrozenClientIp(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 21, 21, 21
	const initQuota, preConsumed = 10000, 3000

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-ip-refund", 5000)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.ClientIp = frozenClientIp

	RefundTaskQuota(ctx, task, "task failed: upstream error")

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, model.LogTypeRefund, log.Type)
	assert.Equal(t, frozenClientIp, log.Ip)
}

func TestRecalculateTaskQuota_RecordsFrozenClientIp(t *testing.T) {
	truncate(t)
	ctx := context.Background()

	const userID, tokenID, channelID = 22, 22, 22
	const initQuota, preConsumed = 10000, 2000
	const actualQuota = 3000 // 实际额 ≠ 预扣额,必然写一条差额日志

	seedUser(t, userID, initQuota)
	seedToken(t, tokenID, userID, "sk-ip-recalc", 5000)
	seedChannel(t, channelID)

	task := makeTask(userID, channelID, preConsumed, tokenID, BillingSourceWallet, 0)
	task.PrivateData.ClientIp = frozenClientIp

	RecalculateTaskQuota(ctx, task, actualQuota, "adaptor adjustment")

	log := getLastLog(t)
	require.NotNil(t, log)
	assert.Equal(t, actualQuota-preConsumed, log.Quota)
	assert.Equal(t, frozenClientIp, log.Ip)
}
