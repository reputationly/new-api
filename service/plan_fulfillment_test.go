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

// 套餐履约率报表。所有 fixture 都走生产写入路径：收入由 CompleteSubscriptionOrder 写
// 流水，日志由 RecordConsumeLog / RecordTaskBillingLog 写（成本由渠道成本表在出口算）。
// 手写流水或 other 串的话，报表和测试会一起错。

const fulfillmentChannelCosted = 7101   // 配了成本比 0.5
const fulfillmentChannelUncosted = 7102 // 没配成本（自有算力或漏配）

func recordConsume(t *testing.T, userId, channelId int, quota int, info *relaycommon.RelayInfo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/chat/completions", nil)
	other := map[string]interface{}{"group_ratio": 1.0}
	appendBillingInfo(info, other)
	model.RecordConsumeLog(c, userId, model.RecordConsumeLogParams{
		ChannelId: channelId, ModelName: "gpt-test", Quota: quota, Other: other,
	})
}

func TestBuildPlanFulfillmentReport(t *testing.T) {
	truncate(t)
	withComputePointRate(t, 100)
	seedUser(t, 96001, 0)
	require.NoError(t, model.ReplaceChannelModelCosts(fulfillmentChannelCosted, []*model.ChannelModelCost{
		{ChannelId: fulfillmentChannelCosted, ModelName: "gpt-test", CostRatio: 0.5},
	}))
	t.Cleanup(model.InitChannelModelCostCache)

	plan := &model.SubscriptionPlan{
		Title: "专业版", PriceAmount: 299, Currency: "CNY", Enabled: true,
		DurationUnit: model.SubscriptionDurationMonth, DurationValue: 1,
	}
	require.NoError(t, model.DB.Create(plan).Error)
	model.InvalidateSubscriptionPlanCache(plan.Id)

	// 收入：一笔 299 元的订单。流水用生产代码拼（SubscriptionOrderFundEntry）——不驱动
	// CompleteSubscriptionOrder 本身，是因为它在事务里有几处用全局 DB 而不是 tx，
	// 单连接的测试库会等自己持有的连接（生产连接池默认 1000，不受影响）。
	order := &model.SubscriptionOrder{
		UserId: 96001, PlanId: plan.Id, Money: 299, TradeNo: "PF-1",
		Status: common.TopUpStatusSuccess, CreateTime: common.GetTimestamp(),
	}
	require.NoError(t, model.DB.Create(order).Error)
	_, err := model.InsertFundEntry(model.SubscriptionOrderFundEntry(order, plan))
	require.NoError(t, err)
	sub := model.UserSubscription{
		UserId: 96001, PlanId: plan.Id, Status: "active",
		StartTime: common.GetTimestamp() - 60, EndTime: common.GetTimestamp() + 86400,
	}
	require.NoError(t, model.DB.Create(&sub).Error)

	ent := func() *relaycommon.RelayInfo {
		return &relaycommon.RelayInfo{
			BillingSource: BillingSourceEntitlement, EntitlementPlanId: plan.Id,
			EntitlementPlanTitle: plan.Title, SubscriptionId: sub.Id,
		}
	}
	// 1 美元 = 500000 quota = ¥7.3 = 730 分
	recordConsume(t, 96001, fulfillmentChannelCosted, 500000, ent())   // 等值 730，成本 365
	recordConsume(t, 96001, fulfillmentChannelUncosted, 500000, ent()) // 等值 730，成本未知
	// 异步任务失败退款：只带订阅 id，报表要按它反查套餐并冲销等值与成本
	model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId: 96001, LogType: model.LogTypeRefund, ChannelId: fulfillmentChannelCosted,
		ModelName: "gpt-test", Quota: 100000,
		Other: taskBillingOther(&model.Task{PrivateData: model.TaskPrivateData{
			BillingSource: BillingSourceEntitlement, SubscriptionId: sub.Id,
			BillingContext: &model.TaskBillingContext{GroupRatio: 1},
		}}),
	}) // 等值 -146，成本 -73
	// 超额：被套餐覆盖却按余额扣
	recordConsume(t, 96001, fulfillmentChannelCosted, 500000, &relaycommon.RelayInfo{
		BillingSource: BillingSourceWallet,
		EntitlementFallback: &relaycommon.EntitlementFallback{
			Reason: EntitlementFallbackCountExhausted, PlanId: plan.Id,
		},
	})
	// 期内到期、用了 40% 的批次
	now := common.GetTimestamp()
	require.NoError(t, model.DB.Create(&model.ComputePointLot{
		UserId: 96001, Source: model.ComputePointLotSourceSubscription, RefId: sub.Id,
		PointsTotal: 10000, PointsUsed: 4000, ExpiresAt: now - 10,
	}).Error)
	// 窗口内、但还没到期的批次：余量不是作废，只是还没用，不能算进来
	require.NoError(t, model.DB.Create(&model.ComputePointLot{
		UserId: 96001, Source: model.ComputePointLotSourceSubscription, RefId: sub.Id,
		PointsTotal: 90000, PointsUsed: 0, ExpiresAt: now + 30,
	}).Error)

	report, err := BuildPlanFulfillmentReport(now-3600, now+60)
	require.NoError(t, err)
	require.Len(t, report.Rows, 1)
	row := report.Rows[0]

	require.Equal(t, plan.Id, row.PlanId)
	require.Equal(t, "专业版", row.PlanTitle)
	require.Equal(t, int64(1), row.OrderCount)
	require.Equal(t, int64(29900), row.RevenueFen)

	require.Equal(t, int64(2), row.CallCount, "退款不减调用次数：请求确实发生过")
	require.Equal(t, int64(730+730-146), row.ValueFen)
	require.Equal(t, int64(365-73), row.CostFen, "退款要按同一成本口径冲销")
	require.Equal(t, int64(1), row.UncostedCallCount)
	require.Equal(t, int64(730), row.UncostedValueFen)

	require.Equal(t, int64(1), row.OverageCount)
	require.Equal(t, int64(730), row.OverageFen)

	require.Equal(t, 100, row.PointsExpiredGranted)
	require.Equal(t, 60, row.PointsExpiredUnused)
	require.InDelta(t, 0.6, *row.ExpiredUnusedRatio, 1e-9)

	require.NotNil(t, row.FulfillmentRate)
	require.InDelta(t, float64(292)/29900, *row.FulfillmentRate, 1e-9)
	require.Equal(t, row.CostFen, report.Total.CostFen)
}

