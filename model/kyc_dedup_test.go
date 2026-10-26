package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func createKYCUser(t *testing.T, id int, username string) {
	t.Helper()
	require.NoError(t, DB.Create(&User{
		Id:       id,
		Username: username,
		Role:     1,
		Status:   1,
		AffCode:  username,
	}).Error)
}

func submitKYC(t *testing.T, userId int, idNumber string) *UserKYC {
	t.Helper()
	enc, err := common.EncryptIDNumber(idNumber)
	require.NoError(t, err)
	hash, err := common.HMACIDNumber(idNumber)
	require.NoError(t, err)
	kyc, err := UpsertKYC(userId, "张三", KYCIdTypeIDCard, enc, hash)
	require.NoError(t, err)
	return kyc
}

// 两个账号都 pending 提交同一证件号 → 先后审批：第一个成功，第二个被拒绝，
// 错误 errors.Is ErrKYCDuplicateID 且消息含占用者用户名；B 行仍 pending，kyc_status=1。
func TestApproveKYC_DuplicateBlocked(t *testing.T) {
	truncateTables(t)
	createKYCUser(t, 6101, "kyc_a")
	createKYCUser(t, 6102, "kyc_b")
	idNum := "110101199001011234"
	kA := submitKYC(t, 6101, idNum)
	kB := submitKYC(t, 6102, idNum)

	require.NoError(t, ApproveKYC(kA.Id, 1))

	err := ApproveKYC(kB.Id, 1)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrKYCDuplicateID))
	assert.Contains(t, err.Error(), "kyc_a")

	var b UserKYC
	require.NoError(t, DB.First(&b, kB.Id).Error)
	assert.Equal(t, KYCStatusPending, b.Status)
	var ub User
	require.NoError(t, DB.First(&ub, 6102).Error)
	assert.Equal(t, KYCStatusPending, ub.KycStatus)
}

// 拒绝 → 重新提交（C2 累加）→ 审批通过。
func TestKYC_RejectResubmitApprove(t *testing.T) {
	truncateTables(t)
	createKYCUser(t, 6110, "kyc_re")
	idNum := "110101199001011235"
	k := submitKYC(t, 6110, idNum)
	require.NoError(t, RejectKYC(k.Id, 1, "照片模糊"))

	k2 := submitKYC(t, 6110, idNum)
	require.NoError(t, ApproveKYC(k2.Id, 1))
	var u User
	require.NoError(t, DB.First(&u, 6110).Error)
	assert.Equal(t, KYCStatusApproved, u.KycStatus)
}

// A 通过后管理员 ResetKYC（硬删）释放证件号 → B 的同证件 pending 可批准。
func TestKYC_ResetReleasesID(t *testing.T) {
	truncateTables(t)
	createKYCUser(t, 6120, "kyc_ra")
	createKYCUser(t, 6121, "kyc_rb")
	idNum := "110101199001011236"
	kA := submitKYC(t, 6120, idNum)
	kB := submitKYC(t, 6121, idNum)

	require.NoError(t, ApproveKYC(kA.Id, 1))
	require.NoError(t, ResetKYC(kA.Id, 1))
	require.NoError(t, ApproveKYC(kB.Id, 1))
}

// A 的 approved 行被软删（模拟账号删除 User.Delete）后仍占用证件号：
// 新账号提交同证件被拒，已 pending 的 B 也无法批准。
func TestKYC_SoftDeletedApprovedStillOccupies(t *testing.T) {
	truncateTables(t)
	createKYCUser(t, 6130, "kyc_sa")
	createKYCUser(t, 6131, "kyc_sb")
	createKYCUser(t, 6132, "kyc_sc")
	idNum := "110101199001011237"
	kA := submitKYC(t, 6130, idNum)
	kB := submitKYC(t, 6131, idNum)
	require.NoError(t, ApproveKYC(kA.Id, 1))

	// 模拟账号删除：软删 user_kycs 行
	require.NoError(t, DB.Delete(&UserKYC{}, kA.Id).Error)

	enc, err := common.EncryptIDNumber(idNum)
	require.NoError(t, err)
	hash, err := common.HMACIDNumber(idNum)
	require.NoError(t, err)
	_, err = UpsertKYC(6132, "李四", KYCIdTypeIDCard, enc, hash)
	assert.True(t, errors.Is(err, ErrKYCDuplicateID))

	err = ApproveKYC(kB.Id, 1)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrKYCDuplicateID))
}

// 无冲突正常审批：kyc 行 status=2，users.kyc_status=2。
func TestKYC_ApproveNoConflict(t *testing.T) {
	truncateTables(t)
	createKYCUser(t, 6140, "kyc_ok")
	k := submitKYC(t, 6140, "110101199001011238")
	require.NoError(t, ApproveKYC(k.Id, 1))

	var got UserKYC
	require.NoError(t, DB.First(&got, k.Id).Error)
	assert.Equal(t, KYCStatusApproved, got.Status)
	var u User
	require.NoError(t, DB.First(&u, 6140).Error)
	assert.Equal(t, KYCStatusApproved, u.KycStatus)
}

