package service

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

// 「0 元 + 有积分」的用户每笔请求都能成功（积分混扣），但钱包一分没动。
// 旧实现拿整笔 consume 去减钱包余额，于是每笔都命中 UserQuota-consume < threshold，
// 反复收到「额度即将用尽」邮件。这里锁住修正后的判定。
func TestShouldSendWalletQuotaWarn(t *testing.T) {
	const threshold = 1_000_000

	cases := []struct {
		name           string
		userQuota      int
		consume        int
		pointsConsumed int
		want           bool
	}{
		{"0 元钱包整笔由积分承担", 0, 1_000_000, 1_000_000, false},
		{"积分取整后略超总额", 0, 1_000_000, 1_006_850, false},
		{"0 元钱包部分积分部分现金", 0, 1_000_000, 400_000, true},
		{"余额充足纯钱包消费", 10_000_000, 1_000_000, 0, false},
		{"余额低于阈值纯钱包消费", 0, 1_000_000, 0, true},
		{"退款方向不提醒", 0, -500, 0, false},
		{"无积分时与旧实现一致", 1_500_000, 1_000_000, 0, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, shouldSendWalletQuotaWarn(c.userQuota, c.consume, c.pointsConsumed, threshold))
		})
	}
}

// 两条静音途径：运营关全局开关、用户自己退订。任一命中都不发。
func TestQuotaRemindMuted(t *testing.T) {
	original := common.QuotaRemindEnabled
	t.Cleanup(func() { common.QuotaRemindEnabled = original })

	common.QuotaRemindEnabled = true
	require.False(t, quotaRemindMuted(dto.UserSetting{}), "默认应照常提醒")
	require.True(t, quotaRemindMuted(dto.UserSetting{QuotaRemindDisabled: true}), "用户退订后不再提醒")

	common.QuotaRemindEnabled = false
	require.True(t, quotaRemindMuted(dto.UserSetting{}), "全局开关关闭后一律不提醒")
}
