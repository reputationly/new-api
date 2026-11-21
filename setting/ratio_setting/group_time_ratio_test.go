package ratio_setting

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/require"
)

// shanghai 是测试里所有时刻的基准时区。窗口默认时区就是它，两边一致才能让
// "02:00" 这种字面量在用例里表示它看起来的意思。
var shanghai = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}()

// at 构造 2026-09-14（周一）起的某个时刻。选周一是为了让 weekday 偏移好算：
// +0=周一 ... +4=周五 +5=周六 +6=周日。
func at(t *testing.T, dayOffset, hour, minute int) time.Time {
	t.Helper()
	return time.Date(2026, 9, 14, hour, minute, 0, 0, shanghai).AddDate(0, 0, dayOffset)
}

// seedTimeRatio 铺设时段配置并在用例结束后清空，避免污染包内其他测试。
func seedTimeRatio(t *testing.T, jsonStr string) {
	t.Helper()
	require.NoError(t, UpdateGroupTimeRatioByJSONString(jsonStr))
	t.Cleanup(func() {
		_ = UpdateGroupTimeRatioByJSONString(`{}`)
	})
}

// TestResolveGroupRatio_NoTimeConfigIsIdentical 是这一层能安全上线的依据：
// GroupTimeRatio 为空时，解析结果必须与加入 Layer 4 之前逐位相同。
func TestResolveGroupRatio_NoTimeConfigIsIdentical(t *testing.T) {
	seedRatios(t, `{"default":1,"premium":1.5}`, `{"vip":{"premium":0.7}}`,
		`{"premium":{"GLM-5":{"mode":"multiply","value":0.5}}}`)
	seedTimeRatio(t, `{}`)

	res := ResolveGroupRatioAt("vip", "premium", "GLM-5", at(t, 0, 2, 0))
	require.Equal(t, 0.35, res.Final) // 0.7 × 0.5，与 Layer 4 无关
	require.Empty(t, res.TimeWindow)
	require.Zero(t, res.TimeValue)
}

// TestResolveGroupRatio_TimeLayerStacksOnModelRule 锁死 Layer 4 的核心语义：
// 命中时段时**乘在常规价上**（叠乘），而不是取代模型折扣。
//
// 取代会把模型折扣与 override 锁价一起吃掉，运营配的数与实扣脱钩（§4.1/§4.8）。
// 若实现被改回取代，下面 0.21 的断言会变成 0.42。
func TestResolveGroupRatio_TimeLayerStacksOnModelRule(t *testing.T) {
	seedRatios(t, `{"default":1,"premium":1.5}`, `{"vip":{"premium":0.7}}`,
		`{"premium":{"GLM-5":{"mode":"multiply","value":0.5}}}`)
	seedTimeRatio(t, `{
		"windows": {"night": {"label":"深夜档","start":"00:00","end":"08:00"}},
		"rules":   {"premium": {"GLM-5": [{"window":"night","value":0.6}]}}
	}`)

	t.Run("时段内取代模型折扣", func(t *testing.T) {
		res := ResolveGroupRatioAt("vip", "premium", "GLM-5", at(t, 0, 2, 0))
		// Base 0.7（身份折扣覆盖了分组倍率 1.5）× 模型折扣 0.5 × 时段系数 0.6
		require.InDelta(t, 0.21, res.Final, 1e-9)
		require.NotEqual(t, 0.42, res.Final) // 取代语义的回归哨兵
		require.Equal(t, "night", res.TimeWindow)
		require.Equal(t, "深夜档", res.TimeLabel)
		require.Equal(t, 0.6, res.TimeValue)
	})

	t.Run("时段外不打折", func(t *testing.T) {
		res := ResolveGroupRatioAt("vip", "premium", "GLM-5", at(t, 0, 10, 0))
		require.InDelta(t, 0.35, res.Final, 1e-9)
		require.Empty(t, res.TimeWindow)
	})

	t.Run("override 锁价同样被叠乘，不再被吃掉", func(t *testing.T) {
		require.NoError(t, UpdateGroupModelRatioByJSONString(
			`{"premium":{"GLM-5":{"mode":"override","value":2.2}}}`))
		res := ResolveGroupRatioAt("vip", "premium", "GLM-5", at(t, 0, 2, 0))
		// 锁价 2.2 × 时段系数 0.6（取代语义会算成 Base 0.7 × 0.6 = 0.42）
		require.InDelta(t, 1.32, res.Final, 1e-9)
	})
}

// TestResolveGroupRatio_TimeLayerKeepsUserTier 用户档折扣必须排在时段折扣之后。
//
// 放在前面会被 Layer 4 的取代吃掉——企业客户的档位优惠每天夜里静默消失 8 小时，
// 而日志上只有一个最终倍率，反算不出是哪一层拍的板。
func TestResolveGroupRatio_TimeLayerKeepsUserTier(t *testing.T) {
	seedRatios(t, `{"default":1}`, `{}`, `{"default":{"GLM-5":{"mode":"multiply","value":0.8}}}`)
	require.NoError(t, UpdateUserGroupModelRatioByJSONString(
		`{"corp":{"GLM-5":{"mode":"multiply","value":0.9}}}`))
	t.Cleanup(func() { _ = UpdateUserGroupModelRatioByJSONString(`{}`) })
	seedTimeRatio(t, `{
		"windows": {"night": {"label":"深夜档","start":"00:00","end":"08:00"}},
		"rules":   {"default": {"GLM-5": [{"window":"night","value":0.56}]}}
	}`)

	busy := ResolveGroupRatioAt("corp", "default", "GLM-5", at(t, 0, 10, 0))
	require.InDelta(t, 0.72, busy.Final, 1e-9) // 1 × 0.8 × 0.9

	idle := ResolveGroupRatioAt("corp", "default", "GLM-5", at(t, 0, 2, 0))
	require.InDelta(t, 0.4032, idle.Final, 1e-9) // 1 × 0.8 × 0.56 × 0.9，档位折扣仍在
	require.NotEqual(t, 0.56, idle.Final)        // 档位折扣被吃掉的回归哨兵
	require.Equal(t, 0.9, idle.UserRuleValue)
}

