package ratio_setting

import (
	"testing"

	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

// seedRatios 铺设三层配置，并在用例结束后恢复，避免包内测试互相污染。
func seedRatios(t *testing.T, groupRatio, groupGroupRatio, groupModelRatio string) {
	t.Helper()
	require.NoError(t, UpdateGroupRatioByJSONString(groupRatio))
	require.NoError(t, UpdateGroupGroupRatioByJSONString(groupGroupRatio))
	require.NoError(t, UpdateGroupModelRatioByJSONString(groupModelRatio))
	t.Cleanup(func() {
		_ = UpdateGroupRatioByJSONString(`{"default":1}`)
		_ = UpdateGroupGroupRatioByJSONString(`{}`)
		_ = UpdateGroupModelRatioByJSONString(`{}`)
	})
}

// TestResolveGroupRatio_EmptyConfigIsIdentical 是 P0 能安全上线的依据：
// GroupModelRatio 为空时，解析结果必须与改造前逐位相同。
func TestResolveGroupRatio_EmptyConfigIsIdentical(t *testing.T) {
	seedRatios(t, `{"default":1,"premium":1.5}`, `{"vip":{"premium":0.7}}`, `{}`)

	t.Run("仅基础倍率", func(t *testing.T) {
		res := ResolveGroupRatio("default", "premium", "GLM-5")
		require.Equal(t, 1.5, res.Final)
		require.Equal(t, 1.5, res.Base)
		require.False(t, res.HasSpecialRatio)
		require.Empty(t, res.RuleMatch)
	})

	t.Run("命中身份折扣", func(t *testing.T) {
		res := ResolveGroupRatio("vip", "premium", "GLM-5")
		require.Equal(t, 0.7, res.Final)
		require.True(t, res.HasSpecialRatio)
		require.Equal(t, 0.7, res.SpecialRatio)
		require.Empty(t, res.RuleMatch)
	})

	t.Run("未知分组退回1", func(t *testing.T) {
		require.Equal(t, 1.0, ResolveGroupRatio("default", "nonexistent", "GLM-5").Final)
	})
}

// TestResolveGroupRatio_TwoLayersCompose 锁死 docs/group-management-redesign.md §3.2：
// 身份折扣与促销折扣必须**正交叠加**。
//
// 若实现被改成「把两类规则拍平成一个规则集、取最具体的一条」，模型精确匹配会胜过
// 分组级规则、只命中促销那条，得到 1.5 × 0.5 = 0.75——vip 身份被静默丢掉，
// vip 反而比预期贵。下面对 0.75 的显式否定断言就是防这个回归的。
func TestResolveGroupRatio_TwoLayersCompose(t *testing.T) {
	seedRatios(t,
		`{"default":1,"premium":1.5}`,
		`{"vip":{"premium":0.7}}`,
		`{"premium":{"GLM-5":{"mode":"multiply","value":0.5}}}`,
	)

	res := ResolveGroupRatio("vip", "premium", "GLM-5")

	require.InDelta(t, 0.35, res.Final, 1e-9, "身份折扣 0.7 与促销 ×0.5 必须叠加")
	require.Greater(t, 0.75-res.Final, 1e-9,
		"结果等于 0.75 说明两层被拍平成一层、身份折扣被丢弃")

	require.Equal(t, 1.5, res.GroupRatio)
	require.Equal(t, 0.7, res.Base)
	require.Equal(t, "GLM-5", res.RuleMatch)
	require.Equal(t, "multiply", res.RuleMode)

	// 同一条促销对非 vip 用户仍从基础倍率起算
	require.InDelta(t, 0.75, ResolveGroupRatio("default", "premium", "GLM-5").Final, 1e-9)
}

// TestResolveGroupRatio_OverrideDiscardsBase override 是精确定价，
// 与分组基础倍率、身份折扣全部脱钩——这正是它最容易被误用的地方，必须锁住。
func TestResolveGroupRatio_OverrideDiscardsBase(t *testing.T) {
	seedRatios(t,
		`{"premium":1.5}`,
		`{"vip":{"premium":0.7}}`,
		`{"premium":{"GLM-5":{"mode":"override","value":2.2}}}`,
	)

	vip := ResolveGroupRatio("vip", "premium", "GLM-5")
	require.Equal(t, 2.2, vip.Final, "override 无视身份折扣")
	require.Equal(t, 0.7, vip.Base, "但 Base 仍记录被吃掉的身份折扣，供日志与试算器展示")

	require.Equal(t, 2.2, ResolveGroupRatio("default", "premium", "GLM-5").Final)
}

func TestResolveGroupRatio_PatternSpecificity(t *testing.T) {
	seedRatios(t, `{"premium":1}`, `{}`, `{"premium":{
		"wan2.2-*":     {"mode":"multiply","value":0.8},
		"wan2.2-t2v-*": {"mode":"multiply","value":0.6},
		"wan2.2-t2v-a": {"mode":"multiply","value":0.4}
	}}`)

	cases := []struct {
		model string
		want  float64
		rule  string
	}{
		{"wan2.2-i2v", 0.8, "wan2.2-*"},
		{"wan2.2-t2v-b", 0.6, "wan2.2-t2v-*"},
		{"wan2.2-t2v-a", 0.4, "wan2.2-t2v-a"},
		{"wan2.1-t2v", 1, ""},
	}
	for _, c := range cases {
		t.Run(c.model, func(t *testing.T) {
			res := ResolveGroupRatio("default", "premium", c.model)
			require.InDelta(t, c.want, res.Final, 1e-9)
			require.Equal(t, c.rule, res.RuleMatch)
		})
	}
}

// TestResolveGroupRatio_NoModelContext 无模型上下文的调用点（modelName 为空）
// 必须退化成改造前的行为，而不是命中某条通配规则。
func TestResolveGroupRatio_NoModelContext(t *testing.T) {
	seedRatios(t, `{"premium":1.5}`, `{}`, `{"premium":{"*x":{"mode":"multiply","value":0.1}}}`)

	res := ResolveGroupRatio("default", "premium", "")
	require.Equal(t, 1.5, res.Final)
	require.Empty(t, res.RuleMatch)
}

// TestModelRatioRule_BareNumberCompat 手工编辑 JSON 时 {"GLM-5": 0.5} 这种写法
// 要能被接住，按 multiply 处理。
func TestModelRatioRule_BareNumberCompat(t *testing.T) {
	seedRatios(t, `{"premium":2}`, `{}`, `{"premium":{"GLM-5":0.5,"o3":{"value":0.25}}}`)

	glm := ResolveGroupRatio("default", "premium", "GLM-5")
	require.InDelta(t, 1.0, glm.Final, 1e-9)
	require.Equal(t, "multiply", glm.RuleMode)

	// 对象写法省略 mode 时同样默认 multiply
	o3 := ResolveGroupRatio("default", "premium", "o3")
	require.InDelta(t, 0.5, o3.Final, 1e-9)
	require.Equal(t, "multiply", o3.RuleMode)
}

func TestCheckGroupModelRatio(t *testing.T) {
	t.Run("空值放行", func(t *testing.T) {
		require.NoError(t, CheckGroupModelRatio(""))
		require.NoError(t, CheckGroupModelRatio("  "))
	})

	t.Run("合法配置", func(t *testing.T) {
		require.NoError(t, CheckGroupModelRatio(`{"premium":{"GLM-5":{"mode":"override","value":2.2},"wan-*":0.8}}`))
	})

	for name, payload := range map[string]string{
		"中缀通配":   `{"premium":{"gpt-*-mini":{"mode":"multiply","value":0.8}}}`,
		"负值":     `{"premium":{"GLM-5":{"mode":"multiply","value":-1}}}`,
		"未知模式":   `{"premium":{"GLM-5":{"mode":"divide","value":2}}}`,
		"空模型模式":  `{"premium":{"":{"mode":"multiply","value":1}}}`,
		"非法JSON": `{"premium":`,
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, CheckGroupModelRatio(payload))
		})
	}
}

// resolutionLog 走一遍「解析 → 填进 GroupRatioInfo → 生成日志串」的真实路径，
// 而不是直接构造 GroupRatioInfo——后者测不出 relay/helper 那边的字段搬运是否正确。
func resolutionLog(t *testing.T, modelName string) string {
	t.Helper()
	res := ResolveGroupRatio("default", "premium", modelName)
	return types.GroupRatioInfo{
		ModelRuleMatch: res.RuleMatch,
		ModelRuleMode:  res.RuleMode,
		ModelRuleValue: res.RuleValue,
	}.ModelRuleLog()
}

func TestGroupRatioInfoModelRuleLog(t *testing.T) {
	seedRatios(t, `{"premium":1.5}`, `{}`, `{"premium":{
		"GLM-5":    {"mode":"override","value":2.2},
		"wan2.2-*": {"mode":"multiply","value":0.8}
	}}`)

	require.Equal(t, "GLM-5:=2.2", resolutionLog(t, "GLM-5"))
	require.Equal(t, "wan2.2-*:×0.8", resolutionLog(t, "wan2.2-i2v"))
	require.Empty(t, resolutionLog(t, "unmatched-model"))
}
