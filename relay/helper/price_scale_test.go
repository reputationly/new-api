package helper

import (
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 预扣的倍率路径（ModelPriceHelper）与「预扣 token 数下限」的关系。
//
// 单位约定（新口径）：倍率路径的 quota/token = ratio × QuotaPerMillionTokens（=1000），
// 而 common.PreConsumedQuota **不是 quota，是 token 数下限**，所以它既不乘 1000、
// 也不参与单位迁移。这条用例把两者一起钉住：
//   - 若有人给下限也乘了 1000，冻结额会放大 1000 倍；
//   - 若有人把倍率路径写回「× QuotaPerUnit / 汇率」，QPU 变化会让预扣跟着变。
func TestModelPriceHelperPreConsumeUsesTokenFloorNotQuotaUnit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// relay/helper 的测试二进制不跑 InitResources，倍率表要显式加载默认值。
	ratio_setting.InitRatioSettings()

	const modelName = "gpt-4o"
	modelRatio, ok, _ := ratio_setting.GetModelRatio(modelName)
	require.True(t, ok, "前置：%s 必须有默认倍率", modelName)
	require.Equal(t, 1.0,
		ratio_setting.ResolveGroupRatio("default", "default", modelName).Final,
		"前置：default 分组倍率必须是 1，否则期望值要跟着改")

	require.Less(t, 100, common.PreConsumedQuota,
		"前置：promptTokens 必须低于下限，否则测不到下限这条分支")

	original := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = original })

	preConsume := func(qpu float64) int {
		common.QuotaPerUnit = qpu
		ctx, _ := gin.CreateTestContext(nil)
		info := &relaycommon.RelayInfo{
			OriginModelName: modelName,
			UserGroup:       "default",
			UsingGroup:      "default",
		}
		priceData, err := ModelPriceHelper(ctx, info, 100, &types.TokenCountMeta{})
		require.NoError(t, err)
		return priceData.QuotaToPreConsume
	}

	// 期望值：token 下限 × 倍率 × 1000，与 QuotaPerUnit 无关。
	want := int(math.Round(float64(common.PreConsumedQuota) * modelRatio * common.QuotaPerMillionTokens))
	require.Equal(t, want, preConsume(500_000), "倍率路径固定乘 1000，不读 QPU")
	require.Equal(t, want, preConsume(7_300_000), "QPU = ¥1e-6 单位下预扣不变")
	require.Equal(t, want, preConsume(7_300_000_000), "QPU = ¥1e-9 单位下预扣不变")
}

// 按次价分支（ModelPriceHelperPerCall）必须用人民币 × QuotaPerYuan 并四舍五入：
// 运营填 ¥0.1 时应收 100,000,000 quota（= ¥0.1），且不读 QPU、不受汇率影响。
func TestModelPriceHelperPerCallPricePath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const modelName = "percall-test-model"
	saved := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateModelPriceByJSONString(saved) })
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(
		`{"percall-test-model":0.1}`)) // ¥0.1/次

	original := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = original })

	perCallQuota := func(qpu float64) int {
		common.QuotaPerUnit = qpu
		ctx, _ := gin.CreateTestContext(nil)
		info := &relaycommon.RelayInfo{
			OriginModelName: modelName,
			UserGroup:       "default",
			UsingGroup:      "default",
		}
		priceData, err := ModelPriceHelperPerCall(ctx, info)
		require.NoError(t, err)
		return priceData.Quota
	}

	require.Equal(t, 100_000_000, perCallQuota(7_300_000), "¥0.1 应按 100,000,000 quota 收取")
	require.Equal(t, 100_000_000, perCallQuota(7_300_000_000), "按次价路径不读 QPU")
}

// 按次价的取整方向：int() 截断会少收 1 quota，必须是 math.Round。
// ¥0.0099999999 × 1e9 = 9,999,999.9 → 10,000,000。
func TestModelPriceHelperPerCallRoundsPerCallPrice(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const modelName = "percall-rounding-test-model"
	saved := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateModelPriceByJSONString(saved) })
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(
		`{"percall-rounding-test-model":0.0099999999}`))

	ctx, _ := gin.CreateTestContext(nil)
	info := &relaycommon.RelayInfo{
		OriginModelName: modelName,
		UserGroup:       "default",
		UsingGroup:      "default",
	}
	priceData, err := ModelPriceHelperPerCall(ctx, info)
	require.NoError(t, err)
	require.Equal(t, 10_000_000, priceData.Quota, "9,999,999.9 必须四舍五入到 10,000,000（不得截断）")
}