// TestTimeWindow_Boundaries 锁死区间是左闭右开 [start, end)。
// 起止那一分钟归谁，是配置里看不出来、错了也没人发现的那类边界。
func TestTimeWindow_Boundaries(t *testing.T) {
	win := TimeWindow{Start: "00:00", End: "08:00"}

	require.True(t, win.ActiveAt(at(t, 0, 0, 0)), "起点闭区间")
	require.True(t, win.ActiveAt(at(t, 0, 7, 59)))
	require.False(t, win.ActiveAt(at(t, 0, 8, 0)), "终点开区间")
	require.False(t, win.ActiveAt(at(t, 0, 23, 59)))
}

// TestTimeWindow_CrossMidnight 跨午夜窗口按**起始日**判定生效日。
//
// 另一种读法（按当前日判定）会让「工作日 22:00→06:00」在周六 00:00 突然断档，
// 而配置上完全看不出来。这里用周五夜锁死正确那种。
func TestTimeWindow_CrossMidnight(t *testing.T) {
	t.Run("每天", func(t *testing.T) {
		win := TimeWindow{Start: "22:00", End: "06:00"}
		require.True(t, win.ActiveAt(at(t, 0, 22, 0)))
		require.True(t, win.ActiveAt(at(t, 1, 2, 0)))
		require.False(t, win.ActiveAt(at(t, 1, 6, 0)))
		require.False(t, win.ActiveAt(at(t, 0, 12, 0)))
	})

	t.Run("工作日按起始日判定", func(t *testing.T) {
		// days 1-5 = 周一至周五
		win := TimeWindow{Start: "22:00", End: "06:00", Days: []int{1, 2, 3, 4, 5}}

		// 周五 23:00 起始 → 生效
		require.True(t, win.ActiveAt(at(t, 4, 23, 0)))
		// 周六 02:00 仍属周五那一段 → 生效
		require.True(t, win.ActiveAt(at(t, 5, 2, 0)))
		// 周六 23:00 起始日是周六，不在 days 里 → 不生效
		require.False(t, win.ActiveAt(at(t, 5, 23, 0)))
		// 周日 02:00 起始日是周六 → 不生效
		require.False(t, win.ActiveAt(at(t, 6, 2, 0)))
		// 周一 02:00 起始日是周日 → 不生效
		require.False(t, win.ActiveAt(at(t, 7, 2, 0)))
	})
}

// TestTimeWindow_Timezone 窗口时刻按自己的时区解释，不受进程时区影响。
func TestTimeWindow_Timezone(t *testing.T) {
	win := TimeWindow{Start: "00:00", End: "08:00", TZ: "UTC"}

	// 北京 02:00 = UTC 前一天 18:00，不在 UTC 的 00:00-08:00 里
	require.False(t, win.ActiveAt(at(t, 0, 2, 0)))
	// 北京 10:00 = UTC 02:00，在窗口里
	require.True(t, win.ActiveAt(at(t, 0, 10, 0)))
}

// TestTimeWindow_NextBoundary 模型广场靠这个值渲染「至 08:00」。
func TestTimeWindow_NextBoundary(t *testing.T) {
	t.Run("时段内返回结束时刻", func(t *testing.T) {
		win := TimeWindow{Start: "00:00", End: "08:00"}
		next, ok := win.NextBoundary(at(t, 0, 2, 0))
		require.True(t, ok)
		require.True(t, next.Equal(at(t, 0, 8, 0)), "got %s", next)
	})

	t.Run("时段外返回下次开始时刻", func(t *testing.T) {
		win := TimeWindow{Start: "00:00", End: "08:00"}
		next, ok := win.NextBoundary(at(t, 0, 10, 0))
		require.True(t, ok)
		require.True(t, next.Equal(at(t, 1, 0, 0)), "got %s", next)
	})

	t.Run("跨周末的工作日窗口", func(t *testing.T) {
		win := TimeWindow{Start: "00:00", End: "08:00", Days: []int{1, 2, 3, 4, 5}}
		// 周五 10:00 之后，下一次生效是周一 00:00（跳过周六周日）
		next, ok := win.NextBoundary(at(t, 4, 10, 0))
		require.True(t, ok)
		require.True(t, next.Equal(at(t, 7, 0, 0)), "got %s", next)
	})
}

// TestPickTimeRule_Specificity 时段规则的模式串匹配必须与 Layer 2/3 同一套
// （精确 > 前缀通配 > 长前缀优先）——它们共用 pickRuleFrom，这里锁死不被改回各写一份。
func TestPickTimeRule_Specificity(t *testing.T) {
	seedRatios(t, `{"default":1}`, `{}`, `{}`)
	seedTimeRatio(t, `{
		"windows": {"night": {"start":"00:00","end":"08:00"}},
		"rules": {"default": {
			"wan2.2-*":     [{"window":"night","value":0.8}],
			"wan2.2-t2v-*": [{"window":"night","value":0.7}],
			"wan2.2-t2v-plus": [{"window":"night","value":0.6}]
		}}
	}`)

	night := at(t, 0, 2, 0)
	require.InDelta(t, 0.6, ResolveGroupRatioAt("default", "default", "wan2.2-t2v-plus", night).Final, 1e-9)
	require.InDelta(t, 0.7, ResolveGroupRatioAt("default", "default", "wan2.2-t2v-lite", night).Final, 1e-9)
	require.InDelta(t, 0.8, ResolveGroupRatioAt("default", "default", "wan2.2-i2v-x", night).Final, 1e-9)
	require.InDelta(t, 1.0, ResolveGroupRatioAt("default", "default", "hunyuan-video", night).Final, 1e-9)
}

