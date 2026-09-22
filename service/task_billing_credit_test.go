package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/require"
)

// 异步轮询期的差额补扣必须也结转透支。
//
// 同步路径由 BillingSession.syncCreditConsumed 做，这条差额结算是另一条入口。
// 不补的话：补扣把 quota 打成负数后永久停在那里、credit_used 不增、日志记 0——
// 负余额被算进现金口径，现金账与信用账**各自都能自洽**，每日校验抓不到，
// 而应收被实实在在地低估、授信上限对异步补扣形同虚设。
func TestRecalculate_TopUpChargeSettlesOverdraft(t *testing.T) {
	truncate(t)
	const uid = 9701
	// 授信客户的典型形态：0 预付余额 + 授信额度
	seedUser(t, uid, 0)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", uid).
		Update("credit_limit", 100000).Error)

	task := makeVideoTask(t, uid, uid, 1000, nil) // 非延迟记账
	task.PrivateData.BillingContext.GroupRatio = 1.0
	model.UpdateUserUsedQuotaAndRequestCount(uid, 1000)

	// 提交时预扣 1000 已结转成欠款（模拟 syncCreditConsumed 的结果）
	require.NoError(t, model.DecreaseUserQuota(uid, 1000, true))
	settled, err := model.SettleOverdraftToCredit(uid, 1000)
	require.NoError(t, err)
	require.Equal(t, int64(1000), settled)
	task.PrivateData.CreditConsumed = 1000

	// 轮询期发现实际用量更高，补扣 500
	RecalculateTaskQuota(context.Background(), task, 1500, "测试差额补扣")

	var u model.User
	require.NoError(t, model.DB.Select("quota", "credit_used").
		Where("id = ?", uid).First(&u).Error)
	require.Equal(t, 0, u.Quota, "补扣造成的透支必须结转，不能让 quota 停在负数")
	require.Equal(t, int64(1500), u.CreditUsed, "应收要涵盖补扣部分，否则被低估")
	require.Equal(t, 1500, task.PrivateData.CreditConsumed,
		"任务上的信用实付要同步更新，退款时才能原路冲销")

	// 补扣日志要记这次新增的授信，否则对账在信用侧对不上
	var log model.Log
	require.NoError(t, model.DB.Order("id desc").First(&log).Error)
	require.Equal(t, model.LogTypeConsume, log.Type)
	require.Equal(t, 500, log.Quota)
	require.Equal(t, 500, log.CreditConsumed, "本次补扣中由授信承担的部分")
}

// 混扣任务退款同样要先冲销授信欠款。
//
// 冲销逻辑若只放在纯钱包分支，混扣任务（BillingSource=points_wallet）的退款会绕过它，
// 信用部分被退进钱包而 credit_used 一分不减——与积分被洗成真实余额是同一条套利通道。
func TestTaskAdjustFunding_HybridRefundReversesCredit(t *testing.T) {
	truncate(t)
	const uid = 9702
	seedUser(t, uid, 0)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", uid).
		Updates(map[string]interface{}{"credit_limit": 100000, "credit_used": 800}).Error)

	task := makeVideoTask(t, uid, uid, 1000, nil)
	task.PrivateData.BillingSource = BillingSourceHybrid
	task.PrivateData.PointsConsumed = 200
	task.PrivateData.CreditConsumed = 800 // 混扣任务里由授信承担的部分

	// 全额退款
	require.NoError(t, taskAdjustFunding(task, -1000))

	var u model.User
	require.NoError(t, model.DB.Select("quota", "credit_used").
		Where("id = ?", uid).First(&u).Error)
	require.Equal(t, int64(0), u.CreditUsed,
		"混扣任务的信用部分必须冲销，否则退款把欠款洗成了可用余额")
	require.Equal(t, 0, task.PrivateData.CreditConsumed)
	require.Equal(t, 0, u.Quota,
		"信用冲销掉 800 后仅余 200 走积分原路退，不应有现金进账")
}

// 纯钱包任务的混合退款：信用与现金按拆分各退各的。
func TestTaskAdjustFunding_WalletRefundSplitsCreditAndCash(t *testing.T) {
	truncate(t)
	const uid = 9703
	seedUser(t, uid, 0)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", uid).
		Updates(map[string]interface{}{"credit_limit": 100000, "credit_used": 300}).Error)

	task := makeVideoTask(t, uid, uid, 1000, nil)
	task.PrivateData.CreditConsumed = 300 // 1000 的消费里只有 300 走了授信

	require.NoError(t, taskAdjustFunding(task, -1000))

	var u model.User
	require.NoError(t, model.DB.Select("quota", "credit_used").
		Where("id = ?", uid).First(&u).Error)
	require.Equal(t, int64(0), u.CreditUsed, "先冲欠款")
	require.Equal(t, 700, u.Quota, "其余才退现金")
}
