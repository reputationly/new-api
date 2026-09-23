package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func seedEntitlementPlan(t *testing.T, resetPeriod string) *SubscriptionPlan {
	t.Helper()
	plan := &SubscriptionPlan{
		Title:            "权益测试套餐",
		PriceAmount:      299,
		Currency:         "CNY",
		DurationUnit:     SubscriptionDurationMonth,
		DurationValue:    1,
		Enabled:          true,
		QuotaResetPeriod: resetPeriod,
	}
	require.NoError(t, DB.Create(plan).Error)
	// SQLite 删行后会复用 id：上一个测试缓存的同 id 套餐会被读到，按套餐判定的逻辑
	// （如老式额度「无 / 不限」）就会拿到别的测试的配置
	InvalidateSubscriptionPlanCache(plan.Id)
	return plan
}

func replaceEntitlements(t *testing.T, planId int, items []SubscriptionPlanEntitlement) error {
	t.Helper()
	return DB.Transaction(func(tx *gorm.DB) error {
		return ReplacePlanEntitlementsTx(tx, planId, items)
	})
}

// ---------------------------------------------------------------------------
// 校验与规整
// ---------------------------------------------------------------------------

// 「消耗算力点=否」是无限制模型这个核心功能的开关，必须能真的存成 false。
// GORM 对带 default 标签的字段会把零值从 INSERT 里省掉、让数据库默认值顶上，
// 那样这个开关会静默失效——这里把它钉死。
func TestReplacePlanEntitlements_ConsumePointsFalseRoundTrips(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)

	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "qwen3-flash", ConsumePoints: false, RateLimitRPM: 30},
	}))

	list, err := ListPlanEntitlements(plan.Id)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.False(t, list[0].ConsumePoints, "「不消耗算力点」必须能真的存成 false")
}

// 同一条权益从「消耗」改成「不消耗」也必须落库——更新路径若用结构体更新，
// GORM 同样会把 false 当作未设置而跳过。
func TestReplacePlanEntitlements_TogglingConsumePointsOffPersists(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)

	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "qwen3-flash", ConsumePoints: true, LimitCount: 100, ResetPeriod: SubscriptionResetMonthly},
	}))
	list, err := ListPlanEntitlements(plan.Id)
	require.NoError(t, err)
	require.Len(t, list, 1)

	kept := *list[0]
	kept.ConsumePoints = false
	kept.RateLimitRPM = 30
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{kept}))

	list, err = ListPlanEntitlements(plan.Id)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.False(t, list[0].ConsumePoints, "关闭「消耗算力点」必须落库")
}

// 不限次 / 不消耗算力点的权益没有任何成本闸门，RPM 是唯一兜底，必填。
func TestValidateEntitlement_RateLimitRequiredWhenNoCostGate(t *testing.T) {
	cases := []struct {
		name string
		ent  SubscriptionPlanEntitlement
		ok   bool
	}{
		{"不限次且无RPM", SubscriptionPlanEntitlement{Models: "a", ConsumePoints: true, ConsumeDiscount: 1}, false},
		{"不限次但有RPM", SubscriptionPlanEntitlement{Models: "a", ConsumePoints: true, ConsumeDiscount: 1, RateLimitRPM: 60}, true},
		{"不消耗算力点且无RPM", SubscriptionPlanEntitlement{Models: "a", ConsumePoints: false, ConsumeDiscount: 1, LimitCount: 100}, false},
		{"限次且消耗算力点可不填RPM", SubscriptionPlanEntitlement{Models: "a", ConsumePoints: true, ConsumeDiscount: 1, LimitCount: 500}, true},
		{"模型范围为空", SubscriptionPlanEntitlement{Models: "  ", ConsumePoints: true, ConsumeDiscount: 1, LimitCount: 500}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ent := c.ent
			err := ValidateEntitlement(&ent)
			if c.ok {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
		})
	}
}

func TestNormalizeEntitlement_TrimsDedupesAndDefaults(t *testing.T) {
	ent := SubscriptionPlanEntitlement{
		Models:          " gpt-5 , claude-opus-* ,gpt-5,, ",
		ChannelIds:      " 3 , 3 ,5 ",
		ConsumeDiscount: 0,
		LimitCount:      -5,
		RateLimitRPM:    -1,
		ResetPeriod:     "不存在的周期",
	}
	NormalizeEntitlement(&ent)

	require.Equal(t, "gpt-5,claude-opus-*", ent.Models, "去空白、去重、保持书写顺序")
	require.Equal(t, "3,5", ent.ChannelIds)
	require.Equal(t, float64(1), ent.ConsumeDiscount, "折扣系数缺省为 1")
	require.Equal(t, int64(0), ent.LimitCount, "负数次数归零，即不限次")
	require.Equal(t, 0, ent.RateLimitRPM)
	require.Equal(t, SubscriptionResetNever, ent.ResetPeriod, "非法周期归一为 never")
}

