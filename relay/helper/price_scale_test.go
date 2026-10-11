package helper

import (
	"math"
	"testing"

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
