package service

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 套餐权益在日志里的归因与降级记录（设计文档 §8.4、§10.3）。
//
// 扣费侧早就算出了命中哪条权益、烧了多少点，但此前一个字都没进日志：套餐内的调用
// 只留下 billing_source，降级的那笔在日志里就是一条普通的钱包扣费。用户问「买了
// 套餐怎么还扣钱」时，这里记的就是全部答案。

// 1 算力点 = 100 quota，数字好算
func withComputePointRate(t *testing.T, qpcp float64) {
	t.Helper()
	prev := common.QuotaPerComputePointFunc
	common.QuotaPerComputePointFunc = func() float64 { return qpcp }
	t.Cleanup(func() { common.QuotaPerComputePointFunc = prev })
}

// ---- 降级记录 ----

func TestEntitlementFallback_RecordedWhenCountExhausted(t *testing.T) {
	truncate(t)
	seedBillingUser(t, 90301, 1000000, 0)
	seedMatchableEntitlement(t, 90301, 0, 100000)
	require.NoError(t, model.DB.Model(&model.UserSubscriptionEntitlement{}).
		Where("user_id = ?", 90301).
		Updates(map[string]interface{}{"limit_count": 5, "used_count": 5}).Error)

	info := relayInfoForModel(90301, "gpt-test")
	session, apiErr := NewBillingSession(newTestGinContext(1), info, 1000)
	require.Nil(t, apiErr)
	require.NotEqual(t, BillingSourceEntitlement, session.funding.Source())

	fb := info.EntitlementFallback
	require.NotNil(t, fb, "被套餐覆盖却降级了，必须留下记录")
	require.Equal(t, EntitlementFallbackCountExhausted, fb.Reason)
	require.Equal(t, "e2e", fb.PlanTitle)
	require.Equal(t, int64(5), fb.LimitCount)
}

// 「本次需 N 点、剩余 M 点不足」：所需向上取整（真正要扣的量），剩余向下取整（不虚报）
func TestEntitlementFallback_RecordedWhenPointsInsufficient(t *testing.T) {
	truncate(t)
	withComputePointRate(t, 100)
	seedBillingUser(t, 90302, 1000000, 0)
	seedMatchableEntitlement(t, 90302, 10, 150) // 批次 150 quota = 1.5 点

	info := relayInfoForModel(90302, "gpt-test")
	session, apiErr := NewBillingSession(newTestGinContext(1), info, 1050) // 10.5 点
	require.Nil(t, apiErr)
	require.NotEqual(t, BillingSourceEntitlement, session.funding.Source())

	fb := info.EntitlementFallback
	require.NotNil(t, fb)
	require.Equal(t, EntitlementFallbackPointsInsufficient, fb.Reason)
	require.Equal(t, 11, fb.PointsNeeded)
	require.Equal(t, 1, fb.PointsAvailable)
}

func TestEntitlementFallback_NilWhenEntitlementUsed(t *testing.T) {
	truncate(t)
	seedBillingUser(t, 90303, 1000000, 0)
	seedMatchableEntitlement(t, 90303, 10, 100000)

	info := relayInfoForModel(90303, "gpt-test")
	// 上一轮重试留下的记录不能带进这一轮
	info.EntitlementFallback = &relaycommon.EntitlementFallback{Reason: "stale"}
	session, apiErr := NewBillingSession(newTestGinContext(1), info, 1000)
	require.Nil(t, apiErr)
	require.Equal(t, BillingSourceEntitlement, session.funding.Source())
	require.Nil(t, info.EntitlementFallback)
}

// 模型根本不在套餐里不叫降级——那是正常走余额，标「超额」会误导
func TestEntitlementFallback_NilWhenModelNotCovered(t *testing.T) {
	truncate(t)
	seedBillingUser(t, 90304, 1000000, 0)
	seedMatchableEntitlement(t, 90304, 10, 100000)

	info := relayInfoForModel(90304, "other-model")
	_, apiErr := NewBillingSession(newTestGinContext(1), info, 1000)
	require.Nil(t, apiErr)
	require.Nil(t, info.EntitlementFallback)
}

