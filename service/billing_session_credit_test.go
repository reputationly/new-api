package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/require"
)

func seedCreditLineUser(t *testing.T, id int, quota int, limit int64) {
	t.Helper()
	seedUser(t, id, quota)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", id).
		Update("credit_limit", limit).Error)
}

func userQuotaAndCreditUsed(t *testing.T, id int) (quota int, creditUsed int64) {
	t.Helper()
	var u model.User
	require.NoError(t, model.DB.Select("quota", "credit_used").Where("id = ?", id).First(&u).Error)
	return u.Quota, u.CreditUsed
}

// 生产复现（2026-09-24，BATCH_UPDATE_ENABLED=true）：同一授信用户的两个请求在同一个
// 批量刷新周期内结算。结算补扣进了队列，结转读 DB 读到的是没扣补扣的旧 quota：
// 第一个只结转了一部分，第二个读到 0、结转 0。漏掉的透支永久停在负 quota 里，
// credit_used 少记，日志把它算成现金消耗。
func TestWalletSettle_BatchModeCreditUserNoLeak(t *testing.T) {
	truncate(t)
	const uid = 9801
	seedCreditLineUser(t, uid, 0, 100000)

	common.BatchUpdateEnabled = true
	t.Cleanup(func() { common.BatchUpdateEnabled = false })

	newSession := func() (*BillingSession, *relaycommon.RelayInfo) {
		f := &WalletFunding{userId: uid}
		require.NoError(t, f.PreConsume(1000))
		info := &relaycommon.RelayInfo{UserId: uid, IsPlayground: true}
		return &BillingSession{relayInfo: info, funding: f, preConsumedQuota: 1000}, info
	}
	s1, info1 := newSession()
	s2, info2 := newSession()

	require.NoError(t, s1.Settle(4000))
	require.NoError(t, s2.Settle(4000))

	quota, used := userQuotaAndCreditUsed(t, uid)
	require.Equal(t, 0, quota, "透支必须全部结转，不得停在负 quota 里")
	require.Equal(t, int64(8000), used)
	require.Equal(t, 4000, info1.CreditConsumed)
	require.Equal(t, 4000, info2.CreditConsumed, "后结算的请求不得因读到旧 quota 而记 0")
}

// Redis 开启时授信判断读缓存的 CreditLimit 字段，未命中才回源 DB，且不回填缓存。
//
// 缓存由生产方 GetUserCache 真实回填，而不是手写 hash：字段名写错的话读不到、
// 会静默落到 DB，下面「缓存说有授信、DB 说没有」这一步就会失败。
func TestHasCreditLine_RedisReadsSingleFieldWithoutBackfill(t *testing.T) {
	truncate(t)
	const creditUid, plainUid = 9803, 9804
	seedCreditLineUser(t, creditUid, 0, 100000)
	// seedUser 的 aff_code 恒为空串且有唯一约束，同一测试建第二个用户须另给
	require.NoError(t, model.DB.Create(&model.User{Id: plainUid, Username: "plain_user",
		Quota: 10000, Status: common.UserStatusEnabled, AffCode: "plain9804"}).Error)
	mr := withRedis(t)

	common.BatchUpdateEnabled = true
	t.Cleanup(func() { common.BatchUpdateEnabled = false })

	// 未命中：回源 DB 判断，且不得把整行回填进缓存
	require.NoError(t, model.DecreaseUserQuota(plainUid, 100, false))
	time.Sleep(100 * time.Millisecond)
	require.False(t, mr.Exists(fmt.Sprintf("user:%d", plainUid)), "授信判断不得触发整行回填")
	quota, _ := userQuotaAndCreditUsed(t, plainUid)
	require.Equal(t, 10000, quota, "普通用户仍进批量队列")

	// 命中：以缓存字段为准。先让生产方回填缓存，再只改 DB，证明读的是缓存那一格
	_, err := model.GetUserCache(creditUid)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return mr.Exists(fmt.Sprintf("user:%d", creditUid)) },
		time.Second, 10*time.Millisecond)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", creditUid).
		Update("credit_limit", 0).Error)

	require.NoError(t, model.DecreaseUserQuota(creditUid, 500, false))
	quota, _ = userQuotaAndCreditUsed(t, creditUid)
	require.Equal(t, -500, quota, "缓存显示有授信，应直写 DB")
}

// 未开授信的用户保持批量写：直写只为授信用户开，普通用户的写库量不变。
func TestWalletSettle_BatchModeNonCreditUserStillQueued(t *testing.T) {
	truncate(t)
	const uid = 9802
	seedUser(t, uid, 10000)

	common.BatchUpdateEnabled = true
	t.Cleanup(func() { common.BatchUpdateEnabled = false })

	f := &WalletFunding{userId: uid}
	require.NoError(t, f.PreConsume(1000))
	require.NoError(t, f.Settle(3000))

	quota, _ := userQuotaAndCreditUsed(t, uid)
	require.Equal(t, 9000, quota, "补扣应进批量队列，DB 只反映直写的预扣")
}
