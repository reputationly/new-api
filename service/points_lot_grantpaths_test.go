package service

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// AC4：实名发放（本人 + 邀请人）的「有效期为 0 时行为不变」。
// R4 指出这条路径此前没有测试——它恰好是两条独立流水（kyc: / invite:）里最容易
// 接错口径的一条：邀请奖励的流水记在**邀请人**账上，幂等键却用**被邀请人** id。

func snapshotPointsSetting(t *testing.T) *operation_setting.PointsSetting {
	t.Helper()
	ps := operation_setting.GetPointsSetting()
	snap := *ps
	t.Cleanup(func() { *ps = snap })
	return ps
}

func seedKycPathUser(t *testing.T, id int, inviterId int) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.User{
		Id: id, Username: fmt.Sprintf("kycp_%d", id), Role: 1, Status: 1,
		Group: "default", AffCode: fmt.Sprintf("ky%06d", id),
		InviterId: inviterId,
	}).Error)
	t.Cleanup(func() {
		model.DB.Exec("DELETE FROM points_lots WHERE user_id IN ?", []int{id, inviterId})
		model.DB.Exec("DELETE FROM fund_entries WHERE user_id IN ?", []int{id, inviterId})
		model.DB.Unscoped().Where("id IN ?", []int{id, inviterId}).Delete(&model.User{})
	})
}

func pointsBalance(t *testing.T, id int) int64 {
	t.Helper()
	var b int64
	require.NoError(t, model.DB.Unscoped().Model(&model.User{}).Where("id = ?", id).
		Select("points_balance").Scan(&b).Error)
	return b
}

func lotOfSource(t *testing.T, userId int, source string) model.PointsLot {
	t.Helper()
	var lots []model.PointsLot
	require.NoError(t, model.DB.Where("user_id = ? AND source = ?", userId, source).Find(&lots).Error)
	require.Len(t, lots, 1, "来源 %s 应恰好一条批次", source)
	return lots[0]
}

func ledger(t *testing.T, userId int, refType, refId string) model.FundEntry {
	t.Helper()
	var e model.FundEntry
	require.NoError(t, model.DB.Where("user_id = ? AND ref_type = ? AND ref_id = ?", userId, refType, refId).
		First(&e).Error)
	return e
}

func TestPointsLotGrant_KycSelfAndInviter(t *testing.T) {
	ps := snapshotPointsSetting(t)
	ps.Enabled, ps.RequireKyc = true, false
	ps.KycVerifiedPoints, ps.KycInviterPoints = 30, 50
	ps.KycVerifiedExpireDays, ps.KycInviterExpireDays = 0, 0

	const invitee, inviter = 6201, 6202
	seedKycPathUser(t, inviter, 0)
	seedKycPathUser(t, invitee, inviter)

	GrantKycPoints(invitee)

	selfQuota := int64(common.PointsToQuota(30))
	inviterQuota := int64(common.PointsToQuota(50))

	require.EqualValues(t, selfQuota, pointsBalance(t, invitee), "本人奖励到账")
	require.EqualValues(t, inviterQuota, pointsBalance(t, inviter), "邀请人奖励到账")

	// 批次：都永不过期
	selfLot := lotOfSource(t, invitee, model.FundSourceKyc)
	require.EqualValues(t, selfQuota, selfLot.Amount)
	require.Zero(t, selfLot.ExpiresAt)

	inviteLot := lotOfSource(t, inviter, model.FundSourceInvite)
	require.EqualValues(t, inviterQuota, inviteLot.Amount)
	require.Zero(t, inviteLot.ExpiresAt)

	// 流水：本人那条记在自己账上，幂等键 kyc:<被实名用户>
	selfEntry := ledger(t, invitee, model.FundRefUser, fmt.Sprintf("kyc:%d", invitee))
	require.Equal(t, model.FundAccountPoints, selfEntry.Account)
	require.Equal(t, model.FundKindGift, selfEntry.Kind)
	require.Equal(t, model.FundSourceKyc, selfEntry.Source)
	require.EqualValues(t, selfQuota, selfEntry.QuotaDelta)

	// 邀请人那条记在**邀请人**账上，幂等键仍用被邀请人 id（触发事件是后者的实名）
	inviterEntry := ledger(t, inviter, model.FundRefUser, fmt.Sprintf("invite:%d", invitee))
	require.Equal(t, model.FundAccountPoints, inviterEntry.Account)
	require.Equal(t, model.FundKindGift, inviterEntry.Kind)
	require.Equal(t, model.FundSourceInvite, inviterEntry.Source)
	require.EqualValues(t, inviterQuota, inviterEntry.QuotaDelta)

	// 占位已置位，重复调用不得再发
	GrantKycPoints(invitee)
	require.EqualValues(t, selfQuota, pointsBalance(t, invitee))
}

