package model

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

// 对公转账入账流水必须记管理员确认的**实际到账金额**。
//
// 这里踩过一次：ApproveBankTransferOrder 里的 order 是 tx.First 取的审批前快照
// （该处注释已写明「仅读取不可变字段」），而 credited_fen 由条件 Updates(map) 写库、
// GORM 不回填结构体。若流水读 order.CreditedFen，拿到的是 pending 订单的 0，
// 对公转账营收会被整体清零——而且报表上看不出来，只会显示「这个月没有对公收入」。
func TestApproveBankTransferOrder_FundEntryUsesCreditedFen(t *testing.T) {
	truncateTables(t)
	require.NoError(t, DB.Create(&User{Id: 801, Username: "bt_801", Role: 1, Status: 1}).Error)

	const declaredFen = int64(500000) // 用户申报 ¥5000
	const creditedFen = int64(498800) // 实际到账 ¥4988（扣了手续费）
	order, err := CreateBankTransferOrderWithReceipt(801, declaredFen, "测试转账", "enc")
	require.NoError(t, err)

	require.NoError(t, ApproveBankTransferOrder(order.Id, 9001, creditedFen, "BD 确认", "127.0.0.1"))

	var entry FundEntry
	require.NoError(t, DB.Where("ref_type = ? AND ref_id = ?",
		FundRefBankTransfer, order.TradeNo).First(&entry).Error)

	require.Equal(t, creditedFen, entry.CashFen,
		"必须记实际到账金额；记成 0 会让对公转账营收整体消失")
	require.NotEqual(t, int64(0), entry.CashFen, "CashFen>0 才计入营收")
	require.Equal(t, "BD 确认", entry.Remark, "审批备注同样不能读审批前快照")
	require.Equal(t, 9001, entry.OperatorId)
	require.Equal(t, FundKindPrepay, entry.Kind)
	require.Equal(t, FundAccountCash, entry.Account)
}

// 注册礼落点：积分系统启用时进积分池（赠送不得混入现金池，否则「真实入账」虚高），
// 未启用时仍进现金池（那种部署里积分不可用，收敛过去等于把注册礼作废）。
func TestApplyNewUserRegisterGrant_RoutesByPointsSwitch(t *testing.T) {
	origQuota := common.QuotaForNewUser
	common.QuotaForNewUser = 68493
	t.Cleanup(func() { common.QuotaForNewUser = origQuota })

	ps := operation_setting.GetPointsSetting()
	origEnabled := ps.Enabled
	t.Cleanup(func() { ps.Enabled = origEnabled })

	ps.Enabled = true
	u := &User{}
	applyNewUserRegisterGrant(u)
	require.Equal(t, 0, u.Quota, "积分启用时注册礼不得进现金池")
	require.Equal(t, 68493, u.PointsBalance)
	require.Equal(t, FundAccountPoints, newUserRegisterGrantAccount())

	ps.Enabled = false
	u2 := &User{}
	applyNewUserRegisterGrant(u2)
	require.Equal(t, 68493, u2.Quota, "积分未启用时保持发额度，否则注册礼作废")
	require.Equal(t, 0, u2.PointsBalance)
	require.Equal(t, FundAccountCash, newUserRegisterGrantAccount())
}

// 注册礼额度与注册赠分是两路独立配置，同时配置时必须叠加而非互相覆盖。
// applyNewUserRegisterGrant 先于 NewUserPointsGrant 执行，后者若用 `=` 赋值会吞掉前者。
func TestApplyNewUserRegisterGrant_AccumulatesWithPointsGrant(t *testing.T) {
	origQuota := common.QuotaForNewUser
	common.QuotaForNewUser = 1000
	t.Cleanup(func() { common.QuotaForNewUser = origQuota })

	ps := operation_setting.GetPointsSetting()
	origEnabled := ps.Enabled
	ps.Enabled = true
	t.Cleanup(func() { ps.Enabled = origEnabled })

	u := &User{}
	applyNewUserRegisterGrant(u)
	u.PointsBalance += 2000 // 模拟 NewUserPointsGrant 的注册赠分

	require.Equal(t, 3000, u.PointsBalance, "两路注册礼必须叠加，不能互相覆盖")
}

// 注册礼的核心不变量：**两条流水之和必须等于账户余额的实际变化**。
//
// 这里出过一次错：把注册礼额度折进 PointsBalance 之后，流水的第二条仍传 user.PointsBalance
// （已含注册礼额度），于是额度被两条流水各记一次，合计比真实余额多出一个 QuotaForNewUser。
// 上一版测试只覆盖了 applyNewUserRegisterGrant 这个纯函数，看不到调用方的传参错误，
// 所以这里直接走 Insert 端到端断言不变量。
func TestInsert_RegisterGrantLedgerMatchesBalance(t *testing.T) {
	truncateTables(t)

	origQuota := common.QuotaForNewUser
	common.QuotaForNewUser = 68493 // ¥1 的 quota
	t.Cleanup(func() { common.QuotaForNewUser = origQuota })

	ps := operation_setting.GetPointsSetting()
	origEnabled, origNew := ps.Enabled, ps.NewUserPoints
	ps.Enabled, ps.NewUserPoints = true, 10 // 注册再送 10 积分
	t.Cleanup(func() { ps.Enabled, ps.NewUserPoints = origEnabled, origNew })

	u := &User{Username: "reg_ledger_1", Password: "pass12345", DisplayName: "t"}
	require.NoError(t, u.Insert(0))

	var persisted User
	require.NoError(t, DB.Where("id = ?", u.Id).First(&persisted).Error)
	require.Equal(t, 0, persisted.Quota, "积分启用时注册礼不得进现金池")
	require.Greater(t, persisted.PointsBalance, 0)

	var ledgerSum int64
	require.NoError(t, DB.Model(&FundEntry{}).
		Where("user_id = ? AND account = ?", u.Id, FundAccountPoints).
		Select("COALESCE(SUM(quota_delta), 0)").Scan(&ledgerSum).Error)

	require.Equal(t, int64(persisted.PointsBalance), ledgerSum,
		"流水合计必须等于余额实际变化，否则每笔新注册都会让每日自洽校验不平")

	// 两条流水各自的金额也要对：额度一条、赠分一条，互不包含
	var quotaEntry, pointsEntry FundEntry
	require.NoError(t, DB.Where("ref_type = ? AND ref_id = ?",
		FundRefUser, fmt.Sprintf("register-quota:%d", u.Id)).First(&quotaEntry).Error)
	require.NoError(t, DB.Where("ref_type = ? AND ref_id = ?",
		FundRefUser, fmt.Sprintf("register-points:%d", u.Id)).First(&pointsEntry).Error)
	require.Equal(t, int64(common.QuotaForNewUser), quotaEntry.QuotaDelta)
	require.Equal(t, int64(common.PointsToQuota(10)), pointsEntry.QuotaDelta,
		"赠分那条不得包含注册礼额度")
}