// ---------------------------------------------------------------------------
// 整体替换：Id 稳定性与连带清理
// ---------------------------------------------------------------------------

// 保留下来的权益必须沿用原 Id。已售出订阅的次数计数器通过 PlanEntitlementId 指向
// 它，若「全删再全插」，存量客户的计数器会集体指向不存在的权益。
func TestReplacePlanEntitlements_KeepsIdsForSurvivingRows(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)

	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "qwen3-*", ConsumePoints: true, RateLimitRPM: 60},
		{Models: "gpt-5", ConsumePoints: true, LimitCount: 500, ResetPeriod: SubscriptionResetMonthly},
	}))
	before, err := ListPlanEntitlements(plan.Id)
	require.NoError(t, err)
	require.Len(t, before, 2)

	// 改第二条的次数上限，两条都保留
	first := *before[0]
	second := *before[1]
	second.LimitCount = 800
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{first, second}))

	after, err := ListPlanEntitlements(plan.Id)
	require.NoError(t, err)
	require.Len(t, after, 2)
	require.Equal(t, before[0].Id, after[0].Id, "保留的权益必须沿用原 Id")
	require.Equal(t, before[1].Id, after[1].Id)
	require.Equal(t, int64(800), after[1].LimitCount)
}

// 顺序即匹配优先级，由提交顺序唯一决定，不信任前端传来的 SortOrder。
func TestReplacePlanEntitlements_SortOrderFollowsSubmittedOrder(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)

	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "a", SortOrder: 99, ConsumePoints: true, RateLimitRPM: 60},
		{Models: "b", SortOrder: 5, ConsumePoints: true, RateLimitRPM: 60},
		{Models: "c", SortOrder: 0, ConsumePoints: true, RateLimitRPM: 60},
	}))

	list, err := ListPlanEntitlements(plan.Id)
	require.NoError(t, err)
	require.Len(t, list, 3)
	require.Equal(t, "a", list[0].Models)
	require.Equal(t, "b", list[1].Models)
	require.Equal(t, "c", list[2].Models)
	require.Equal(t, 0, list[0].SortOrder)
	require.Equal(t, 1, list[1].SortOrder)
	require.Equal(t, 2, list[2].SortOrder)
}

// 被移除的权益要连带删掉指向它的用户计数器，否则闸门定义没了、计数还留着。
func TestReplacePlanEntitlements_RemovedEntitlementDropsUserCounters(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "qwen3-*", ConsumePoints: true, RateLimitRPM: 60},
		{Models: "gpt-5", ConsumePoints: true, LimitCount: 500, ResetPeriod: SubscriptionResetMonthly},
	}))

	sub, err := CreateUserSubscriptionFromPlanTx(DB, 801, plan, "order")
	require.NoError(t, err)

	var counters []UserSubscriptionEntitlement
	require.NoError(t, DB.Where("user_subscription_id = ?", sub.Id).Find(&counters).Error)
	require.Len(t, counters, 2, "购买套餐时应为每条权益建好计数器")

	list, err := ListPlanEntitlements(plan.Id)
	require.NoError(t, err)
	kept := *list[0]
	removedId := list[1].Id
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{kept}))

	require.NoError(t, DB.Where("user_subscription_id = ?", sub.Id).Find(&counters).Error)
	require.Len(t, counters, 1, "被移除权益的计数器应一并清掉")
	require.NotEqual(t, removedId, counters[0].PlanEntitlementId)
}

// 权益校验不通过时，套餐本身也不该被改动——两者在同一个事务里。
func TestReplacePlanEntitlements_InvalidItemRollsBackWholeTransaction(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "qwen3-*", ConsumePoints: true, RateLimitRPM: 60},
	}))

	err := replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "qwen3-*", ConsumePoints: true, RateLimitRPM: 60},
		{Models: "gpt-5", ConsumePoints: true, LimitCount: 0}, // 不限次却没填 RPM
	})
	require.ErrorIs(t, err, ErrEntitlementRateRequired)

	list, err := ListPlanEntitlements(plan.Id)
	require.NoError(t, err)
	require.Len(t, list, 1, "校验失败应整体回滚，不留下半套配置")
}

// ---------------------------------------------------------------------------
// 次数重置
// ---------------------------------------------------------------------------

