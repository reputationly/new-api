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
// 单位约定：倍率路径的 quota/token = ratio × QuotaScale()，而 common.PreConsumedQuota
// **不是 quota，是 token 数下限**，所以它既不乘 scale、也不参与单位迁移。
// 这条用例把两者一起钉住：
// 若有人给下限也乘了 scale，冻结额会放大 14.6 倍，下面的期望值立刻对不上。
func TestModelPriceHelperPreConsumeScalesRatioNotTokenFloor(t *testing.T) {
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

	// 旧单位：500 tokens（下限） × ratio，四舍五入后与改造前的 int() 截断同值
	common.QuotaPerUnit = 500000
	require.Equal(t, int(math.Round(float64(common.PreConsumedQuota)*modelRatio)), preConsume(500000))

	// 新单位：下限仍是 500 tokens，只有倍率这一步乘了 scale。
	// 期望值显式用 7300000/RatioQuotaBase 算，不读全局——require.Equal 会先算期望值
	// 再算 actual，读全局会读到上一行刚设的旧值。
	const qpuNew = 7300000.0
	require.Equal(t, int(math.Round(float64(common.PreConsumedQuota)*modelRatio*(qpuNew/common.RatioQuotaBase))), preConsume(qpuNew))
}

// 按次价分支（ModelPriceHelperPerCall）此前是 int(price × QPU × group)：
// 运营填 ¥0.1、汇率 7.3 时，落库 0.1/7.3 美元，QPU = 7300000 下算出 99999.9999…，
// 截断会少收 1 quota。
func TestModelPriceHelperPerCallRoundsPerCallPrice(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const modelName = "percall-rounding-test-model"
	saved := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() { _ = ratio_setting.UpdateModelPriceByJSONString(saved) })
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(
		`{"percall-rounding-test-model":0.013698630136986301}`)) // ¥0.1 ÷ 7.3

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

	common.QuotaPerUnit = 7300000
	require.Equal(t, 100000, perCallQuota(7300000), "¥0.1 应按 100000 quota 收取")

	common.QuotaPerUnit = 500000
	require.Equal(t, 6849, perCallQuota(500000))
}

// 按次缺价时的倍率兜底 modelRatio/2 × QuotaPerUnit 属于价格路径（已乘 QPU），
// 不得再乘 scale；同时它也要四舍五入。
func TestModelPriceHelperPerCallRatioFallbackDoesNotDoubleScale(t *testing.T) {
	gin.SetMode(gin.TestMode)
	// relay/helper 的测试二进制不跑 InitResources，倍率表要显式加载默认值。
	ratio_setting.InitRatioSettings()

	const modelName = "gpt-4o" // 有默认倍率、无按次价
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

	common.QuotaPerUnit = 500000
	require.Equal(t, int(math.Round(modelRatio/2*500000)), quota(500000))

	common.QuotaPerUnit = 7300000
	require.Equal(t, int(math.Round(modelRatio/2*7300000)), quota(7300000))
}