// ---- 结算后的点数同步 ----

// 预扣同步的是估算值；结算补扣之后日志必须记真正烧掉的点数
func TestEntitlementSettle_SyncsFinalPointsToRelayInfo(t *testing.T) {
	truncate(t)
	seedBillingUser(t, 90305, 1000000, 0)
	seedMatchableEntitlement(t, 90305, 10, 100000)

	info := relayInfoForModel(90305, "gpt-test")
	session, apiErr := NewBillingSession(newTestGinContext(1), info, 1000)
	require.Nil(t, apiErr)
	require.Equal(t, int64(1000), info.EntitlementPointsSpent)

	require.NoError(t, session.Settle(3000))
	require.Equal(t, int64(3000), info.EntitlementPointsSpent)
	require.Equal(t, "e2e", info.EntitlementPlanTitle)
	require.Equal(t, int64(10), info.EntitlementLimitCount)
	require.Equal(t, int64(1), info.EntitlementUsedCount)
}

// ---- 写进 Other ----

func TestAppendEntitlementInfo_WritesAttributionAndPoints(t *testing.T) {
	withComputePointRate(t, 100)
	info := &relaycommon.RelayInfo{
		BillingSource:          BillingSourceEntitlement,
		EntitlementId:          7,
		EntitlementPlanId:      3,
		EntitlementPlanTitle:   "专业版",
		EntitlementPointsSpent: 1050,
		EntitlementLimitCount:  500,
		EntitlementUsedCount:   153,
		SubscriptionId:         42,
	}
	other := map[string]interface{}{}
	appendEntitlementInfo(info, other)

	require.Equal(t, 7, other["entitlement_id"])
	require.Equal(t, "专业版", other["entitlement_plan_title"])
	require.Equal(t, 42, other["subscription_id"])
	// 消费侧向上取整：10.5 点记 11 点，同时存 quota 作权威值
	require.Equal(t, 11, other["compute_points"])
	require.Equal(t, int64(1050), other["compute_points_quota"])
	require.Equal(t, int64(500), other["entitlement_limit_count"])
	require.Equal(t, int64(153), other["entitlement_used_count"])
	require.NotContains(t, other, "entitlement_fallback")
}

func TestAppendEntitlementInfo_UnlimitedOmitsCount(t *testing.T) {
	info := &relaycommon.RelayInfo{BillingSource: BillingSourceEntitlement, EntitlementUsedCount: 3}
	other := map[string]interface{}{}
	appendEntitlementInfo(info, other)
	require.NotContains(t, other, "entitlement_limit_count")
	require.NotContains(t, other, "entitlement_used_count")
}

func TestAppendEntitlementInfo_WritesFallbackOnlyWhenNotEntitlement(t *testing.T) {
	fb := &relaycommon.EntitlementFallback{Reason: EntitlementFallbackCountExhausted, PlanId: 3}

	wallet := map[string]interface{}{}
	appendEntitlementInfo(&relaycommon.RelayInfo{BillingSource: BillingSourceWallet, EntitlementFallback: fb}, wallet)
	require.Equal(t, fb, wallet["entitlement_fallback"])
	require.NotContains(t, wallet, "compute_points")

	ent := map[string]interface{}{}
	appendEntitlementInfo(&relaycommon.RelayInfo{BillingSource: BillingSourceEntitlement, EntitlementFallback: fb}, ent)
	require.NotContains(t, ent, "entitlement_fallback")

	hybrid := map[string]interface{}{}
	appendEntitlementInfo(&relaycommon.RelayInfo{BillingSource: BillingSourceHybrid, EntitlementFallback: fb}, hybrid)
	require.Equal(t, fb, hybrid["entitlement_fallback"], "积分+余额混扣同样是花了钱")

	// 降级后落到老式订阅额度：用户一分没花，不是超额
	sub := map[string]interface{}{}
	appendEntitlementInfo(&relaycommon.RelayInfo{BillingSource: BillingSourceSubscription, EntitlementFallback: fb}, sub)
	require.NotContains(t, sub, "entitlement_fallback")
}