func TestResetDueUserSubscriptionEntitlements_ZeroesUsedAndAdvancesPeriod(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "gpt-5", ConsumePoints: true, LimitCount: 500, ResetPeriod: SubscriptionResetMonthly},
	}))
	sub, err := CreateUserSubscriptionFromPlanTx(DB, 802, plan, "order")
	require.NoError(t, err)

	var counter UserSubscriptionEntitlement
	require.NoError(t, DB.Where("user_subscription_id = ?", sub.Id).First(&counter).Error)
	require.Greater(t, counter.NextResetTime, int64(0))

	// 用掉一部分，并把重置时刻拨到过去
	due := GetDBTimestamp() - 10
	require.NoError(t, DB.Model(&UserSubscriptionEntitlement{}).Where("id = ?", counter.Id).
		Updates(map[string]interface{}{"used_count": 321, "next_reset_time": due}).Error)

	n, err := ResetDueUserSubscriptionEntitlements(100)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	var after UserSubscriptionEntitlement
	require.NoError(t, DB.First(&after, counter.Id).Error)
	require.Equal(t, int64(0), after.UsedCount, "重置必须把已用次数归零")
	require.Equal(t, due, after.LastResetTime, "上次重置时刻应记成刚跨过的那个边界")
	// 必须离开刚消费掉的那个边界值——条件更新抢占正是靠它区分「这次重置还没人做」
	// 和「已经有人做完了」，停在原地会被每轮反复重置。
	require.NotEqual(t, due, after.NextResetTime, "重置时刻必须离开旧边界")
}

// 运营改了套餐的次数上限，下一次重置时存量客户跟着刷新——这是明确选定的
// 「实时生效」语义，不是购买时冻结的快照。
func TestResetDueUserSubscriptionEntitlements_RefreshesLimitFromPlan(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "gpt-5", ConsumePoints: true, LimitCount: 500, ResetPeriod: SubscriptionResetMonthly},
	}))
	sub, err := CreateUserSubscriptionFromPlanTx(DB, 803, plan, "order")
	require.NoError(t, err)

	var counter UserSubscriptionEntitlement
	require.NoError(t, DB.Where("user_subscription_id = ?", sub.Id).First(&counter).Error)
	require.Equal(t, int64(500), counter.LimitCount)

	// 运营把次数上限调到 900
	list, err := ListPlanEntitlements(plan.Id)
	require.NoError(t, err)
	updated := *list[0]
	updated.LimitCount = 900
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{updated}))

	require.NoError(t, DB.Model(&UserSubscriptionEntitlement{}).Where("id = ?", counter.Id).
		Update("next_reset_time", GetDBTimestamp()-10).Error)
	n, err := ResetDueUserSubscriptionEntitlements(100)
	require.NoError(t, err)
	require.Equal(t, 1, n)

	var after UserSubscriptionEntitlement
	require.NoError(t, DB.First(&after, counter.Id).Error)
	require.Equal(t, int64(900), after.LimitCount, "下期额度应按套餐当前配置刷新")
}

// 归属对象没了的计数器只摘出重置队列、不删行——后台任务不该凭一次「查不到」
// 就销毁计费计数器。
func TestResetDueUserSubscriptionEntitlements_OrphanIsParkedNotDeleted(t *testing.T) {
	truncateTables(t)
	orphan := &UserSubscriptionEntitlement{
		UserId:             804,
		UserSubscriptionId: 999999,
		PlanEntitlementId:  999999,
		LimitCount:         100,
		UsedCount:          7,
		NextResetTime:      GetDBTimestamp() - 10,
	}
	require.NoError(t, DB.Create(orphan).Error)

	n, err := ResetDueUserSubscriptionEntitlements(100)
	require.NoError(t, err)
	require.Equal(t, 0, n)

	var after UserSubscriptionEntitlement
	require.NoError(t, DB.First(&after, orphan.Id).Error)
	require.Equal(t, int64(0), after.NextResetTime, "应摘出重置队列，避免每轮重复捞出")
	require.Equal(t, int64(7), after.UsedCount, "不得销毁计数证据")
}

// 不设重置周期的权益不该进入重置队列，否则会被当成「立刻到期」反复处理。
func TestInstantiateEntitlements_NeverResetHasNoNextResetTime(t *testing.T) {
	truncateTables(t)
	plan := seedEntitlementPlan(t, SubscriptionResetMonthly)
	require.NoError(t, replaceEntitlements(t, plan.Id, []SubscriptionPlanEntitlement{
		{Models: "qwen3-*", ConsumePoints: true, RateLimitRPM: 60, ResetPeriod: SubscriptionResetNever},
	}))

	sub, err := CreateUserSubscriptionFromPlanTx(DB, 805, plan, "order")
	require.NoError(t, err)

	var counter UserSubscriptionEntitlement
	require.NoError(t, DB.Where("user_subscription_id = ?", sub.Id).First(&counter).Error)
	require.Equal(t, int64(0), counter.NextResetTime)

	n, err := ResetDueUserSubscriptionEntitlements(100)
	require.NoError(t, err)
	require.Equal(t, 0, n)
}

