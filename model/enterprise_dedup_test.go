package model

import (
	"errors"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func enterpriseFields(usccHash string) EnterpriseFields {
	return EnterpriseFields{
		CompanyName:   "测试科技有限公司",
		UsccEnc:       "enc-uscc",
		UsccHash:      usccHash,
		LegalRepName:  "张三",
		LegalRepIdEnc: "enc-legal-id",
	}
}

func submitEnterprise(t *testing.T, userId int, usccHash string) *UserEnterprise {
	t.Helper()
	ent, err := UpsertEnterpriseWithImages(userId, enterpriseFields(usccHash), "lic", "front", "back")
	require.NoError(t, err)
	return ent
}

// 同一 USCC 可在多个账号分别认证：两个账号都提交成功、双双批准，
// users.enterprise_status 都为 2。
func TestEnterprise_SameUSCCMultipleAccounts(t *testing.T) {
	truncateTables(t)
	createKYCUser(t, 6201, "ent_a")
	createKYCUser(t, 6202, "ent_b")
	const usccHash = "hash-uscc-shared"

	eA := submitEnterprise(t, 6201, usccHash)
	eB := submitEnterprise(t, 6202, usccHash)
	require.NoError(t, ApproveEnterprise(eA.Id, 1))
	require.NoError(t, ApproveEnterprise(eB.Id, 1))

	for _, userId := range []int{6201, 6202} {
		var u User
		require.NoError(t, DB.First(&u, userId).Error)
		assert.Equal(t, EnterpriseStatusApproved, u.EnterpriseStatus)
	}
}

// A 已通过后，B 再提交同一 USCC 仍允许：提交期跨账号去重已移除。
// （旧实现只会在存在其他 approved 同 USCC 记录时命中，本用例专门锁住这条路径。）
func TestEnterprise_SubmitAfterOtherApproved(t *testing.T) {
	truncateTables(t)
	createKYCUser(t, 6220, "ent_pre_a")
	createKYCUser(t, 6221, "ent_pre_b")
	const usccHash = "hash-uscc-after-approved"

	eA := submitEnterprise(t, 6220, usccHash)
	require.NoError(t, ApproveEnterprise(eA.Id, 1))

	eB := submitEnterprise(t, 6221, usccHash)
	require.NoError(t, ApproveEnterprise(eB.Id, 1))

	for _, userId := range []int{6220, 6221} {
		var u User
		require.NoError(t, DB.First(&u, userId).Error)
		assert.Equal(t, EnterpriseStatusApproved, u.EnterpriseStatus)
	}
}

// 同账号重复提交：submit_count 累加，超出 EnterpriseMaxSubmitCount 返回
// ErrEnterpriseSubmitLimitExceeded。
func TestEnterprise_SubmitLimit(t *testing.T) {
	truncateTables(t)
	createKYCUser(t, 6210, "ent_lim")
	const usccHash = "hash-uscc-limit"

	ent := submitEnterprise(t, 6210, usccHash)
	for i := 1; i < common.EnterpriseMaxSubmitCount; i++ {
		ent = submitEnterprise(t, 6210, usccHash)
	}
	assert.Equal(t, common.EnterpriseMaxSubmitCount, ent.SubmitCount)

	_, err := UpsertEnterpriseWithImages(6210, enterpriseFields(usccHash), "lic", "front", "back")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrEnterpriseSubmitLimitExceeded))
}