// 异步任务的提交日志自建 other、不经过 appendBillingInfo——接线漏了的话同步请求
// 有套餐归因、视频任务却没有，而视频恰恰是套餐的主力消耗。
func TestLogTaskConsumption_CarriesEntitlementInfo(t *testing.T) {
	truncate(t)
	withComputePointRate(t, 100)
	seedUser(t, 9301, 10_000_000)
	seedChannel(t, 9301)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos", nil)

	info := videoRelayInfo(t, "vid", true, nil)
	info.UserId = 9301
	info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: 9301}
	info.UsingGroup = "default"
	info.BillingSource = BillingSourceEntitlement
	info.EntitlementPlanTitle = "专业版"
	info.EntitlementPointsSpent = 60000

	LogTaskConsumption(c, info)

	var log model.Log
	require.NoError(t, model.DB.Order("id desc").First(&log).Error)
	var other map[string]interface{}
	require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
	require.Equal(t, "专业版", other["entitlement_plan_title"])
	require.Equal(t, float64(600), other["compute_points"])
}

// ---- 日志的「计费来源」筛选 ----

// fixture 由真实写入路径产出（appendEntitlementInfo → RecordConsumeLog），不手写 JSON：
// 筛选靠 LIKE 匹配序列化后的文本，手写的串和真实产出长得不一样时，测试和代码会一起错。
func recordLogWithBilling(t *testing.T, userId int, info *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	other := map[string]interface{}{}
	appendBillingInfo(info, other)
	model.RecordConsumeLog(c, userId, model.RecordConsumeLogParams{
		ModelName: "m", Quota: 100, Other: other,
	})
}

func TestLogBillingFilter_MatchesRealSerializedOther(t *testing.T) {
	truncate(t)
	seedUser(t, 9401, 1000)

	recordLogWithBilling(t, 9401, &relaycommon.RelayInfo{
		BillingSource: BillingSourceEntitlement, EntitlementPlanTitle: "专业版",
	})
	recordLogWithBilling(t, 9401, &relaycommon.RelayInfo{
		BillingSource: BillingSourceWallet,
		EntitlementFallback: &relaycommon.EntitlementFallback{
			Reason: EntitlementFallbackCountExhausted, PlanId: 1,
		},
	})
	recordLogWithBilling(t, 9401, &relaycommon.RelayInfo{BillingSource: BillingSourceWallet})

	count := func(billing string) int {
		logs, _, err := model.GetUserLogs(9401, model.LogTypeUnknown, 0, 0, "", "", 0, 100, "", "", nil, billing)
		require.NoError(t, err)
		return len(logs)
	}
	require.Equal(t, 3, count(""))
	require.Equal(t, 1, count(model.LogBillingEntitlement))
	require.Equal(t, 1, count(model.LogBillingOverage))
	require.Equal(t, 3, count("bogus"), "未知取值不筛，也不能把列表筛空")
}

// ---- 与前端的契约：golden fixture ----

// 前端（helpers/entitlementLog.js、使用日志列）按键名读日志 other。两边各测各的，
// 键名一边改了另一边没改，双方测试照样全绿、页面上一个字都不出。所以 fixture 由这里
// ——真实的写入函数——产出，前端测试直接读同一个文件。
//
// 改了写入格式后用 UPDATE_GOLDEN=1 重新生成，再跑前端测试看消费方是否跟上。
const entitlementLogGolden = "../web/classic/src/helpers/__tests__/fixtures/entitlementLogOther.json"