// 一次性回填迁移：小写 x 的历史行被重写为归一化后的 enc+hash；
// 已归一化的行不动；标记写入后第二次调用是 no-op。
func TestKYC_IdNumberNormalizeBackfill(t *testing.T) {
	truncateTables(t)
	createKYCUser(t, 6150, "kyc_n1")
	createKYCUser(t, 6151, "kyc_n2")
	lower := "11010119900101123x"
	upper := "11010119900101123X"

	encLower, err := common.EncryptIDNumber(lower)
	require.NoError(t, err)
	hashLower, err := common.HMACIDNumber(lower)
	require.NoError(t, err)
	rowLower := &UserKYC{
		UserId: 6150, RealName: "张三", IdType: KYCIdTypeIDCard,
		IdNumberEnc: encLower, IdNumberHash: hashLower,
		Status: KYCStatusPending, SubmitCount: 1,
	}
	require.NoError(t, DB.Create(rowLower).Error)

	encUpper, err := common.EncryptIDNumber(upper)
	require.NoError(t, err)
	hashUpper, err := common.HMACIDNumber(upper)
	require.NoError(t, err)
	rowUpper := &UserKYC{
		UserId: 6151, RealName: "李四", IdType: KYCIdTypeIDCard,
		IdNumberEnc: encUpper, IdNumberHash: hashUpper,
		Status: KYCStatusPending, SubmitCount: 1,
	}
	require.NoError(t, DB.Create(rowUpper).Error)

	require.NoError(t, migrateKYCIdNumberNormalize())

	var got UserKYC
	require.NoError(t, DB.First(&got, rowLower.Id).Error)
	plain, err := common.DecryptIDNumber(got.IdNumberEnc)
	require.NoError(t, err)
	assert.Equal(t, upper, plain)
	assert.Equal(t, hashUpper, got.IdNumberHash)

	var gotNorm UserKYC
	require.NoError(t, DB.First(&gotNorm, rowUpper.Id).Error)
	assert.Equal(t, encUpper, gotNorm.IdNumberEnc)
	assert.Equal(t, hashUpper, gotNorm.IdNumberHash)

	var marker Option
	require.NoError(t, DB.Where(Option{Key: "kyc_id_number_normalized"}).First(&marker).Error)

	// 第二次调用命中标记直接返回，不改任何行
	require.NoError(t, migrateKYCIdNumberNormalize())
	var again UserKYC
	require.NoError(t, DB.First(&again, rowLower.Id).Error)
	assert.Equal(t, got.IdNumberEnc, again.IdNumberEnc)
	assert.Equal(t, hashUpper, again.IdNumberHash)
}

func TestNormalizeIDNumber(t *testing.T) {
	assert.Equal(t, "11010119900101123X", common.NormalizeIDNumber(" 11010119900101123x "))
}

// 已注销占用列表 + 释放：软删 approved 行只出现在专用列表里，释放后证件号可被新账号重新实名。
func TestKYC_OccupyingListAndRelease(t *testing.T) {
	truncateTables(t)
	createKYCUser(t, 6160, "kyc_occ_a")
	createKYCUser(t, 6161, "kyc_occ_b")
	idNum := "110101199001011240"
	kA := submitKYC(t, 6160, idNum)
	require.NoError(t, ApproveKYC(kA.Id, 1))

	// 活跃 approved 记录不算占用，不出现在释放列表
	rows, total, err := GetKYCReleaseList("", 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, rows)

	// 模拟用户自助注销：user_kycs 与 users 都软删
	require.NoError(t, DB.Delete(&UserKYC{}, kA.Id).Error)
	require.NoError(t, DB.Delete(&User{}, 6160).Error)

	rows, total, err = GetKYCReleaseList("", 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, kA.Id, rows[0].Id)
	assert.Equal(t, "kyc_occ_a", rows[0].Username)

	// 占用期间新账号无法实名
	enc, err := common.EncryptIDNumber(idNum)
	require.NoError(t, err)
	hash, err := common.HMACIDNumber(idNum)
	require.NoError(t, err)
	_, err = UpsertKYC(6161, "李四", KYCIdTypeIDCard, enc, hash)
	assert.True(t, errors.Is(err, ErrKYCDuplicateID))

	// 释放后证件号回到可实名状态
	occupyingUserId, err := ReleaseOccupiedKYC(kA.Id)
	require.NoError(t, err)
	assert.Equal(t, 6160, occupyingUserId)

	rows, total, err = GetKYCReleaseList("", 1, 20)
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)
	assert.Empty(t, rows)

	kB := submitKYC(t, 6161, idNum)
	require.NoError(t, ApproveKYC(kB.Id, 1))
	var ub User
	require.NoError(t, DB.First(&ub, 6161).Error)
	assert.Equal(t, KYCStatusApproved, ub.KycStatus)
}

// 活跃记录不能走释放接口（必须用 ResetKYC），避免「记录被硬删、用户状态还停在已认证」。
func TestKYC_ReleaseRejectsActiveRecord(t *testing.T) {
	truncateTables(t)
	createKYCUser(t, 6170, "kyc_rel_active")
	k := submitKYC(t, 6170, "110101199001011241")
	require.NoError(t, ApproveKYC(k.Id, 1))

	_, err := ReleaseOccupiedKYC(k.Id)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "未注销")
}