// 管理员直接开通的订阅没有收入：成本照列，履约率为空而不是除以 0
func TestBuildPlanFulfillmentReport_NoRevenueHasNilRate(t *testing.T) {
	truncate(t)
	seedUser(t, 96002, 0)
	require.NoError(t, model.ReplaceChannelModelCosts(fulfillmentChannelCosted, []*model.ChannelModelCost{
		{ChannelId: fulfillmentChannelCosted, ModelName: "gpt-test", CostRatio: 0.5},
	}))
	t.Cleanup(model.InitChannelModelCostCache)

	recordConsume(t, 96002, fulfillmentChannelCosted, 500000, &relaycommon.RelayInfo{
		BillingSource: BillingSourceEntitlement, EntitlementPlanId: 4242,
	})
	now := common.GetTimestamp()
	report, err := BuildPlanFulfillmentReport(now-3600, now+60)
	require.NoError(t, err)
	require.Len(t, report.Rows, 1)
	require.Equal(t, int64(365), report.Rows[0].CostFen)
	require.Nil(t, report.Rows[0].FulfillmentRate)
}

// 与前端的契约：golden fixture。前端面板按 json 字段名取值，字段改名的话前端那一列
// 会整列变成 ¥0.00、没有任何报错。fixture 由真实的转换函数（toFulfillmentRow）产出，
// 前端测试拿它当接口返回值。改了字段后 UPDATE_GOLDEN=1 重新生成，再跑前端测试。
const planFulfillmentGolden = "../web/classic/src/components/table/reconcile/__tests__/fixtures/planFulfillmentReport.json"

func TestPlanFulfillmentReport_MatchesFrontendGolden(t *testing.T) {
	withComputePointRate(t, 100)
	row := toFulfillmentRow(&planAccum{
		orderCount: 3, revenueFen: 29900,
		callCount: 120, uncostedCalls: 7,
		valueQuota: 500000, costQuota: 250000, uncostedQuota: 50000,
		overageCount: 2, overageQuota: 100000,
		expiredGranted: 10000, expiredUnused: 6000,
	})
	row.PlanId = 1
	row.PlanTitle = "专业版"
	got, err := common.Marshal(&PlanFulfillmentReport{
		Start: 1700000000, End: 1702592000,
		Rows:  []PlanFulfillmentRow{row},
		Total: row,
	})
	require.NoError(t, err)

	if os.Getenv("UPDATE_GOLDEN") == "1" {
		require.NoError(t, os.MkdirAll(filepath.Dir(planFulfillmentGolden), 0o755))
		require.NoError(t, os.WriteFile(planFulfillmentGolden, got, 0o644))
	}
	want, err := os.ReadFile(planFulfillmentGolden)
	require.NoError(t, err, "golden 缺失：UPDATE_GOLDEN=1 go test ./service/ -run PlanFulfillmentReport_MatchesFrontendGolden")
	require.JSONEq(t, string(want), string(got))
}