// 按次缺价时的倍率兜底是「预扣 25 万 tokens 的输入价」：250000 × 倍率 × 1000。
// 它是 token 语义，不得再乘 QuotaPerUnit/汇率，也必须四舍五入。
func TestModelPriceHelperPerCallRatioFallbackDoesNotDoubleScale(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// relay/helper 的测试二进制不跑 InitResources，倍率表要显式加载默认值。
	ratio_setting.InitRatioSettings()

	const modelName = "gpt-4o" // 有默认倍率、测试内不配按次价
	modelRatio, ok, _ := ratio_setting.GetModelRatio(modelName)
	require.True(t, ok)

	original := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = original })

	quota := func(qpu float64) int {
		common.QuotaPerUnit = qpu
		ctx, _ := gin.CreateTestContext(nil)
		info := &relaycommon.RelayInfo{
			OriginModelName: modelName,
			UserGroup:       "default",
			UsingGroup:      "default",
		}
		priceData, err := ModelPriceHelperPerCall(ctx, info)
		require.NoError(t, err)
		return priceData.Quota
	}

	want := int(math.Round(250_000 * modelRatio * common.QuotaPerMillionTokens))
	require.Equal(t, want, quota(7_300_000))
	require.Equal(t, want, quota(7_300_000_000), "按量预扣是 token 语义，不读 QPU")
}

// seedAlwaysOnWindow 铺一条「此刻必然生效」的时段规则：窗口以当前时刻为中心
// 前后各扩 2 小时（跨午夜由段语义自然覆盖），days 留空=每天。
// 返回值是窗口键名，调用方负责在 cleanup 里清空 GroupTimeRatio。
func seedAlwaysOnWindow(t *testing.T, modelName, pricesJSON string) {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	now := time.Now().In(loc)
	cfg := fmt.Sprintf(`{"windows":{"peak":{"label":"peak","start":%q,"end":%q}},`+
		`"rules":{"*":{"%s":[{"window":"peak","prices":%s}]}}}`,
		now.Add(-2*time.Hour).Format("15:04"), now.Add(2*time.Hour).Format("15:04"),
		modelName, pricesJSON)
	require.NoError(t, ratio_setting.UpdateGroupTimeRatioByJSONString(cfg))
}

// per_call 是分量价目：命中时段时按次价必须吃到自己的系数。
// 整单只乘 input 系数（GroupRatio），per_call 的差额由 ScaleComponent 折进单价——
// 少了它，运营在面板上配的「高峰按次价」永远不会出现在账单里（预扣与结算同漏）。
func TestModelPriceHelperPerCallAppliesTimeFactor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ratio_setting.InitRatioSettings()

	const modelName = "percall-timefactor-test"
	savedPrice := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelPriceByJSONString(savedPrice)
		_ = ratio_setting.UpdateGroupTimeRatioByJSONString(`{}`)
	})
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(
		`{"percall-timefactor-test":0.1}`)) // ¥0.1/次

	perCallQuota := func() int {
		ctx, _ := gin.CreateTestContext(nil)
		info := &relaycommon.RelayInfo{
			OriginModelName: modelName,
			UserGroup:       "default",
			UsingGroup:      "default",
		}
		priceData, err := ModelPriceHelperPerCall(ctx, info)
		require.NoError(t, err)
		return priceData.Quota
	}

	// 无时段规则：¥0.1 × QuotaPerYuan × 1
	require.Equal(t, 100_000_000, perCallQuota(), "前置：常规按次价")

	// 时段内按次价 ¥0.2（= 常规 2 倍）：必须收到 200,000,000
	seedAlwaysOnWindow(t, modelName, `{"per_call":0.2}`)
	require.Equal(t, 200_000_000, perCallQuota(),
		"per_call 时段系数 2.0 必须折进按次价")
}

// input 与 per_call 系数互相独立：input 减半不得把按次价也拖下去。
// 期望语义 = 「该时段按次价的绝对价目 × 常规分组倍率」，即 ¥0.2 × 1。
func TestModelPriceHelperPerCallTimeFactorIndependentOfInput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ratio_setting.InitRatioSettings()

	const modelName = "percall-timefactor-both-test"
	savedPrice := ratio_setting.ModelPrice2JSONString()
	savedRatio := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() {
		_ = ratio_setting.UpdateModelPriceByJSONString(savedPrice)
		_ = ratio_setting.UpdateModelRatioByJSONString(savedRatio)
		_ = ratio_setting.UpdateGroupTimeRatioByJSONString(`{}`)
	})
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(
		`{"percall-timefactor-both-test":0.1}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(
		`{"percall-timefactor-both-test":2}`)) // 输入 ¥2/1M 当 input 分量基准

	ctx, _ := gin.CreateTestContext(nil)
	info := &relaycommon.RelayInfo{
		OriginModelName: modelName,
		UserGroup:       "default",
		UsingGroup:      "default",
	}
	// input 价目 1（系数 0.5）+ per_call 价目 0.2（系数 2.0）
	seedAlwaysOnWindow(t, modelName, `{"input":1,"per_call":0.2}`)

	priceData, err := ModelPriceHelperPerCall(ctx, info)
	require.NoError(t, err)
	// GroupRatio = 1 × f_input(0.5)；ScaleComponent(per_call) = 2.0/0.5 = 4
	// → quota = 0.1 × QPY × 0.5 × 4 = ¥0.2 × QPY，与 input 减半无关
	require.Equal(t, 200_000_000, priceData.Quota,
		"按次价应等于时段绝对价目 × 分组倍率，input 系数不得渗透")
}