// TestPickTimeRule_OverlapTakesLowest 手改 JSON 绕过保存校验时的运行时兜底：
// 多窗口同时生效取最小值。结果确定，且方向对用户有利。
func TestPickTimeRule_OverlapTakesLowest(t *testing.T) {
	seedRatios(t, `{"default":1}`, `{}`, `{}`)
	seedTimeRatio(t, `{
		"windows": {
			"a": {"start":"00:00","end":"08:00"},
			"b": {"start":"02:00","end":"04:00"}
		},
		"rules": {"default": {"m": [
			{"window":"a","value":0.8},
			{"window":"b","value":0.5}
		]}}
	}`)

	require.InDelta(t, 0.5, ResolveGroupRatioAt("default", "default", "m", at(t, 0, 3, 0)).Final, 1e-9)
	require.InDelta(t, 0.8, ResolveGroupRatioAt("default", "default", "m", at(t, 0, 6, 0)).Final, 1e-9)
}

// TestResolveGroupRatio_EmptyModelNameMissesTimeLayer 无模型上下文的调用点
// （modelName 传空）必须与改造前逐位相同。
func TestResolveGroupRatio_EmptyModelNameMissesTimeLayer(t *testing.T) {
	seedRatios(t, `{"default":1}`, `{}`, `{}`)
	seedTimeRatio(t, `{
		"windows": {"night": {"start":"00:00","end":"08:00"}},
		"rules":   {"default": {"*": [{"window":"night","value":0.5}]}}
	}`)

	res := ResolveGroupRatioAt("default", "default", "", at(t, 0, 2, 0))
	require.Equal(t, 1.0, res.Final)
	require.Empty(t, res.TimeWindow)
}

func TestCheckGroupTimeRatio(t *testing.T) {
	validWindows := `"windows": {"night": {"start":"00:00","end":"08:00"}}`

	cases := []struct {
		name    string
		json    string
		wantErr string
	}{
		{"空配置", ``, ""},
		{"空对象", `{}`, ""},
		{
			"合法配置",
			`{` + validWindows + `, "rules":{"default":{"wan2.2-*":[{"window":"night","value":0.7}]}}}`,
			"",
		},
		{
			"悬空的模板引用",
			`{` + validWindows + `, "rules":{"default":{"m":[{"window":"noon","value":0.7}]}}}`,
			"unknown window",
		},
		{
			// 系数乘在常规价上，>1 是紧缺时段加价，合法
			"系数大于1（加价）",
			`{` + validWindows + `, "rules":{"default":{"m":[{"window":"night","value":1.2}]}}}`,
			"",
		},
		{
			"系数为0",
			`{` + validWindows + `, "rules":{"default":{"m":[{"window":"night","value":0}]}}}`,
			"至少要配一个分量价目",
		},
		{
			"中缀通配",
			`{` + validWindows + `, "rules":{"default":{"wan*plus":[{"window":"night","value":0.7}]}}}`,
			"trailing wildcard",
		},
		{
			"起止相同",
			`{"windows":{"x":{"start":"08:00","end":"08:00"}}}`,
			"must differ",
		},
		{
			"非法钟点",
			`{"windows":{"x":{"start":"25:00","end":"08:00"}}}`,
			"invalid hour",
		},
		{
			// 放行的话 "9:00" > "18:00" 按字符比较为真，前端会把它渲染成跨午夜，
			// 而后端按分钟算根本不跨——同一份配置两边结论不同
			"小时未补零",
			`{"windows":{"x":{"start":"9:00","end":"18:00"}}}`,
			"expect HH:MM",
		},
		{
			"分钟未补零",
			`{"windows":{"x":{"start":"09:0","end":"18:00"}}}`,
			"expect HH:MM",
		},
		{
			"非法星期",
			`{"windows":{"x":{"start":"00:00","end":"08:00","days":[7]}}}`,
			"weekday must be 0-6",
		},
		{
			"窗口重叠",
			`{"windows":{"a":{"start":"00:00","end":"08:00"},"b":{"start":"02:00","end":"04:00"}},
			  "rules":{"default":{"m":[{"window":"a","value":0.8},{"window":"b","value":0.5}]}}}`,
			"overlap",
		},
		{
			"跨午夜与次日晨段重叠",
			`{"windows":{"a":{"start":"22:00","end":"06:00"},"b":{"start":"05:00","end":"07:00"}},
			  "rules":{"default":{"m":[{"window":"a","value":0.8},{"window":"b","value":0.5}]}}}`,
			"overlap",
		},
		{
			"不同日不算重叠",
			`{"windows":{"a":{"start":"00:00","end":"08:00","days":[1]},"b":{"start":"02:00","end":"04:00","days":[2]}},
			  "rules":{"default":{"m":[{"window":"a","value":0.8},{"window":"b","value":0.5}]}}}`,
			"",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckGroupTimeRatio(tc.json)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tc.wantErr)
		})
	}
}

