package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

func seedTopUpUser(t *testing.T, id int) *User {
	t.Helper()
	user := &User{Id: id, Username: "topup_manual_user", Quota: 0, Status: common.UserStatusEnabled, Group: "default"}
	require.NoError(t, DB.Create(user).Error)
	return user
}

func seedPendingTopUp(t *testing.T, userId int, tradeNo, provider string, amount int64, money float64) {
	t.Helper()
	topUp := &TopUp{
		UserId:          userId,
		Amount:          amount,
		Money:           money,
		TradeNo:         tradeNo,
		PaymentMethod:   provider,
		PaymentProvider: provider,
		CreateTime:      common.GetTimestamp(),
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, DB.Create(topUp).Error)
}

func userQuota(t *testing.T, userId int) int {
	t.Helper()
	var user User
	require.NoError(t, DB.Where("id = ?", userId).First(&user).Error)
	return user.Quota
}

// 管理员手动补单的到账额度必须与对应渠道的自动回调路径一致。
//
// Amount 的单位随 provider 而异：直连渠道（支付宝 / 微信）与 Creem、对公转账写的就是
// quota；epay / waffo / waffo_pancake 写的是美元数量；Stripe 用 Money（美元）。
// 改造前只区分了 Stripe，其余一律 Amount × QuotaPerUnit，直连订单补单会多发 QPU 倍。
func TestManualCompleteTopUpMatchesProviderSemantics(t *testing.T) {
	originalQPU := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = originalQPU })
	common.QuotaPerUnit = 7300000000

	const (
		quotaOrderAmount  = int64(common.QuotaPerYuan) // 直连渠道：Amount 已经是 quota（¥1）
		dollarOrderAmount = int64(1)         // 外币渠道：Amount 是美元数量（$1）
	)

	cases := []struct {
		name     string
		provider string
		amount   int64
		money    float64
		want     int
	}{
		{"支付宝直连", PaymentProviderAlipayDirect, quotaOrderAmount, 1, int(common.QuotaPerYuan)},
		{"微信直连", PaymentProviderWxpayDirect, quotaOrderAmount, 1, int(common.QuotaPerYuan)},
		{"Creem", PaymentProviderCreem, quotaOrderAmount, 1, int(common.QuotaPerYuan)},
		{"对公转账补单", PaymentProviderBankTransfer, quotaOrderAmount, 1, int(common.QuotaPerYuan)},
		{"易支付", PaymentProviderEpay, dollarOrderAmount, 7.3, int(common.QuotaPerUnit)},
		{"Waffo", PaymentProviderWaffo, dollarOrderAmount, 7.3, int(common.QuotaPerUnit)},
		{"Waffo Pancake", PaymentProviderWaffoPancake, dollarOrderAmount, 7.3, int(common.QuotaPerUnit)},
		{"Stripe", PaymentProviderStripe, quotaOrderAmount, 1, int(common.QuotaPerUnit)},
	}

	for i, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			truncateTables(t)
			userId := 9400 + i
			seedTopUpUser(t, userId)
			tradeNo := "manual-" + c.provider
			seedPendingTopUp(t, userId, tradeNo, c.provider, c.amount, c.money)

			require.NoError(t, ManualCompleteTopUp(tradeNo, "127.0.0.1"))
			require.Equal(t, c.want, userQuota(t, userId))

			// 幂等：已成功的订单再补一次不再加钱
			require.NoError(t, ManualCompleteTopUp(tradeNo, "127.0.0.1"))
			require.Equal(t, c.want, userQuota(t, userId))
		})
	}
}
