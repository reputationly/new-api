package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// metadata.post_stages 的结构校验。
//
// 校验放在**提交阶段**是这套改动的核心取舍之一：这些声明一旦进了任务行，推进就是
// 后台跑的；那时才发现"模型名打错了"，只能静默降级 —— 用户付了 2K 拿到 768P 且不报错。

func TestParsePostStagesValid(t *testing.T) {
	ratio := 2.0
	stages, err := ParsePostStages([]any{
		map[string]any{"kind": "sr", "model": "relayvsr", "sr_ratio": ratio, "target_short_edge": 1440},
	})
	require.NoError(t, err)
	require.Len(t, stages, 1)
	require.Equal(t, PostStageKindSR, stages[0].Kind)
	require.Equal(t, "relayvsr", stages[0].Model)
	require.Equal(t, 1440, stages[0].TargetShortEdge)
	require.NotNil(t, stages[0].SRRatio)
	require.Equal(t, 2.0, *stages[0].SRRatio)
}

func TestParsePostStagesTrimsAndChains(t *testing.T) {
	stages, err := ParsePostStages([]any{
		map[string]any{"kind": " SR ", "model": " relayvsr "},
		map[string]any{"kind": "v2a", "model": "audioX"},
	})
	require.NoError(t, err)
	require.Len(t, stages, 2)
	require.Equal(t, PostStageKindSR, stages[0].Kind, "kind 要归一化")
	require.Equal(t, "relayvsr", stages[0].Model, "模型名要去空格")
	require.Equal(t, []string{"relayvsr", "audioX"}, PostStageModels(stages))
}

func TestParsePostStagesRejects(t *testing.T) {
	cases := []struct {
		name string
		raw  any
		want string
	}{
		{"未知种类", []any{map[string]any{"kind": "upscale", "model": "x"}}, "kind 只能是"},
		{"重复种类", []any{
			map[string]any{"kind": "sr", "model": "a"},
			map[string]any{"kind": "sr", "model": "b"},
		}, "出现了两次"},
		{"缺模型", []any{map[string]any{"kind": "sr"}}, "必须给出 model"},
		{"倍率非正", []any{map[string]any{"kind": "sr", "model": "a", "sr_ratio": 0}}, "sr_ratio 必须大于 0"},
		{"短边为负", []any{map[string]any{"kind": "sr", "model": "a", "target_short_edge": -1}}, "target_short_edge"},
		{"超过两段", []any{
			map[string]any{"kind": "sr", "model": "a"},
			map[string]any{"kind": "v2a", "model": "b"},
			map[string]any{"kind": "sr", "model": "c"},
		}, "最多两段"},
		{"顺序反了", []any{
			map[string]any{"kind": "v2a", "model": "b"},
			map[string]any{"kind": "sr", "model": "a"},
		}, "顺序必须是 sr → v2a"},
		{"不是数组", map[string]any{"kind": "sr"}, "解析失败"},
		{"字段类型不对", []any{map[string]any{"kind": "sr", "model": "a", "sr_ratio": "abc"}}, "解析失败"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := ParsePostStages(c.raw)
			require.Error(t, err)
			require.Contains(t, err.Error(), c.want)
		})
	}
}

// 空/缺省不是错误：老客户端不会带这个键，带了空数组也等于没声明。
func TestParsePostStagesEmpty(t *testing.T) {
	for _, raw := range []any{nil, []any{}} {
		stages, err := ParsePostStages(raw)
		require.NoError(t, err)
		require.Empty(t, stages)
	}
}