// 端到端口径一致性：同一份配置，计费终值 / 展示视图 / 日志标签必须指向同一个数。
// 三者各自算一遍是本次改造最容易出的错——分开时都对，合起来对不上。
func TestTimeRatio_EndToEndConsistency(t *testing.T) {
	seedRatios(t, `{"default":1,"premium":1.5}`, `{}`,
		`{"premium":{"wan2.2-*":{"mode":"multiply","value":0.8}}}`)
	seedTimeRatio(t, `{
		"windows": {"night": {"label":"深夜档","start":"00:00","end":"08:00"}},
		"rules":   {"premium": {"wan2.2-*": [{"window":"night","value":0.5}]}}
	}`)

	night := time.Date(2026, 9, 14, 2, 0, 0, 0, shanghai)
	day := time.Date(2026, 9, 14, 10, 0, 0, 0, shanghai)

	// 展示侧与计费侧共用同一次解析的 Base 与用户档乘数，这里照 controller/pricing.go
	// 的用法传，任一侧改了口径这个测试就会红。
	viewAt := func(t *testing.T, at time.Time) (TimeRatioView, bool, RatioResolution) {
		t.Helper()
		res := ResolveGroupRatioAt("default", "premium", "wan2.2-t2v-plus", at)
		view, ok := ResolveTimeRatioView("premium", "wan2.2-t2v-plus", at, res)
		return view, ok, res
	}

	t.Run("空闲时段", func(t *testing.T) {
		view, ok, res := viewAt(t, night)
		require.InDelta(t, 0.6, res.Final, 1e-9, "1.5 × 0.8 × 0.5，时段系数叠乘在模型折扣上")
		require.True(t, ok)
		require.True(t, view.Active)
		// 标出来的那一档必须就是计费用的那一档：详情页靠它渲染「进行中」，
		// 标错等于告诉用户他在享受一个并没有生效的折扣
		require.True(t, view.Windows[0].Active)
		// 展示的时段终值必须**就是**计费用的那个数。这条是整个展示链路的锚：
		// 广场标「空闲时段 N 折」用的就是 Ratio，与 res.Final 分叉两边都不报错。
		require.InDelta(t, res.Final, view.Windows[0].Ratio, 1e-9)
		require.InDelta(t, res.Final, view.BestRatio, 1e-9)
		require.Equal(t, "2026-09-14T08:00:00+08:00", view.Until)
	})

	t.Run("高峰时段", func(t *testing.T) {
		view, ok, res := viewAt(t, day)
		require.InDelta(t, 1.2, res.Final, 1e-9, "1.5 × 0.8，走常规模型折扣")
		require.Empty(t, res.TimeWindow)
		require.True(t, ok, "高峰时段也要下发，否则前端走 fallback 显示原价")
		require.False(t, view.Active)
		require.False(t, view.Windows[0].Active)
		// 高峰时段靠 BestRatio 渲染「深夜档 7.5 折」——绝对值，用户不用心算
		require.InDelta(t, 0.6, view.BestRatio, 1e-9)
		// 档位名必须一并下发：角标硬编码「空闲时段」时，只配了一个更贵的高峰档
		// 会显示成「空闲时段 N 折」，标签指向错的时段
		require.Equal(t, "深夜档", view.BestLabel)
		// 未命中时段时的倍率必须一并下发：详情页靠它补出「其余时段」那一行，
		// 否则表里只有打折的那几档，用户看不出其余时间是多少
		require.InDelta(t, res.NormalRatio, view.NormalRatio, 1e-9)
		require.InDelta(t, 1.2, view.NormalRatio, 1e-9, "1.5 × 0.8，模型折扣那一档")
		require.Equal(t, "2026-09-15T00:00:00+08:00", view.Until)
	})

	t.Run("没配时段的分组不受影响", func(t *testing.T) {
		res := ResolveGroupRatioAt("default", "default", "wan2.2-t2v-plus", night)
		require.InDelta(t, 1.0, res.Final, 1e-9)
		_, ok := ResolveTimeRatioView("default", "wan2.2-t2v-plus", night, res)
		require.False(t, ok)
	})
}

// TestResolveGroupRatio_TimeRuleClearsModelRuleTrace 时段规则取代模型折扣后，
// 必须把 Layer 2 的命中痕迹一并清掉。
//
// 留着的话日志里会写出一条**没有生效**的 group_model_rule，运营拿
// group_base_ratio × 规则值 反算得到的数与 group_ratio 对不上——日志的分层
// 必须永远自洽，这是 service/log_info_generate.go 明令不能破的不变式。
func TestResolveGroupRatio_TimeRuleKeepsModelRuleTrace(t *testing.T) {
	seedRatios(t, `{"default":1}`, `{}`,
		`{"default":{"GLM-5":{"mode":"multiply","value":0.8,"remark":"常规"}}}`)
	seedTimeRatio(t, `{
		"windows": {"night": {"label":"深夜档","start":"00:00","end":"08:00"}},
		"rules":   {"default": {"GLM-5": [{"window":"night","value":0.56}]}}
	}`)

	busy := ResolveGroupRatioAt("default", "default", "GLM-5", at(t, 0, 10, 0))
	require.Equal(t, "GLM-5", busy.RuleMatch, "高峰时段走模型折扣，痕迹要留着")

	idle := ResolveGroupRatioAt("default", "default", "GLM-5", at(t, 0, 2, 0))
	require.InDelta(t, 0.448, idle.Final, 1e-9) // 0.8 × 0.56
	require.Equal(t, "GLM-5", idle.RuleMatch, "叠乘语义下模型折扣生效，痕迹必须留着")
	require.Equal(t, "multiply", idle.RuleMode)
	require.Equal(t, 0.8, idle.RuleValue)
	require.InDelta(t, idle.Final, idle.Base*idle.RuleValue*idle.TimeValue, 1e-9)
	// 日志的反算链路：base × 规则值 × 时段系数 = group_ratio，三者自洽
	require.Equal(t, "深夜档:×0.56", types.GroupRatioInfo{
		TimeWindow: idle.TimeWindow,
		TimeLabel:  idle.TimeLabel,
		TimeValue:  idle.TimeValue,
	}.TimeRuleLog())
}