func TestEntitlementLogOther_MatchesFrontendGolden(t *testing.T) {
	withComputePointRate(t, 100)
	build := func(info *relaycommon.RelayInfo) map[string]interface{} {
		other := map[string]interface{}{}
		appendBillingInfo(info, other)
		return other
	}
	got := map[string]interface{}{
		"entitlement": build(&relaycommon.RelayInfo{
			BillingSource:          BillingSourceEntitlement,
			EntitlementId:          7,
			EntitlementPlanId:      3,
			EntitlementPlanTitle:   "专业版",
			EntitlementPointsSpent: 12000,
			EntitlementLimitCount:  500,
			EntitlementUsedCount:   153,
			SubscriptionId:         42,
		}),
		"overage_points": build(&relaycommon.RelayInfo{
			BillingSource: BillingSourceWallet,
			EntitlementFallback: &relaycommon.EntitlementFallback{
				Reason: EntitlementFallbackPointsInsufficient, PlanId: 3, PlanTitle: "专业版",
				PointsNeeded: 120, PointsAvailable: 40,
			},
		}),
		"overage_hybrid": build(&relaycommon.RelayInfo{
			BillingSource: BillingSourceHybrid,
			EntitlementFallback: &relaycommon.EntitlementFallback{
				Reason: EntitlementFallbackCountExhausted, PlanId: 3, PlanTitle: "专业版",
				LimitCount: 500,
			},
		}),
		"overage_count": build(&relaycommon.RelayInfo{
			BillingSource: BillingSourceWallet,
			EntitlementFallback: &relaycommon.EntitlementFallback{
				Reason: EntitlementFallbackCountExhausted, PlanId: 3, PlanTitle: "专业版",
				LimitCount: 500,
			},
		}),
	}
	gotJSON, err := common.Marshal(got)
	require.NoError(t, err)

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(entitlementLogGolden), 0o755))
		require.NoError(t, os.WriteFile(entitlementLogGolden, gotJSON, 0o644))
	}
	want, err := os.ReadFile(entitlementLogGolden)
	require.NoError(t, err, "golden 文件缺失：UPDATE_GOLDEN=1 go test ./service/ -run MatchesFrontendGolden")
	require.JSONEq(t, string(want), string(gotJSON),
		"日志 other 的写入格式变了：前端按这个文件读，先确认前端跟上再 UPDATE_GOLDEN=1 重新生成")
}

// 管理端没有 user_id 收窄，按计费来源筛选（LIKE）必须带起始时间，否则全表扫描。
// 用户侧不受限：它先被 user_id 索引收窄（上面那条测试不带时间照样能筛）。
func TestLogBillingFilter_AdminRequiresStartTimestamp(t *testing.T) {
	truncate(t)
	seedUser(t, 9402, 1000)
	recordLogWithBilling(t, 9402, &relaycommon.RelayInfo{
		BillingSource: BillingSourceEntitlement, EntitlementPlanTitle: "专业版",
	})

	_, _, err := model.GetAllLogs(model.LogTypeUnknown, 0, 0, "", "", "", 0, 100, nil, "", "", model.LogBillingEntitlement)
	require.Error(t, err)
	err = model.ExportAllLogs(model.LogTypeUnknown, 0, 0, "", "", "", nil, "", "", model.LogBillingOverage, 100,
		func([]*model.Log) error { return nil })
	require.Error(t, err)

	logs, _, err := model.GetAllLogs(model.LogTypeUnknown, 1, 0, "", "", "", 0, 100, nil, "", "", model.LogBillingEntitlement)
	require.NoError(t, err)
	require.Len(t, logs, 1)

	// 未知取值本来就不筛，不必拦
	_, _, err = model.GetAllLogs(model.LogTypeUnknown, 0, 0, "", "", "", 0, 100, nil, "", "", "bogus")
	require.NoError(t, err)
}