// ---------------------------------------------------------------------------
// 模型范围匹配
// ---------------------------------------------------------------------------

// P4 的请求闸门会用这个匹配器判断「这次调用算不算在套餐内」，匹配错就是计费错。
// 与前端 helpers/entitlementOverlap.js 是同语义的两份实现，改一边必须改另一边。
func TestEntitlementMatchesModel(t *testing.T) {
	cases := []struct {
		patterns string
		name     string
		want     bool
	}{
		{"gpt-5", "gpt-5", true},
		{"gpt-5", "gpt-5-mini", false},
		{"gpt-5", "gpt-4", false},
		{"claude-opus-*", "claude-opus-4", true},
		{"claude-opus-*", "claude-opus-", true},
		{"claude-opus-*", "claude-sonnet-4", false},
		{"qwen3-*,glm-*", "glm-4-plus", true},
		{"qwen3-*,glm-*", "gpt-5", false},
		{"*", "任意模型", true},
		// 模型名里带 . 很常见（ltx2.5、gpt-4.1）。按段扫描实现天然按字面量处理，
		// 不像正则那样需要转义——这里把这个性质钉死，防止哪天改成正则实现又踩回去。
		{"ltx2.5", "ltx2.5", true},
		{"ltx2.5", "ltx2x5", false},
		{"gpt-4.1-*", "gpt-4.1-mini", true},
		{"gpt-4.1-*", "gpt-4x1-mini", false},
		// 中间带通配
		{"a*z", "abcz", true},
		{"a*z", "abc", false},
		{"", "gpt-5", false},
	}
	for _, c := range cases {
		ent := &SubscriptionPlanEntitlement{Models: c.patterns}
		require.Equal(t, c.want, ent.MatchesModel(c.name),
			"patterns=%q name=%q", c.patterns, c.name)
	}
}

func TestEntitlementMatchesModel_NilAndEmpty(t *testing.T) {
	var nilEnt *SubscriptionPlanEntitlement
	require.False(t, nilEnt.MatchesModel("gpt-5"))
	require.False(t, (&SubscriptionPlanEntitlement{Models: "gpt-5"}).MatchesModel(""))
}

// ---------------------------------------------------------------------------
// 重置窗口计数
// ---------------------------------------------------------------------------

// 次数上限是每个重置窗口的额度，售价对应整个套餐周期。窗口数算错就会让
// 「最坏成本 vs 售价」的比较失真——算少了是低估，正是「保证不亏」最怕的方向。
func TestCountEntitlementResetWindows(t *testing.T) {
	// 用固定起点，避免测试在月末跑时结果漂移
	start := time.Date(2026, 1, 5, 10, 0, 0, 0, time.Local)

	monthEnd := start.AddDate(0, 1, 0).Unix()
	yearEnd := start.AddDate(1, 0, 0).Unix()

	t.Run("不重置视为单个窗口", func(t *testing.T) {
		n, capped := CountEntitlementResetWindows(start, monthEnd, SubscriptionResetNever, 0)
		require.Equal(t, 1, n)
		require.False(t, capped)
	})

	// Jan5 买的月付套餐，Feb1 会按自然月对齐重置一次，到 Feb5 结束前经历两个窗口。
	// 「一个月≈30天」的近似算法会把这半个窗口算丢，从而低估最坏成本。
	t.Run("月付套餐配月重置会跨自然月边界", func(t *testing.T) {
		n, _ := CountEntitlementResetWindows(start, monthEnd, SubscriptionResetMonthly, 0)
		require.Equal(t, 2, n)
	})

	t.Run("月付套餐配周重置约四到五个窗口", func(t *testing.T) {
		n, _ := CountEntitlementResetWindows(start, monthEnd, SubscriptionResetWeekly, 0)
		require.GreaterOrEqual(t, n, 4)
		require.LessOrEqual(t, n, 6)
	})

	t.Run("年付套餐配月重置约十二个窗口", func(t *testing.T) {
		n, _ := CountEntitlementResetWindows(start, yearEnd, SubscriptionResetMonthly, 0)
		require.GreaterOrEqual(t, n, 12)
		require.LessOrEqual(t, n, 13)
	})

	// 自定义周期允许填极小的秒数，配上长周期套餐会让循环次数爆炸。
	t.Run("自定义周期极小时撞护栏而不是卡死", func(t *testing.T) {
		n, capped := CountEntitlementResetWindows(start, yearEnd, SubscriptionResetCustom, 1)
		require.True(t, capped, "应撞到护栏上限")
		require.Equal(t, entitlementResetWindowCap, n)
	})
}