// TestResolveTimeRatioView_NoBadgeWhenActiveWindowIsFullPrice 校验允许配 ×1
// 的占位档，命中它时不能标「进行中」——那个青色角标会和旁边没打折的价格自相矛盾。
func TestResolveTimeRatioView_NoBadgeWhenActiveWindowIsFullPrice(t *testing.T) {
	seedRatios(t, `{"default":1}`, `{}`, `{}`)
	seedTimeRatio(t, `{
		"windows": {
			"evening": {"label":"傍晚档","start":"18:00","end":"22:00"},
			"deep":    {"label":"深夜档","start":"00:00","end":"06:00"}
		},
		"rules": {"default": {"m": [
			{"window":"evening","value":1},
			{"window":"deep","value":0.5}
		]}}
	}`)

	// 20:00 命中 ×1 的傍晚档：有折扣的档位存在（深夜 0.5），但此刻并不打折
	res := ResolveGroupRatioAt("default", "default", "m", at(t, 0, 20, 0))
	view, ok := ResolveTimeRatioView("default", "m", at(t, 0, 20, 0), res)
	require.True(t, ok)
	require.InDelta(t, 1.0, res.Final, 1e-9, "×1 不改变价格")
	require.False(t, view.Active, "此刻没打折，不该出优惠角标")
	require.InDelta(t, 0.5, view.BestRatio, 1e-9, "但仍要告诉用户深夜档有 5 折")

	// 角标不出，不等于「不在任何时段里」：×1 那一档正是此刻的计费档，详情表必须
	// 把它标成当前行。两者共用一个字段时，表里会没有任何窗口行生效、而「其余时段」
	// 行被标成当前——用户看到的当前价是模型折扣价，实扣却是原价。
	require.Equal(t, "傍晚档", view.Label, "此刻所处的档位仍要给出")
	charged := make([]string, 0, 1)
	for _, w := range view.Windows {
		if w.Active {
			charged = append(charged, w.Label)
		}
	}
	require.Equal(t, []string{"傍晚档"}, charged,
		"×1 的原价档也是计费档，必须标出来")

	// 02:00 命中真正打折的深夜档
	res = ResolveGroupRatioAt("default", "default", "m", at(t, 0, 2, 0))
	view, _ = ResolveTimeRatioView("default", "m", at(t, 0, 2, 0), res)
	require.True(t, view.Active)
	require.Equal(t, "深夜档", view.Label)
}

// TestTimeRatio_TwoWindowsSameTier 覆盖「一个业务档位拆成两个模板」这种配法。
//
// 「工作日高峰」在业务上是一个档位，但它是 09:00-12:00 与 14:00-18:00 两段
// （午休不算高峰）。一个模板只能表示一个连续区间，所以要建两个模板、在规则里
// 各绑一条、都填同一个倍率。这条路径的计费与下发数据必须与单模板一样准确——
// 它是当前唯一能表达这类档位的方式，不能只在单区间的用例上验过就算数。
func TestTimeRatio_TwoWindowsSameTier(t *testing.T) {
	// 常规倍率 0.35（未命中时段时走它），高峰两段都是 0.5
	seedRatios(t, `{"default":1}`, `{}`,
		`{"default":{"m":{"mode":"multiply","value":0.35}}}`)
	seedTimeRatio(t, `{
		"windows": {
			"am": {"label":"上午工作时间","start":"09:00","end":"12:00","days":[1,2,3,4,5]},
			"pm": {"label":"下午工作时间","start":"14:00","end":"18:00","days":[1,2,3,4,5]}
		},
		"rules": {"default": {"m": [
			{"window":"am","value":0.5},
			{"window":"pm","value":0.5}
		]}}
	}`)

	// 两段不重叠，保存校验必须放行
	require.NoError(t, CheckGroupTimeRatio(GroupTimeRatio2JSONString()))

	billing := []struct {
		name  string
		at    time.Time
		final float64
	}{
		{"周一 上午高峰", at(t, 0, 10, 0), 0.175}, // 0.35 × 0.5
		{"周一 午休", at(t, 0, 13, 0), 0.35},
		{"周一 下午高峰", at(t, 0, 15, 0), 0.175},
		{"周一 晚间", at(t, 0, 20, 0), 0.35},
		{"周一 高峰起点", at(t, 0, 9, 0), 0.175},
		{"周一 高峰终点(开区间)", at(t, 0, 12, 0), 0.35},
		{"周六 同一钟点", at(t, 5, 10, 0), 0.35},
		{"周日 同一钟点", at(t, 6, 15, 0), 0.35},
	}
	for _, c := range billing {
		t.Run("计费/"+c.name, func(t *testing.T) {
			require.InDelta(t, c.final,
				ResolveGroupRatioAt("default", "default", "m", c.at).Final, 1e-9)
		})
	}

	viewAt := func(t *testing.T, ts time.Time) TimeRatioView {
		t.Helper()
		res := ResolveGroupRatioAt("default", "default", "m", ts)
		v, ok := ResolveTimeRatioView("default", "m", ts,
			res)
		require.True(t, ok)
		// 下发给广场的每一档终值，必须就是那个时刻真正会扣的数
		require.InDelta(t, 0.35, v.NormalRatio, 1e-9)
		require.Len(t, v.Windows, 2)
		for _, w := range v.Windows {
			require.InDelta(t, 0.175, w.Ratio, 1e-9) // 常规价 0.35 × 时段系数 0.5
		}
		return v
	}

	t.Run("展示/上午高峰进行中", func(t *testing.T) {
		v := viewAt(t, at(t, 0, 10, 0))
		require.True(t, v.Active)
		require.Equal(t, "上午工作时间", v.Label)
		require.True(t, v.Windows[0].Active)
		require.False(t, v.Windows[1].Active, "同一时刻只能有一档在进行中")
		// 下一次状态翻转是上午档结束
		require.Equal(t, at(t, 0, 12, 0).Format(time.RFC3339), v.Until)
	})

	t.Run("展示/午休时下一档是下午", func(t *testing.T) {
		v := viewAt(t, at(t, 0, 13, 0))
		require.False(t, v.Active)
		require.False(t, v.Windows[0].Active)
		require.False(t, v.Windows[1].Active)
		require.Equal(t, at(t, 0, 14, 0).Format(time.RFC3339), v.Until)
	})

	t.Run("展示/下午高峰进行中", func(t *testing.T) {
		v := viewAt(t, at(t, 0, 15, 0))
		require.True(t, v.Active)
		require.Equal(t, "下午工作时间", v.Label)
		require.False(t, v.Windows[0].Active)
		require.True(t, v.Windows[1].Active)
		require.Equal(t, at(t, 0, 18, 0).Format(time.RFC3339), v.Until)
	})

	t.Run("展示/周五晚跳过周末到周一", func(t *testing.T) {
		v := viewAt(t, at(t, 4, 20, 0))
		require.False(t, v.Active)
		require.Equal(t, at(t, 7, 9, 0).Format(time.RFC3339), v.Until)
	})

	t.Run("展示/最优档是高峰价且两档同价", func(t *testing.T) {
		v := viewAt(t, at(t, 0, 20, 0))
		// 高峰比常规贵，BestRatio 仍取时段里的最优（最小）值——它回答的是
		// 「这些时段里最便宜的是多少」，与「比常规便宜吗」是两个问题，
		// 后者由前端拿 normal_ratio 比较后决定角标用不用优惠色
		require.InDelta(t, 0.175, v.BestRatio, 1e-9)
		require.NotEmpty(t, v.BestLabel)
	})
}

