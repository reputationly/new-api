package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

// 套餐赠品的单位往返：TopupPackage.GrantPoints 是**积分数**，而 IncreaseUserPoints
// 收的是 quota unit（points_balance 列的单位），二者相差 QuotaPerPoint ≈ 685 倍。
//
// 此前这里直传 GrantPoints，等于把积分数当 quota unit 加：配置「赠送 500 积分」
// 实发 500 quota unit，用户账户页按 floor 展示就是 0 积分——表现为「赠品完全没发」，
// 而不是「发少了」，所以一直没被发现。
//
// 本用例钉住「配多少、到账多少」的精确往返，换算漏掉就会失败。

func TestGrantTopupPackageBonus_PointsRoundTrip(t *testing.T) {
	truncateTables(t)
	enablePointsSetting(t)

	const grantPoints = 500
	require.NoError(t, DB.Create(&User{Id: 701, Username: "pk_701", Role: 1, Status: 1}).Error)
	pkg := &TopupPackage{Title: "入门包", PriceAmount: 100, GrantPoints: grantPoints, Enabled: true}
	require.NoError(t, DB.Create(pkg).Error)

	topUp := &TopUp{
		UserId:    701,
		Amount:    68493,
		Money:     100,
		TradeNo:   "PKG-BONUS-001",
		PackageId: pkg.Id,
		Status:    common.TopUpStatusSuccess,
	}
	require.NoError(t, DB.Create(topUp).Error)

	GrantTopupPackageBonus(topUp)

	p, q := redeemBalances(t, 701)
	require.Equal(t, common.PointsToQuota(grantPoints), p,
		"到账的 quota unit 应是 PointsToQuota(配置积分数)")
	require.Equal(t, grantPoints, common.QuotaToPoints(p),
		"用户可见积分必须等于配置值——配多少发多少的精确往返")
	require.Equal(t, 0, q, "赠品只进积分池，不得动用现金池")
}

// 赠品流水必须与实际余额变化同值，否则每日自洽校验会长期不平。
func TestGrantTopupPackageBonus_FundEntryMatchesBalance(t *testing.T) {
	truncateTables(t)
	enablePointsSetting(t)

	const grantPoints = 500
	require.NoError(t, DB.Create(&User{Id: 702, Username: "pk_702", Role: 1, Status: 1}).Error)
	pkg := &TopupPackage{Title: "进阶包", PriceAmount: 300, GrantPoints: grantPoints, Enabled: true}
	require.NoError(t, DB.Create(pkg).Error)
	topUp := &TopUp{
		UserId: 702, Amount: 205479, Money: 300,
		TradeNo: "PKG-BONUS-002", PackageId: pkg.Id, Status: common.TopUpStatusSuccess,
	}
	require.NoError(t, DB.Create(topUp).Error)

	GrantTopupPackageBonus(topUp)

	var entry FundEntry
	require.NoError(t, DB.Where("ref_type = ? AND ref_id = ?",
		FundRefTopUp, "bonus:PKG-BONUS-002").First(&entry).Error)

	p, _ := redeemBalances(t, 702)
	require.Equal(t, int64(p), entry.QuotaDelta, "流水金额必须等于实际余额变化")
	require.Equal(t, int64(0), entry.CashFen,
		"赠品是市场成本，不计营收——充值收入已由充值流水记过，再记一次就是重复确认")
	require.Equal(t, FundAccountPoints, entry.Account)
	require.Equal(t, FundKindGift, entry.Kind)

	// 重复调用：流水与**余额**都必须幂等。
	//
	// 上一版这里只断言了流水条数，于是「流水 1 条、积分却加了两次」的情况能完全蒙混过去
	// ——流水侧的唯一索引给了假安全感，真正会出事的是余额那一侧。
	before := p
	GrantTopupPackageBonus(topUp)

	var n int64
	require.NoError(t, DB.Model(&FundEntry{}).
		Where("ref_type = ? AND ref_id = ?", FundRefTopUp, "bonus:PKG-BONUS-002").
		Count(&n).Error)
	require.Equal(t, int64(1), n, "同一订单的赠品流水只能有一条")

	after, _ := redeemBalances(t, 702)
	require.Equal(t, before, after, "重复调用不得重复发放积分")
}
