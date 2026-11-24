package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// AC4：每一条发放路径都要有测试证明「有效期为 0 时行为不变」——余额变化、流水字段
// 与改造前一致，且多出一条 expires_at=0 的批次。
//
// 一审指出只覆盖了注册、管理员 add/gift/override，签到、兑换码、套餐赠品、实名
// （本人 + 邀请人）、OAuth 注册这几条都没测。它们各自有独立的流水口径（source /
// ref_type / ref_id 都不同），任何一条接错都会在报表里表现为「某类赠送消失」。

// pointsGrantPathUser 建一个干净用户（aff_code 有唯一索引，必须给值）。
func pointsGrantPathUser(t *testing.T, id int) {
	t.Helper()
	require.NoError(t, DB.Create(&User{
		Id: id, Username: "gp_" + t.Name(), Role: 1, Status: 1,
		Group: "default", AffCode: "gp" + t.Name(),
	}).Error)
}

func enablePointsForGrantPaths(t *testing.T) *operation_setting.PointsSetting {
	t.Helper()
	ps := operation_setting.GetPointsSetting()
	snap := *ps
	ps.Enabled = true
	ps.RequireKyc = false
	t.Cleanup(func() { *ps = snap })
	return ps
}

func onlyLotOfSource(t *testing.T, userId int, source string) PointsLot {
	t.Helper()
	var lots []PointsLot
	require.NoError(t, DB.Where("user_id = ? AND source = ?", userId, source).Find(&lots).Error)
	require.Len(t, lots, 1, "来源 %s 应恰好一条批次", source)
	return lots[0]
}

func ledgerOf(t *testing.T, userId int, refType, refId string) FundEntry {
	t.Helper()
	var e FundEntry
	require.NoError(t, DB.Where("user_id = ? AND ref_type = ? AND ref_id = ?", userId, refType, refId).
		First(&e).Error)
	return e
}

// ---------------------------------------------------------------------------
// 签到
// ---------------------------------------------------------------------------

func TestPointsLotGrant_CheckinPath(t *testing.T) {
	truncateTables(t)
	enablePointsForGrantPaths(t)

	cs := operation_setting.GetCheckinSetting()
	snap := *cs
	cs.Enabled, cs.RewardType = true, CheckinRewardPoints
	cs.MinPoints, cs.MaxPoints = 10, 10
	cs.PointsExpireWeeks = 0
	t.Cleanup(func() { *cs = snap })

	const id = 5101
	pointsGrantPathUser(t, id)

	checkin, err := UserCheckin(id)
	require.NoError(t, err)
	require.Equal(t, CheckinRewardPoints, checkin.RewardType)
	want := int64(common.PointsToQuota(10))
	require.EqualValues(t, want, checkin.QuotaAwarded)
	require.Zero(t, checkin.PointsExpiresAt, "有效期为 0 → 永不过期")

	// 余额
	var balance int64
	require.NoError(t, DB.Unscoped().Model(&User{}).Where("id = ?", id).
		Select("points_balance").Scan(&balance).Error)
	require.EqualValues(t, want, balance)

	// 批次：expires_at=0
	lot := onlyLotOfSource(t, id, FundSourceCheckin)
	require.EqualValues(t, want, lot.Amount)
	require.Zero(t, lot.ExpiresAt)

	// 流水与改造前一致：gift / checkin / checkin / userId:date
	entry := ledgerOf(t, id, FundRefCheckin, CheckinRefId(id, checkin.CheckinDate))
	require.Equal(t, FundAccountPoints, entry.Account)
	require.Equal(t, FundKindGift, entry.Kind)
	require.Equal(t, FundSourceCheckin, entry.Source)
	require.EqualValues(t, want, entry.QuotaDelta)
	require.Zero(t, entry.CashFen)
}

// ---------------------------------------------------------------------------
// 兑换码
// ---------------------------------------------------------------------------

func TestPointsLotGrant_RedemptionPath(t *testing.T) {
	truncateTables(t)
	enablePointsForGrantPaths(t)

	const id = 5102
	pointsGrantPathUser(t, id)

	code := &Redemption{
		Name: "积分码", Key: "gp-redeem-1", Quota: 68493,
		RewardType: RedemptionRewardPoints, Status: common.RedemptionCodeStatusEnabled,
		CreatedTime: common.GetTimestamp(),
	}
	require.NoError(t, code.Insert())

	quota, _, err := Redeem(code.Key, id)
	require.NoError(t, err)
	require.Equal(t, 68493, quota)

	var balance int64
	require.NoError(t, DB.Unscoped().Model(&User{}).Where("id = ?", id).
		Select("points_balance").Scan(&balance).Error)
	require.EqualValues(t, 68493, balance)

	lot := onlyLotOfSource(t, id, FundSourceRedemption)
	require.EqualValues(t, 68493, lot.Amount)
	require.Zero(t, lot.ExpiresAt, "码本身未设有效期 → 永不过期")

	entry := ledgerOf(t, id, FundRefRedemption, itoa(code.Id))
	require.Equal(t, FundAccountPoints, entry.Account)
	require.Equal(t, FundKindGift, entry.Kind)
	require.Equal(t, FundSourceRedemption, entry.Source)
	require.EqualValues(t, 68493, entry.QuotaDelta)
}