// TestResolveTimeRatioView_OverlapMarksOnlyChargedWindow 重叠时只标计费实际取用的
// 那一档。
//
// 保存校验会拒绝重叠配置，所以这条只在手改 JSON 绕过校验时才会走到——但那正是
// activeIdx 那段逻辑存在的理由。不测它的话，「把每个命中的档都标成进行中」这种
// 改动不会被任何用例拦住（两个不重叠的档永不同时生效，用它们做断言是空的），
// 而表现是详情页同时标出两个「进行中」，其中一个是用户并没有在享受的价。
func TestResolveTimeRatioView_OverlapMarksOnlyChargedWindow(t *testing.T) {
	seedRatios(t, `{"default":1}`, `{}`, `{}`)
	seedTimeRatio(t, `{
		"windows": {
			"wide":   {"label":"宽档","start":"00:00","end":"08:00"},
			"narrow": {"label":"窄档","start":"02:00","end":"04:00"}
		},
		"rules": {"default": {"m": [
			{"window":"wide","value":0.8},
			{"window":"narrow","value":0.5}
		]}}
	}`)

	ts := at(t, 0, 3, 0) // 两档同时生效
	res := ResolveGroupRatioAt("default", "default", "m", ts)
	require.InDelta(t, 0.5, res.Final, 1e-9, "计费取最小值")

	v, ok := ResolveTimeRatioView("default", "m", ts,
		res)
	require.True(t, ok)
	require.True(t, v.Active)
	require.Equal(t, "窄档", v.Label)

	active := make([]string, 0, 1)
	for _, w := range v.Windows {
		if w.Active {
			active = append(active, w.Label)
		}
	}
	require.Equal(t, []string{"窄档"}, active,
		"只有计费实际取用的那一档能标「进行中」")
}

// ── 下面是 v1 独有的用例（叠乘语义、全网作用域、现网换算值） ──────────────────────
// 这个文件同时覆盖叠乘语义、全网作用域与现网换算值，
// 同一条规则在两种代码上算出同一个价。上面那批用例继续钉住旧语义，这里钉住新语义。

func TestResolveGroupRatio_TimeLayerStacksAndKeepsTrace(t *testing.T) {
	seedRatios(t, `{"default":1,"premium":1.5}`, `{"vip":{"premium":0.7}}`,
		`{"premium":{"GLM-5":{"mode":"multiply","value":0.5}}}`)
	seedTimeRatio(t, `{
		"windows": {"peak": {"label":"紧缺时段","start":"09:00","end":"12:00"}},
		"rules":   {"premium": {"GLM-5": [{"window":"peak","value":1.1111}]}}
	}`)

	res := ResolveGroupRatioAt("vip", "premium", "GLM-5", at(t, 0, 10, 0))
	// 常规价 = Base 0.7 × 模型折扣 0.5 = 0.35，再乘时段系数 1.1111
	require.InDelta(t, 0.35*1.1111, res.Final, 1e-9)
	require.InDelta(t, 0.35, res.NormalRatio, 1e-9)
	// 新语义下模型折扣确实生效了，日志痕迹必须留着才能反算
	require.Equal(t, "GLM-5", res.RuleMatch)
	require.InDelta(t, 0.5, res.RuleValue, 1e-9)
}

func TestResolveGroupRatio_TimeLayerDoesNotEatOverride(t *testing.T) {
	seedRatios(t, `{"default":1}`, `{}`,
		`{"default":{"GLM-5":{"mode":"override","value":2.2}}}`)
	seedTimeRatio(t, `{
		"windows": {"night": {"label":"空闲时段","start":"00:00","end":"08:00"}},
		"rules":   {"default": {"GLM-5": [{"window":"night","value":0.6}]}}
	}`)

	res := ResolveGroupRatioAt("default", "default", "GLM-5", at(t, 0, 2, 0))
	require.InDelta(t, 1.32, res.Final, 1e-9) // 锁价 2.2 × 0.6（旧语义会算成 1.0 × 0.6）
	require.NotEqual(t, 0.6, res.Final)
}

