package service

import (
	"context"
	"encoding/json"
	"math"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 对账口径：供应商按 ¥单价 × tokens 收我们，我们按存库的 $单价 × tokens 收用户，
// 前端再按同一个汇率把 quota 还原成 ¥。同汇率下两条链路必须落到同一个数字。
const (
	testRate        = 7.3
	seedanceTokens  = 216900
	seedanceCNYRate = 46.0 // 供应商价目表里 720p / 不含视频输入 那一格
)

func makeVideoTask(t *testing.T, userId, channelId, quota int, vb *model.TaskVideoBilling) *model.Task {
	t.Helper()
	task := &model.Task{
		TaskID:    "task_video_" + time.Now().Format("150405.000000"),
		UserId:    userId,
		ChannelId: channelId,
		Quota:     quota,
		Status:    model.TaskStatus(model.TaskStatusInProgress),
		Group:     "default",
		Data:      json.RawMessage(`{}`),
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
		Properties: model.Properties{
			OriginModelName: "doubao-seedance-2-0-260128",
		},
		PrivateData: model.TaskPrivateData{
			BillingSource: "wallet",
			BillingContext: &model.TaskBillingContext{
				GroupRatio:      1.0,
				OriginModelName: "doubao-seedance-2-0-260128",
				VideoBilling:    vb,
			},
		},
	}
	require.NoError(t, model.DB.Create(task).Error)
	return task
}

// quotaToCNY 复刻前端 renderQuota 的还原公式（web/classic/src/helpers/render.jsx）。
func quotaToCNY(quota int, rate float64) float64 {
	return float64(quota) / common.QuotaPerUnit * rate
}

// 核心不变量：配置成与供应商相同的价格时，我们的账单与供应商账单一致。
func TestVideoMatrix_ReconcilesWithSupplierBill(t *testing.T) {
	truncate(t)
	seedUser(t, 9001, 10_000_000)

	// 运营在界面上填 ¥46，前端 ÷ 汇率后落库成美元。
	usdPrice := seedanceCNYRate / testRate

	task := makeVideoTask(t, 9001, 1, 0, &model.TaskVideoBilling{
		Mode:       "token",
		UnitPrice:  usdPrice,
		Resolution: "720p",
	})

	require.True(t, RecalculateTaskQuotaByVideoMatrix(context.Background(), task, seedanceTokens))

	// 供应商账单：216900 / 1M × ¥46 = ¥9.9774
	supplierCNY := float64(seedanceTokens) / 1e6 * seedanceCNYRate
	require.InDelta(t, 9.9774, supplierCNY, 1e-9)

	// 我们的账单：落账 quota 按同一汇率还原
	oursCNY := quotaToCNY(task.Quota, testRate)
	require.InDeltaf(t, supplierCNY, oursCNY, 0.0001,
		"对账不上：供应商 ¥%.6f，我们 ¥%.6f", supplierCNY, oursCNY)

	// 截断只会少收，且不超过 1 quota
	require.LessOrEqual(t, oursCNY, supplierCNY)
	require.LessOrEqual(t, supplierCNY-oursCNY, quotaToCNY(1, testRate))
}

// 1080p 必须比 720p 贵——这正是改造前缺失的那一维（此前一律少收约 10%）。
func TestVideoMatrix_ResolutionChangesPrice(t *testing.T) {
	truncate(t)
	seedUser(t, 9002, 10_000_000)

	price := func(cny float64, resolution string) int {
		task := makeVideoTask(t, 9002, 1, 0, &model.TaskVideoBilling{
			Mode: "token", UnitPrice: cny / testRate, Resolution: resolution,
		})
		require.True(t, RecalculateTaskQuotaByVideoMatrix(context.Background(), task, seedanceTokens))
		return task.Quota
	}

	q720 := price(46, "720p")
	q1080 := price(51, "1080p")
	require.Greater(t, q1080, q720)
	require.InDelta(t, 51.0/46.0, float64(q1080)/float64(q720), 1e-4)
}

// groupRatio 必须线性缩放，不引入额外舍入。
func TestVideoMatrix_GroupRatioScales(t *testing.T) {
	truncate(t)
	seedUser(t, 9003, 10_000_000)

	task := makeVideoTask(t, 9003, 1, 0, &model.TaskVideoBilling{
		Mode: "token", UnitPrice: seedanceCNYRate / testRate, Resolution: "720p",
	})
	task.PrivateData.BillingContext.GroupRatio = 0.5
	require.True(t, RecalculateTaskQuotaByVideoMatrix(context.Background(), task, seedanceTokens))

	full := float64(seedanceTokens) / 1e6 * (seedanceCNYRate / testRate) * common.QuotaPerUnit
	require.InDelta(t, full*0.5, float64(task.Quota), 1.0)
}

// 结算必须用**提交时冻结**的分组倍率，不能按 task.Group 重查。
//
// 重查会丢信息：提交时用的是 GetGroupGroupRatio(用户分组, 使用分组)，而这里只有
// 使用分组，拿它当两个参数传会漏掉「用户分组 × 使用分组」的特殊倍率——跨分组用户
// 的结算金额就会与预扣、与定价页展示的都对不上。
//
// 顺带也挡住「提交后管理员改了倍率配置，几百秒后结算按新倍率」这一类飘移。
func TestVideoMatrix_UsesFrozenGroupRatioNotCurrentConfig(t *testing.T) {
	truncate(t)
	seedUser(t, 9008, 10_000_000)

	task := makeVideoTask(t, 9008, 1, 0, &model.TaskVideoBilling{
		Mode: "token", UnitPrice: seedanceCNYRate / testRate, Resolution: "720p",
	})
	task.PrivateData.BillingContext.GroupRatio = 0.3

	// 结算前把配置改成完全不同的值——不该影响这一单
	setGroupRatio(t, "default", 2.0)

	require.True(t, RecalculateTaskQuotaByVideoMatrix(context.Background(), task, seedanceTokens))

	full := float64(seedanceTokens) / 1e6 * (seedanceCNYRate / testRate) * common.QuotaPerUnit
	require.InDelta(t, full*0.3, float64(task.Quota), 1.0)
}

// 冻结的分组倍率为 0 是「免费分组」这个合法值，不是「没冻结过」。
//
// 若把 0 当成缺失去按 task.Group 重查，GroupGroupRatio["vip"]["default"]=0
// 这种跨分组免费会被折算回 GetGroupRatio("default")=1.0，
// 对一单提交时免费的任务按原价补扣。
func TestVideoMatrix_ZeroGroupRatioStaysFree(t *testing.T) {
	truncate(t)
	seedUser(t, 9009, 10_000_000)
	// 当前配置是 2.0；若发生重查就会按它计费，从而暴露问题
	setGroupRatio(t, "default", 2.0)

	task := makeVideoTask(t, 9009, 1, 0, &model.TaskVideoBilling{
		Mode: "token", UnitPrice: seedanceCNYRate / testRate, Resolution: "720p",
	})
	task.PrivateData.BillingContext.GroupRatio = 0 // 免费分组

	require.True(t, RecalculateTaskQuotaByVideoMatrix(context.Background(), task, seedanceTokens))
	require.Equal(t, 0, task.Quota, "提交时免费的任务不该在结算时被补扣")
}

// 日志里的 group_ratio 与实际参与计算的必须是同一个值，否则运营按日志反算永远对不上。
func TestVideoMatrix_LoggedGroupRatioMatchesCharged(t *testing.T) {
	truncate(t)
	seedUser(t, 9010, 10_000_000)

	task := makeVideoTask(t, 9010, 1, 0, &model.TaskVideoBilling{
		Mode: "token", UnitPrice: seedanceCNYRate / testRate, Resolution: "720p",
	})
	task.PrivateData.BillingContext.GroupRatio = 0.8
	require.True(t, RecalculateTaskQuotaByVideoMatrix(context.Background(), task, seedanceTokens))

	other := taskBillingOther(task)
	loggedRatio := other["group_ratio"].(float64)
	unitPrice := other["video_unit_price"].(float64)

	// 按日志上的数字反算，应当还原出落账金额
	recomputed := float64(seedanceTokens) / 1e6 * unitPrice * common.QuotaPerUnit * loggedRatio
	require.InDelta(t, recomputed, float64(task.Quota), 1.0)
}

// 未命中矩阵时必须返回 false，让调用方回退到原有的 token 重算路径。
func TestVideoMatrix_MissesFallThrough(t *testing.T) {
	truncate(t)
	seedUser(t, 9004, 10_000_000)

	cases := map[string]*model.TaskVideoBilling{
		"无冻结上下文":                  nil,
		"per_call 模式不该走 token 结算": {Mode: "per_call", UnitPrice: 1, Resolution: "720p"},
		"单价为零":                    {Mode: "token", UnitPrice: 0, Resolution: "720p"},
	}
	for name, vb := range cases {
		t.Run(name, func(t *testing.T) {
			task := makeVideoTask(t, 9004, 1, 123, vb)
			require.False(t, RecalculateTaskQuotaByVideoMatrix(context.Background(), task, seedanceTokens))
			require.Equal(t, 123, task.Quota, "未命中时不该改动额度")
		})
	}

	t.Run("tokens 为零", func(t *testing.T) {
		task := makeVideoTask(t, 9004, 1, 123, &model.TaskVideoBilling{
			Mode: "token", UnitPrice: 1, Resolution: "720p",
		})
		require.False(t, RecalculateTaskQuotaByVideoMatrix(context.Background(), task, 0))
		require.Equal(t, 123, task.Quota)
	})
}

// 模型配了「固定价格」时，旧的 RecalculateTaskQuotaByTokens 会因 hasRatioSetting=false
// 直接 return（480p 与 1080p 收一样的钱）。矩阵路径不读 ModelRatio，必须照常结算。
func TestVideoMatrix_WorksWithoutModelRatio(t *testing.T) {
	truncate(t)
	seedUser(t, 9005, 10_000_000)

	// 不给该模型配任何 ModelRatio
	task := makeVideoTask(t, 9005, 1, 0, &model.TaskVideoBilling{
		Mode: "token", UnitPrice: seedanceCNYRate / testRate, Resolution: "1080p",
	})
	require.True(t, RecalculateTaskQuotaByVideoMatrix(context.Background(), task, seedanceTokens))
	require.Greater(t, task.Quota, 0)
}

// 冻结的维度必须进日志，否则对账时无从判断取的是哪一格。
func TestVideoMatrix_LogsDimensions(t *testing.T) {
	truncate(t)
	seedUser(t, 9006, 10_000_000)

	task := makeVideoTask(t, 9006, 1, 0, &model.TaskVideoBilling{
		Mode: "token", UnitPrice: 6.3, Resolution: "1080p", HasVideoInput: true, Seconds: 10,
	})
	other := taskBillingOther(task)
	require.Equal(t, "token", other["video_price_mode"])
	require.Equal(t, "1080p", other["video_resolution"])
	require.Equal(t, true, other["video_has_input"])
	require.Equal(t, 10, other["video_seconds"])
	require.InDelta(t, 6.3, other["video_unit_price"].(float64), 1e-9)
}

// 汇率只是内部计量系数（docs/currency-fx-architecture.md §二）：
// 界面按同一个汇率录入与回显，¥ 口径就自洽——换个汇率重录一遍，账单仍对得上。
func TestVideoMatrix_RateCancelsWhenReentered(t *testing.T) {
	truncate(t)
	seedUser(t, 9007, 10_000_000)

	supplierCNY := float64(seedanceTokens) / 1e6 * seedanceCNYRate
	for _, rate := range []float64{6.5, 7.3, 8.0} {
		task := makeVideoTask(t, 9007, 1, 0, &model.TaskVideoBilling{
			Mode: "token", UnitPrice: seedanceCNYRate / rate, Resolution: "720p",
		})
		require.True(t, RecalculateTaskQuotaByVideoMatrix(context.Background(), task, seedanceTokens))
		require.InDeltaf(t, supplierCNY, quotaToCNY(task.Quota, rate), 0.0001, "rate=%v", rate)
	}
}

func setGroupRatio(t *testing.T, group string, ratio float64) {
	t.Helper()
	raw, err := json.Marshal(map[string]float64{group: ratio})
	require.NoError(t, err)
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(string(raw)))
	t.Cleanup(func() { _ = ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`) })
}

// ===========================================================================
// 端到端：从 settleTaskBillingOnComplete 进，覆盖「矩阵为什么可能不生效」的守卫
// ===========================================================================

func seedVideoSettleFixture(t *testing.T, userID int) *model.Task {
	t.Helper()
	seedUser(t, userID, 10_000_000)
	seedToken(t, userID, userID, "sk-video-"+strconv.Itoa(userID), 10_000_000)
	seedChannel(t, userID)

	task := makeVideoTask(t, userID, userID, 0, &model.TaskVideoBilling{
		Mode: "token", UnitPrice: seedanceCNYRate / testRate, Resolution: "720p",
	})
	task.PrivateData.TokenId = userID
	task.PrivateData.BillingContext.GroupRatio = 1.0
	return task
}

func expectedSeedanceQuota() int {
	return int(float64(seedanceTokens) / 1e6 * (seedanceCNYRate / testRate) * common.QuotaPerUnit)
}

// 中转商可能只透传 completion_tokens。doubao 的 usage 里没有 prompt_tokens，
// 视频任务也没有输入侧 token，所以 completion 就是全部用量。
// 不兜底的话矩阵拿到 0 直接放弃，任务按预扣额度收费。
func TestSettle_VideoMatrix_FallsBackToCompletionTokens(t *testing.T) {
	truncate(t)
	task := seedVideoSettleFixture(t, 9101)

	settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, &relaycommon.TaskInfo{
		Status:           model.TaskStatusSuccess,
		CompletionTokens: seedanceTokens, // 只有它，TotalTokens 为 0
	})

	require.Equal(t, expectedSeedanceQuota(), task.Quota)
}

// TASK_PRICE_PATCH 里列了该模型时，PerCallBilling 会让结算第 0 步早退，
// 矩阵永不生效——且全程静默。token 模式必须屏蔽掉这一半。
func TestSettle_VideoMatrix_SurvivesTaskPricePatches(t *testing.T) {
	truncate(t)
	task := seedVideoSettleFixture(t, 9102)
	// 模拟 controller 侧 taskPerCallBilling 的判定结果：token 矩阵命中 → false
	task.PrivateData.BillingContext.PerCallBilling = false

	settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, &relaycommon.TaskInfo{
		Status:      model.TaskStatusSuccess,
		TotalTokens: seedanceTokens,
	})

	require.Equal(t, expectedSeedanceQuota(), task.Quota)
}

// 矩阵优先于通用 token 重算：后者依赖 ModelRatio（可能没配），且会乘 OtherRatios。
func TestSettle_VideoMatrix_TakesPrecedenceOverTokenRecalc(t *testing.T) {
	truncate(t)
	task := seedVideoSettleFixture(t, 9103)
	task.PrivateData.BillingContext.OtherRatios = map[string]float64{"video_input": 0.6}

	settleTaskBillingOnComplete(context.Background(), &mockAdaptor{}, task, &relaycommon.TaskInfo{
		Status:      model.TaskStatusSuccess,
		TotalTokens: seedanceTokens,
	})

	// OtherRatios 不该参与——矩阵单价已是终价，再乘 0.6 就是二次计费
	require.Equal(t, expectedSeedanceQuota(), task.Quota)
}

// ===========================================================================
// 按次判定与日志字段：同一份状态的多个写入点必须同源
// ===========================================================================

func videoRelayInfo(t *testing.T, modelName string, usePrice bool, vb *relaycommon.VideoBillingContext) *relaycommon.RelayInfo {
	t.Helper()
	info := &relaycommon.RelayInfo{OriginModelName: modelName}
	info.TaskRelayInfo = &relaycommon.TaskRelayInfo{VideoBilling: vb}
	info.PriceData = types.PriceData{UsePrice: usePrice}
	return info
}

func patchTaskPricePatches(t *testing.T, models ...string) {
	t.Helper()
	prev := constant.TaskPricePatches
	constant.TaskPricePatches = models
	t.Cleanup(func() { constant.TaskPricePatches = prev })
}

// PerCallBilling 为真会让结算第 0 步早退（矩阵永不生效），
// 且日志会被打上 count_billing → 对账把整单算成 1 个计件。两个来源都必须屏蔽。
func TestIsTaskPerCallBilling_TokenMatrixOverridesBothSources(t *testing.T) {
	patchTaskPricePatches(t, "doubao-seedance-2-0-260128")
	tokenMatrix := &relaycommon.VideoBillingContext{
		Mode: ratio_setting.VideoPriceModeToken, UnitPrice: 6.3, Resolution: "720p",
	}

	require.False(t, IsTaskPerCallBilling(
		videoRelayInfo(t, "doubao-seedance-2-0-260128", false, tokenMatrix)))
	require.False(t, IsTaskPerCallBilling(
		videoRelayInfo(t, "doubao-seedance-2-0-260128", true, tokenMatrix)))
}

// 未命中矩阵、或命中的是按次矩阵时，保持改造前的判定一个字节不变。
func TestIsTaskPerCallBilling_UnchangedWhenNotTokenMatrix(t *testing.T) {
	patchTaskPricePatches(t, "patched-model")
	perCall := &relaycommon.VideoBillingContext{
		Mode: ratio_setting.VideoPriceModePerCall, UnitPrice: 0.4, Resolution: "720p",
	}

	cases := []struct {
		name      string
		modelName string
		usePrice  bool
		vb        *relaycommon.VideoBillingContext
		want      bool
	}{
		{"无矩阵 + 命中 patch", "patched-model", false, nil, true},
		{"无矩阵 + UsePrice", "other-model", true, nil, true},
		{"无矩阵 + 都不命中", "other-model", false, nil, false},
		{"按次矩阵 + 命中 patch", "patched-model", false, perCall, true},
		{"按次矩阵 + UsePrice", "other-model", true, perCall, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, IsTaskPerCallBilling(
				videoRelayInfo(t, c.modelName, c.usePrice, c.vb)))
		})
	}
}

func TestIsTaskPerCallBilling_NilSafe(t *testing.T) {
	require.False(t, IsTaskPerCallBilling(nil))
	info := &relaycommon.RelayInfo{OriginModelName: "m"} // TaskRelayInfo 为 nil
	require.False(t, IsTaskPerCallBilling(info))
}

// 提交侧的消费日志是主记录：差额为 0 时不写结算日志、per_call 压根不走结算，
// 那些情况下它是唯一的记录。缺 video_* 字段的话前端矩阵分支不触发，
// 会退回去显示一个没参与计算的 model_ratio。
func TestLogTaskConsumption_CarriesVideoMatrixFields(t *testing.T) {
	truncate(t)
	seedUser(t, 9201, 10_000_000)
	seedChannel(t, 9201)

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/videos", nil)

	info := videoRelayInfo(t, "doubao-seedance-2-0-260128", false,
		&relaycommon.VideoBillingContext{
			Mode: ratio_setting.VideoPriceModeToken, UnitPrice: 8.3836,
			Resolution: "1080p", HasVideoInput: true, Seconds: 10,
		})
	info.UserId = 9201
	info.ChannelMeta = &relaycommon.ChannelMeta{ChannelId: 9201}
	info.UsingGroup = "default"

	LogTaskConsumption(c, info)

	var log model.Log
	require.NoError(t, model.DB.Order("id desc").First(&log).Error)
	var other map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(log.Other), &other))

	require.Equal(t, ratio_setting.VideoPriceModeToken, other["video_price_mode"])
	require.Equal(t, "1080p", other["video_resolution"])
	require.Equal(t, true, other["video_has_input"])
	require.InDelta(t, 8.3836, other["video_unit_price"].(float64), 1e-9)
	// token 计费任务不能标计件——对账据此按「个」计数，会把 20 万 token 算成 1 个
	require.NotContains(t, other, "count_billing")
}

// ===========================================================================
// 已用额度统计口径：提交按预扣额记，实收更低必须回冲
// ===========================================================================

func getUserUsedQuota(t *testing.T, id int) int {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.Select("used_quota").Where("id = ?", id).First(&user).Error)
	return user.UsedQuota
}

func getUserRequestCount(t *testing.T, id int) int {
	t.Helper()
	var user model.User
	require.NoError(t, model.DB.Select("request_count").Where("id = ?", id).First(&user).Error)
	return user.RequestCount
}

// 非延迟记账的任务（sora / midjourney / per_call 矩阵）仍走「提交记预扣、完成记差额」。
// 差额退还时必须回冲 used_quota，否则用户的「已用额度」永远停在预扣值上。
func TestNonDeferred_RefundRollsBackUsedQuota(t *testing.T) {
	truncate(t)
	const uid = 9301
	seedUser(t, uid, 10_000_000)

	const preConsumed = 873287
	const actual = 159544
	task := makeVideoTask(t, uid, uid, preConsumed, nil) // 无 VideoBilling → 非延迟
	task.PrivateData.BillingContext.GroupRatio = 1.0
	// 模拟提交时 LogTaskConsumption 的记账
	model.UpdateUserUsedQuotaAndRequestCount(uid, preConsumed)
	require.Equal(t, preConsumed, getUserUsedQuota(t, uid))

	RecalculateTaskQuota(context.Background(), task, actual, "测试差额退还")

	require.Equal(t, actual, getUserUsedQuota(t, uid),
		"已用额度应落到实收，不该停在预扣额 %d", preConsumed)
	require.Equal(t, 1, getUserRequestCount(t, uid), "回冲额度不能把请求次数也减掉")
}

// ===========================================================================
// 「上游返回用量计费」：提交不记账，完成记一条终值
// ===========================================================================

func TestDeferredBilling_Predicate(t *testing.T) {
	tokenInfo := videoRelayInfo(t, "m", false, &relaycommon.VideoBillingContext{
		Mode: ratio_setting.VideoPriceModeToken, UnitPrice: 6.3,
	})
	perCallInfo := videoRelayInfo(t, "m", false, &relaycommon.VideoBillingContext{
		Mode: ratio_setting.VideoPriceModePerCall, UnitPrice: 0.4,
	})

	require.True(t, IsDeferredUsageBilling(tokenInfo))
	require.False(t, IsDeferredUsageBilling(perCallInfo), "按次矩阵提交时已定价，照常记账")
	require.False(t, IsDeferredUsageBilling(videoRelayInfo(t, "m", false, nil)))
	require.False(t, IsDeferredUsageBilling(nil))
	require.False(t, IsDeferredUsageBilling(&relaycommon.RelayInfo{}), "TaskRelayInfo 为 nil 不该 panic")
}

// 核心：提交时零记账，完成时记一条 = 实收。使用日志里这一单只有一条，
// 金额就是实收，与供应商账单同形（不再是「¥12.75 消费 + ¥10.42 退款」）。
func TestDeferredBilling_RecordsFinalAmountOnce(t *testing.T) {
	truncate(t)
	const uid = 9401
	seedUser(t, uid, 10_000_000)

	const preConsumed = 873287 // 预扣照常发生（余额闸门），只是没记账
	task := makeVideoTask(t, uid, uid, preConsumed, &model.TaskVideoBilling{
		Mode: "token", UnitPrice: seedanceCNYRate / testRate, Resolution: "480p",
	})
	task.PrivateData.BillingContext.GroupRatio = 1.0
	require.Equal(t, 0, getUserUsedQuota(t, uid), "前置：提交时未记账")

	require.True(t, RecalculateTaskQuotaByVideoMatrix(context.Background(), task, 50638))

	require.Equal(t, task.Quota, getUserUsedQuota(t, uid), "已用额度 = 实收")
	require.Equal(t, 1, getUserRequestCount(t, uid), "次数在这里才计，且只计一次")
	require.Equal(t, int64(1), countLogs(t), "只有一条日志")

	var log model.Log
	require.NoError(t, model.DB.Order("id desc").First(&log).Error)
	require.Equal(t, model.LogTypeConsume, log.Type, "记的是消费不是退款")
	require.Equal(t, task.Quota, log.Quota, "日志金额 = 实收，不是差额")
}

// 结算日志必须带上上游返回的 token 数：日志的「输出」列靠它，前端也靠它才拼得出
// 「tokens × 单价 × 倍率 = 金额」这条算式。不带的话对账只能拿一个孤零零的金额去比，
// 判断不出差异是 token 数不同还是单价取错了档。
func TestVideoMatrix_LogCarriesCompletionTokens(t *testing.T) {
	truncate(t)
	const uid = 9406
	seedUser(t, uid, 10_000_000)

	task := makeVideoTask(t, uid, uid, 873287, &model.TaskVideoBilling{
		Mode: "token", UnitPrice: seedanceCNYRate / testRate, Resolution: "480p",
	})
	task.PrivateData.BillingContext.GroupRatio = 1.0

	require.True(t, RecalculateTaskQuotaByVideoMatrix(context.Background(), task, 50638))

	var log model.Log
	require.NoError(t, model.DB.Order("id desc").First(&log).Error)
	require.Equal(t, 50638, log.CompletionTokens)

	// 按日志上的数字反算，应当还原出落账金额
	var other map[string]interface{}
	require.NoError(t, json.Unmarshal([]byte(log.Other), &other))
	unitPrice := other["video_unit_price"].(float64)
	ratio := other["group_ratio"].(float64)
	recomputed := float64(log.CompletionTokens) / 1e6 * unitPrice * common.QuotaPerUnit * ratio
	require.InDelta(t, recomputed, float64(task.Quota), 1.0)
}

// 预扣恰好等于实收时也必须记账——延迟记账下这是唯一的记账时机，
// 零差额直接 return 会让这一单在日志里完全不存在。
func TestDeferredBilling_RecordsEvenWhenDeltaIsZero(t *testing.T) {
	truncate(t)
	const uid = 9402
	seedUser(t, uid, 10_000_000)

	task := makeVideoTask(t, uid, uid, 159544, &model.TaskVideoBilling{
		Mode: "token", UnitPrice: seedanceCNYRate / testRate, Resolution: "480p",
	})
	task.PrivateData.BillingContext.GroupRatio = 1.0

	require.True(t, RecalculateTaskQuotaByVideoMatrix(context.Background(), task, 50638))
	require.Equal(t, int64(1), countLogs(t))
	require.Equal(t, task.Quota, getUserUsedQuota(t, uid))
}

// 免费分组（groupRatio=0）算出的 actualQuota 就是 0，那是合法结果不是失败。
// 早退的话这一单永远不出现在使用日志里——用户用了却查不到。
func TestDeferredBilling_ZeroQuotaStillRecords(t *testing.T) {
	truncate(t)
	const uid = 9407
	seedUser(t, uid, 10_000_000)

	// 免费分组：提交时 FreeModel=true 已跳过预扣，task.Quota 为 0
	task := makeVideoTask(t, uid, uid, 0, &model.TaskVideoBilling{
		Mode: "token", UnitPrice: seedanceCNYRate / testRate, Resolution: "480p",
	})
	task.PrivateData.BillingContext.GroupRatio = 0

	require.True(t, RecalculateTaskQuotaByVideoMatrix(context.Background(), task, 50638))

	require.Equal(t, int64(1), countLogs(t), "免费任务也要有一条记录")
	var log model.Log
	require.NoError(t, model.DB.Order("id desc").First(&log).Error)
	require.Equal(t, model.LogTypeConsume, log.Type)
	require.Equal(t, 0, log.Quota)
	require.Equal(t, 50638, log.CompletionTokens, "用量照记，只是金额为 0")
	require.Equal(t, 10_000_000, getUserQuota(t, uid), "余额不动")
}

// 极小倍率 + 小任务被 int() 截断为 0：预扣必须退回，不能白留。
func TestDeferredBilling_TruncatedToZeroRefundsPreConsumed(t *testing.T) {
	truncate(t)
	const uid = 9408
	const initQuota = 10_000_000
	const preConsumed = 1000
	seedUser(t, uid, initQuota)

	task := makeVideoTask(t, uid, uid, preConsumed, &model.TaskVideoBilling{
		Mode: "token", UnitPrice: seedanceCNYRate / testRate, Resolution: "480p",
	})
	task.PrivateData.BillingContext.GroupRatio = 0.001

	// 100 tokens × 6.3 × 500000 / 1e6 × 0.001 = 0.315 → int() → 0
	require.True(t, RecalculateTaskQuotaByVideoMatrix(context.Background(), task, 100))

	require.Equal(t, 0, task.Quota)
	require.Equal(t, initQuota+preConsumed, getUserQuota(t, uid), "预扣必须退回")
	require.Equal(t, int64(1), countLogs(t))
}

// 非延迟记账的任务保持既有语义：actualQuota=0 视为「无调整」，不记账不退款。
// 既有的 TestRecalculate_ActualQuotaZero 也钉着这条，这里从矩阵入口再确认一次。
func TestNonDeferred_ZeroQuotaStillNoop(t *testing.T) {
	truncate(t)
	const uid = 9409
	seedUser(t, uid, 10_000_000)

	task := makeVideoTask(t, uid, uid, 5000, nil) // 无 VideoBilling → 非延迟
	RecalculateTaskQuota(context.Background(), task, 0, "zero actual")

	require.Equal(t, 5000, task.Quota, "额度不该被改写")
	require.Equal(t, int64(0), countLogs(t))
}

// 结算没走到任何记账分支时的兜底：钱扣了、日志里查无此单是最糟的失败。
func TestDeferredBilling_FallbackRecordsPreConsumed(t *testing.T) {
	truncate(t)
	const uid = 9403
	seedUser(t, uid, 10_000_000)

	const preConsumed = 873287
	task := makeVideoTask(t, uid, uid, preConsumed, &model.TaskVideoBilling{
		Mode: "token", UnitPrice: seedanceCNYRate / testRate, Resolution: "480p",
	})
	task.PrivateData.BillingContext.GroupRatio = 1.0

	DeferredBillingFallback(context.Background(), task, "上游未返回用量")

	require.Equal(t, int64(1), countLogs(t))
	require.Equal(t, preConsumed, getUserUsedQuota(t, uid), "兜底按预扣额入账")
}

// 非延迟记账的任务不该被兜底重复记账。
func TestDeferredBilling_FallbackIsNoopForNormalTasks(t *testing.T) {
	truncate(t)
	const uid = 9404
	seedUser(t, uid, 10_000_000)

	task := makeVideoTask(t, uid, uid, 1000, nil)
	DeferredBillingFallback(context.Background(), task, "任意原因")

	require.Equal(t, int64(0), countLogs(t))
	require.Equal(t, 0, getUserUsedQuota(t, uid))
}

// 延迟记账的任务失败时全额退，净消费为 0——不该留下一条没有对应消费行的孤儿退款。
func TestDeferredBilling_FailedTaskWritesNoLog(t *testing.T) {
	truncate(t)
	const uid = 9405
	seedUser(t, uid, 10_000_000)

	task := makeVideoTask(t, uid, uid, 873287, &model.TaskVideoBilling{
		Mode: "token", UnitPrice: seedanceCNYRate / testRate, Resolution: "480p",
	})

	RefundTaskQuota(context.Background(), task, "上游返回失败")

	require.Equal(t, int64(0), countLogs(t), "从没记过消费，就不该有退款行")
	require.Equal(t, 0, getUserUsedQuota(t, uid))
}

// 任务失败全额退款后这笔消费为 0，已用额度不该把失败的单算进去。
func TestRefundTaskQuota_RollsBackUsedQuota(t *testing.T) {
	truncate(t)
	const uid = 9302
	seedUser(t, uid, 10_000_000)

	const preConsumed = 873287
	task := makeVideoTask(t, uid, uid, preConsumed, nil) // 无 VideoBilling → 非延迟
	model.UpdateUserUsedQuotaAndRequestCount(uid, preConsumed)

	RefundTaskQuota(context.Background(), task, "上游返回失败")

	require.Equal(t, 0, getUserUsedQuota(t, uid))
	require.Equal(t, 1, getUserRequestCount(t, uid), "请求确实发生过，次数不回冲")
}

// 非延迟记账的任务实收高于预扣时走补扣分支。
// 同一次请求在提交时已计过数，差额结算不该再计一次。
func TestNonDeferred_TopUpKeepsRequestCount(t *testing.T) {
	truncate(t)
	const uid = 9303
	seedUser(t, uid, 10_000_000)

	const preConsumed = 1000 // 故意远低于实收
	const actual = 159544
	task := makeVideoTask(t, uid, uid, preConsumed, nil)
	task.PrivateData.BillingContext.GroupRatio = 1.0
	model.UpdateUserUsedQuotaAndRequestCount(uid, preConsumed)

	RecalculateTaskQuota(context.Background(), task, actual, "测试差额补扣")

	require.Equal(t, actual, getUserUsedQuota(t, uid))
	require.Equal(t, 1, getUserRequestCount(t, uid), "一次请求只该计一次数")
}

func TestQuotaTruncationBoundIsNegligible(t *testing.T) {
	// 每单最多少收 1 quota；1 万单累计上限。
	perOrder := quotaToCNY(1, testRate)
	require.Less(t, perOrder, 0.00002)
	require.Less(t, math.Abs(perOrder*10000), 0.2)
}