// ---------------------------------------------------------------------------
// 充值套餐赠品
// ---------------------------------------------------------------------------

func TestPointsLotGrant_PackageBonusPath(t *testing.T) {
	truncateTables(t)
	enablePointsForGrantPaths(t)

	const id = 5103
	pointsGrantPathUser(t, id)

	pkg := &TopupPackage{
		Title: "套餐", PriceAmount: 100, Currency: "CNY",
		GrantPoints: 500, Enabled: true,
	}
	require.NoError(t, pkg.Insert())

	topUp := &TopUp{UserId: id, TradeNo: "gp-bonus-1", PackageId: pkg.Id, Status: common.TopUpStatusSuccess}
	GrantTopupPackageBonus(topUp)

	want := int64(common.PointsToQuota(500))
	var balance int64
	require.NoError(t, DB.Unscoped().Model(&User{}).Where("id = ?", id).
		Select("points_balance").Scan(&balance).Error)
	require.EqualValues(t, want, balance)

	lot := onlyLotOfSource(t, id, FundSourcePackageBonus)
	require.EqualValues(t, want, lot.Amount)
	require.Zero(t, lot.ExpiresAt)

	entry := ledgerOf(t, id, FundRefTopUp, "bonus:"+topUp.TradeNo)
	require.Equal(t, FundAccountPoints, entry.Account)
	require.Equal(t, FundKindGift, entry.Kind)
	require.Equal(t, FundSourcePackageBonus, entry.Source)
	require.EqualValues(t, want, entry.QuotaDelta)
}

// ---------------------------------------------------------------------------
// OAuth 注册（InsertWithTx + FinalizeOAuthUserCreation）
// ---------------------------------------------------------------------------

func TestPointsLotGrant_OAuthRegisterPath(t *testing.T) {
	truncateTables(t)
	ps := enablePointsForGrantPaths(t)

	origQuota := common.QuotaForNewUser
	common.QuotaForNewUser = 68493
	t.Cleanup(func() { common.QuotaForNewUser = origQuota })
	ps.NewUserPoints = 20
	ps.NewUserExpireDays = 0

	u := &User{Username: "oauth_reg_1", Password: "pass12345", DisplayName: "t"}
	require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
		return u.InsertWithTx(tx, 0)
	}))
	u.FinalizeOAuthUserCreation(0)

	want := int64(68493) + int64(common.PointsToQuota(20))
	var balance int64
	require.NoError(t, DB.Unscoped().Model(&User{}).Where("id = ?", u.Id).
		Select("points_balance").Scan(&balance).Error)
	require.EqualValues(t, want, balance, "QuotaForNewUser 与 NewUserPoints 两笔都要到账")

	// 两条流水与改造前同值、口径独立
	quotaEntry := ledgerOf(t, u.Id, FundRefUser, "register-quota:"+itoa(u.Id))
	require.Equal(t, FundAccountPoints, quotaEntry.Account)
	require.Equal(t, FundKindGift, quotaEntry.Kind)
	require.Equal(t, FundSourceRegister, quotaEntry.Source)
	require.EqualValues(t, 68493, quotaEntry.QuotaDelta)

	pointsEntry := ledgerOf(t, u.Id, FundRefUser, "register-points:"+itoa(u.Id))
	require.EqualValues(t, common.PointsToQuota(20), pointsEntry.QuotaDelta,
		"赠分那条不得包含注册礼额度")

	// 两条批次，都永不过期
	var lots []PointsLot
	require.NoError(t, DB.Where("user_id = ? AND source = ?", u.Id, FundSourceRegister).
		Order("id").Find(&lots).Error)
	require.Len(t, lots, 2)
	for _, l := range lots {
		require.Zero(t, l.ExpiresAt, "有效期 0 → 永不过期")
	}
	require.EqualValues(t, want, sumActiveRemaining(listLots(t, u.Id)))
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	n := v
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