// TestResolveGroupRatio_TimePricePremiumKeepsPrice（现网用例 §10.2）锁死「紧缺时段加价」：
// 模型定价 0.45（空闲价 4.5 折）+ 时段系数 0.5/0.45 = 1.1111，工作时段必须仍是 0.5。
func TestResolveGroupRatio_TimePricePremiumKeepsPrice(t *testing.T) {
	seedRatios(t, `{"default":1}`, `{}`,
		`{"default":{"deepseek-v4-1-flash":{"mode":"multiply","value":0.45}}}`)
	seedTimeRatio(t, `{
		"windows": {"am": {"label":"工作时间（上午）","start":"09:00","end":"12:00","days":[1,2,3,4,5]}},
		"rules":   {"default": {"deepseek-v4-1-flash": [{"window":"am","value":1.1111}]}}
	}`)

	busy := ResolveGroupRatioAt("default", "default", "deepseek-v4-1-flash", at(t, 0, 10, 0))
	require.InDelta(t, 0.5, busy.Final, 2e-4) // 0.45 × 1.1111
	require.InDelta(t, 0.45, busy.NormalRatio, 1e-9)
	idle := ResolveGroupRatioAt("default", "default", "deepseek-v4-1-flash", at(t, 0, 2, 0))
	require.InDelta(t, 0.45, idle.Final, 1e-9)
	require.Empty(t, idle.TimeWindow)
}

// TestTimeRatio_GlobalScope 锁死 v1 的作用域：规则挂在 "*" 上对全网生效，
// 按线路存的规则（存量）优先，且是整组取代而不是合并。
func TestTimeRatio_GlobalScope(t *testing.T) {
	seedRatios(t, `{"default":1,"vip001":1}`, `{}`, `{}`)
	seedTimeRatio(t, `{
		"windows": {"am": {"label":"工作时间","start":"09:00","end":"12:00","days":[1,2,3,4,5]}},
		"rules":   {"*": {"m": [{"window":"am","value":1.1111}]}}
	}`)

	global := ResolveGroupRatioAt("default", "vip001", "m", at(t, 0, 10, 0))
	require.InDelta(t, 1.1111, global.Final, 1e-9)
	require.Equal(t, "am", global.TimeWindow)

	seedTimeRatio(t, `{
		"windows": {"am": {"label":"工作时间","start":"09:00","end":"12:00","days":[1,2,3,4,5]}},
		"rules":   {"vip001": {"m": [{"window":"am","value":0.5}]}, "*": {"m": [{"window":"am","value":1.1111}]}}
	}`)
	line := ResolveGroupRatioAt("default", "vip001", "m", at(t, 0, 10, 0))
	require.InDelta(t, 0.5, line.Final, 1e-9, "线路级规则优先")
	other := ResolveGroupRatioAt("default", "default", "m", at(t, 0, 10, 0))
	require.InDelta(t, 1.1111, other.Final, 1e-9, "其他线路仍走全网规则")

	require.True(t, HasGroupTimeRules("default"), "全网规则也算「有线规则」，否则广场会漏掉整条线路")
	seedTimeRatio(t, `{}`)
	require.False(t, HasGroupTimeRules("default"))
}

func TestCheckGroupTimeRatio_ValueRange(t *testing.T) {
	// 系数乘在常规价上：>1 是紧缺时段加价，上限 20；0 与负数仍拒绝。
	ok := `{"windows":{"am":{"start":"09:00","end":"12:00"}},"rules":{"default":{"m":[{"window":"am","value":1.1111}]}}}`
	require.NoError(t, CheckGroupTimeRatio(ok))
	tooBig := `{"windows":{"am":{"start":"09:00","end":"12:00"}},"rules":{"default":{"m":[{"window":"am","value":21}]}}}`
	require.ErrorContains(t, CheckGroupTimeRatio(tooBig), "value must be in (0, 20]")
	zero := `{"windows":{"am":{"start":"09:00","end":"12:00"}},"rules":{"default":{"m":[{"window":"am","value":0}]}}}`
	require.ErrorContains(t, CheckGroupTimeRatio(zero), "至少要配一个分量价目")
}

// TestResolveGroupRatio_OutputFactorIndependent 锁死「分时段价目不等比例变化」。
//
// 忙时输入 2/输出 8、闲时输入 1/输出 7：输入系数 0.5、输出系数 0.875。
// GroupRatio（输入侧）只乘 0.5，输出侧的差额靠 CompletionRatioScale 折进补全倍率。
func TestResolveGroupRatio_OutputFactorIndependent(t *testing.T) {
	seedRatios(t, `{"default":1}`, `{}`, `{}`)
	seedTimeRatio(t, `{
		"windows": {"night": {"label":"闲时","start":"00:00","end":"08:00"}},
		"rules":   {"*": {"m": [{"window":"night","value":0.5,"output_factor":0.875}]}}
	}`)

	night := ResolveGroupRatioAt("default", "default", "m", at(t, 0, 2, 0))
	require.InDelta(t, 0.5, night.Final, 1e-9, "输入侧系数")
	require.InDelta(t, 0.5, night.TimeValue, 1e-9)
	require.InDelta(t, 0.875, night.TimeFactors["output"], 1e-9, "输出侧系数独立")
	// 折算：输出价倍率 = Final × 补全倍率 × (0.875/0.5)
	require.InDelta(t, 1.75, types.ScaleComponent("output", night.TimeFactors), 1e-12)

	// 没配 output_factor 时跟随输入侧（旧配置逐位不变）
	seedTimeRatio(t, `{
		"windows": {"night": {"label":"闲时","start":"00:00","end":"08:00"}},
		"rules":   {"*": {"m": [{"window":"night","value":0.5}]}}
	}`)
	same := ResolveGroupRatioAt("default", "default", "m", at(t, 0, 2, 0))
	require.InDelta(t, 1.0, types.ScaleComponent("output", same.TimeFactors), 1e-9)
	require.Equal(t, 1.0, types.ScaleComponent("output", same.TimeFactors))
	seedTimeRatio(t, `{}`)
}