// 发放失败时仍只记日志：占位不回滚（否则 KYC 审核会被积分发放拖垮，而占位一旦置位
// 重试也不会再发，用户会永久失去这笔积分——两害相权下选择「记日志 + 人工补」）。
//
// 制造失败的手段：换到一个**故意不建 fund_entries 表**的独立库上发放。原先这里靠
// 「预占 kyc:<id> 幂等键」制造失败，但 id 复用修复后撞键会自动换成带时刻的键、
// 发放反而成功，那个前提不再成立（见 TestPointsLotGrant_KycSurvivesReusedUserId）。
func TestPointsLotGrant_KycFailureOnlyLogsAndKeepsPlaceholder(t *testing.T) {
	ps := snapshotPointsSetting(t)
	ps.Enabled, ps.RequireKyc = true, false
	ps.KycVerifiedPoints, ps.KycInviterPoints = 30, 0
	ps.KycVerifiedExpireDays = 0

	const invitee = 6211
	seedKycPathUser(t, invitee, 0)

	// 独立库：users / points_lots 有，fund_entries 故意不建 → 记流水必然失败
	dsn := "file:kyc_fail_case?mode=memory&cache=shared"
	broken, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, broken.AutoMigrate(&model.User{}, &model.PointsLot{}))
	require.NoError(t, broken.Create(&model.User{
		Id: invitee, Username: "kyc_fail_1", Role: 1, Status: 1,
		Group: "default", AffCode: "kyf00001",
	}).Error)

	origDB, origLog := model.DB, model.LOG_DB
	model.DB, model.LOG_DB = broken, broken
	t.Cleanup(func() { model.DB, model.LOG_DB = origDB, origLog })

	require.NotPanics(t, func() { GrantKycPoints(invitee) }, "发放失败不得 panic")

	// 占位保持置位（不回滚）
	var granted bool
	require.NoError(t, broken.Model(&model.User{}).Where("id = ?", invitee).
		Select("kyc_points_granted").Scan(&granted).Error)
	require.True(t, granted, "占位不回滚——重试也不会再发，靠人工补")

	// 整笔回滚：余额与批次都没留下
	var balance int64
	require.NoError(t, broken.Model(&model.User{}).Where("id = ?", invitee).
		Select("points_balance").Scan(&balance).Error)
	require.Zero(t, balance)
	var lotCount int64
	require.NoError(t, broken.Model(&model.PointsLot{}).Where("user_id = ?", invitee).Count(&lotCount).Error)
	require.Zero(t, lotCount, "事务回滚后不得残留批次")
}

func TestPointsLotGrant_KycSurvivesReusedUserId(t *testing.T) {
	ps := snapshotPointsSetting(t)
	ps.Enabled, ps.RequireKyc = true, false
	ps.KycVerifiedPoints, ps.KycInviterPoints = 30, 50
	ps.KycVerifiedExpireDays, ps.KycInviterExpireDays = 0, 0

	const invitee, inviter = 6301, 6302
	seedKycPathUser(t, inviter, 0)
	seedKycPathUser(t, invitee, inviter)

	// 模拟「上一个同 id 用户」留下的两条流水
	for _, ref := range []string{fmt.Sprintf("kyc:%d", invitee), fmt.Sprintf("invite:%d", invitee)} {
		_, err := model.InsertFundEntry(&model.FundEntry{
			UserId: invitee, Account: model.FundAccountPoints, Kind: model.FundKindGift,
			QuotaDelta: 1, CashFen: 0, Source: model.FundSourceKyc,
			RefType: model.FundRefUser, RefId: ref,
		})
		require.NoError(t, err)
	}

	GrantKycPoints(invitee)

	require.EqualValues(t, int64(common.PointsToQuota(30)), pointsBalance(t, invitee),
		"id 被复用时本人奖励仍须到账")
	require.EqualValues(t, int64(common.PointsToQuota(50)), pointsBalance(t, inviter),
		"邀请人奖励仍须到账")

	// 新流水用带时刻的键
	var refreshed []model.FundEntry
	require.NoError(t, model.DB.Where("ref_type = ? AND ref_id LIKE ?", model.FundRefUser, "%#%").
		Order("id").Find(&refreshed).Error)
	require.GreaterOrEqual(t, len(refreshed), 2)
}

func TestPointsUserRefId_KeepsPlainRefWhenFree(t *testing.T) {
	ref := model.PointsUserRefId("kyc", 6399, 1795000000, nil)
	require.Equal(t, "kyc:6399", ref, "没有撞键时必须保留原格式（报表按它归集）")
}
