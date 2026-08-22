package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

// TestGroupStatus 分组健康判定的四条分支。
//
// 「auto」必须先于渠道数判定：它是伪分组，不对应任何渠道，运行时会被替换成
// auto 池里的某个真实分组。不特判的话页面会给它永远挂一个红色「无渠道挂载」——
// 一个永远亮着的假红灯比没有灯更糟，管理员很快学会无视这一列，真出问题也不会看。
func TestGroupStatus(t *testing.T) {
	cases := []struct {
		name         string
		group        string
		channelCount int
		reachable    bool
		want         string
	}{
		{"伪分组优先于一切判定", pseudoGroupAuto, 0, false, "virtual"},
		{"伪分组即便有渠道也仍是伪分组", pseudoGroupAuto, 3, true, "virtual"},
		{"没渠道是最硬的失配", "premium", 0, true, "no_channel"},
		{"有渠道但没人能用", "premium", 3, false, "unreachable"},
		{"正常", "premium", 3, true, "ok"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, groupStatus(c.group, c.channelCount, c.reachable))
		})
	}
}

// TestPseudoGroupAutoValue 这个值在 middleware/auth.go、middleware/distributor.go、
// controller/token.go 等 8 处硬编码，改了要同步。
func TestPseudoGroupAutoValue(t *testing.T) {
	require.Equal(t, "auto", pseudoGroupAuto)
}

// TestStaleRulePatterns_NoRules 没配规则时不该报「未生效」——
// 空数组和 nil 都要返回空，否则页面会给每个分组挂一个莫名其妙的橙色角标。
func TestStaleRulePatterns_NoRules(t *testing.T) {
	require.Empty(t, staleRulePatterns("premium", nil))
	require.Empty(t, staleRulePatterns("premium", map[string]ratio_setting.ModelRatioRule{}))
}