// TestResolveTimeRatioView_DirectoryAndDiscount 锁死「展示口径 = 动态目录价 + 固定折扣」。
//
// 配置：目录价 输入 2 / 输出 8（completion_ratio 4），固定 5 折（模型定价 0.5），
// 闲时输入系数 0.5、输出系数 0.875（→ 闲时目录价 1 / 7）。
// 断言：目录倍率随时段变、折扣恒定、此刻补全倍率 = 4 × 0.875/0.5 = 7。
func TestResolveTimeRatioView_DirectoryAndDiscount(t *testing.T) {
	require.NoError(t, UpdateModelRatioByJSONString(`{"m": 2}`))
	require.NoError(t, UpdateCompletionRatioByJSONString(`{"m": 4}`))
	seedRatios(t, `{"default":1}`, `{}`, `{"default":{"m":{"mode":"multiply","value":0.5}}}`)
	seedTimeRatio(t, `{
		"windows": {"night": {"label":"闲时","start":"00:00","end":"08:00"}},
		"rules":   {"*": {"m": [{"window":"night","value":0.5,"output_factor":0.875}]}}
	}`)

	night := ResolveGroupRatioAt("default", "default", "m", at(t, 0, 2, 0))
	view, ok := ResolveTimeRatioView("default", "m", at(t, 0, 2, 0), night)
	require.True(t, ok)
	// 命中闲时：目录倍率 = 基准 1 × 系数
	require.InDelta(t, 0.5, view.InputDirectoryRatio, 1e-9)
	require.InDelta(t, 0.875, view.OutputDirectoryRatio, 1e-9)
	// 折扣恒为 5 折（常规价终值 0.5 ÷ 基准 1）
	require.InDelta(t, 0.5, view.DiscountRatio, 1e-9)
	// 此刻补全倍率：4 × (0.875 / 0.5) = 7
	require.InDelta(t, 7, view.EffectiveCompletionRatio, 1e-9)
	// 每档行也带同样的口径
	require.InDelta(t, 0.5, view.Windows[0].InputDirectoryRatio, 1e-9)
	require.InDelta(t, 0.875, view.Windows[0].OutputDirectoryRatio, 1e-9)
	require.InDelta(t, 0.5, view.Windows[0].DiscountRatio, 1e-9)

	// 未命中时段（13:00）：目录倍率回到基准、折扣仍是 5 折、补全倍率回到基础值
	mid := ResolveGroupRatioAt("default", "default", "m", at(t, 0, 13, 0))
	view2, ok2 := ResolveTimeRatioView("default", "m", at(t, 0, 13, 0), mid)
	require.True(t, ok2)
	require.InDelta(t, 1.0, view2.InputDirectoryRatio, 1e-9)
	require.InDelta(t, 1.0, view2.OutputDirectoryRatio, 1e-9)
	require.InDelta(t, 0.5, view2.DiscountRatio, 1e-9)
	require.InDelta(t, 4, view2.EffectiveCompletionRatio, 1e-9)

	seedTimeRatio(t, `{}`)
}

// TestResolveGroupRatio_ComponentFactors 锁死「新格式：只配的分量才变」。
//
// 只配 input 0.5 + cache 0.25（输入 2→1、缓存 0.04→0.01，输出不变）：
//
//	· 整单倍率乘 0.5；
//	· 输出**不随时段变** → 补全倍率折 2.0 抵掉整单缩放；
//	· 缓存折 0.5 → 缓存价 = 输入价(1) × 0.02 × 0.5 = 0.01。
func TestResolveGroupRatio_ComponentFactors(t *testing.T) {
	seedRatios(t, `{"default":1}`, `{}`, `{}`)
	seedTimeRatio(t, `{
		"windows": {"night": {"label":"闲时","start":"00:00","end":"08:00"}},
		"rules":   {"*": {"m": [{"window":"night","factors":{"input":0.5,"cache":0.25}}]}}
	}`)

	night := ResolveGroupRatioAt("default", "default", "m", at(t, 0, 2, 0))
	require.InDelta(t, 0.5, night.Final, 1e-9, "整单只乘 input")
	require.InDelta(t, 0.5, night.TimeFactors["input"], 1e-9)
	require.InDelta(t, 0.25, night.TimeFactors["cache"], 1e-9)
	_, hasOutput := night.TimeFactors["output"]
	require.False(t, hasOutput, "没配的分量不进表 = 不随时段变")
	require.InDelta(t, 2.0, types.ScaleComponent("output", night.TimeFactors), 1e-12)
	require.InDelta(t, 0.5, types.ScaleComponent("cache", night.TimeFactors), 1e-12)

	// 旧格式（单 value）仍然是"整价统一乘"：所有分量都进表
	seedTimeRatio(t, `{
		"windows": {"night": {"label":"闲时","start":"00:00","end":"08:00"}},
		"rules":   {"*": {"m": [{"window":"night","value":0.5}]}}
	}`)
	legacy := ResolveGroupRatioAt("default", "default", "m", at(t, 0, 2, 0))
	require.InDelta(t, 0.5, legacy.TimeFactors["output"], 1e-9, "旧格式展开到全部分量")
	require.InDelta(t, 1.0, types.ScaleComponent("output", legacy.TimeFactors), 1e-12)
	seedTimeRatio(t, `{}`)
}
