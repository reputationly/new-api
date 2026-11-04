package model

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
)

// 后置段链的索引算术。
//
// 这里钉的是一次真实缺陷：`HasMorePostStages` 曾写成 `len(PostStages) > Stage`，
// 而 `NextPostStage` 用的是 `Stage-1`，两者差一。sr→v2a 两段链在 sr 成功时
// Stage=2、len=2：`2 > 2` 判成"没有下一段"，配音段永远不被提交 —— 而且不报错，
// 只是静默交付了 sr 版成片。修复方式是让 HasMorePostStages 直接问 NextPostStage，
// 从根上消灭第二份索引算术；这组用例就是那份算术的规格说明。

func stagesOf(kinds ...string) []common.PostStage {
	out := make([]common.PostStage, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, common.PostStage{Kind: k, Model: k + "-model"})
	}
	return out
}

func TestNextPostStageIndexing(t *testing.T) {
	agg := &TaskAggregateInfo{PostStages: stagesOf(common.PostStageKindSR, common.PostStageKindV2A)}

	// Stage=1（生成段在跑）→ 该提交第 1 段 sr
	agg.Stage = 1
	st, ok := agg.NextPostStage()
	require.True(t, ok)
	require.Equal(t, common.PostStageKindSR, st.Kind)

	// Stage=2（sr 在跑/刚成功）→ 该提交第 2 段 v2a
	agg.Stage = 2
	st, ok = agg.NextPostStage()
	require.True(t, ok)
	require.Equal(t, common.PostStageKindV2A, st.Kind)

	// Stage=3（v2a 在跑）→ 链尾
	agg.Stage = 3
	_, ok = agg.NextPostStage()
	require.False(t, ok)
}

func TestHasMorePostStagesMatchesNextPostStage(t *testing.T) {
	two := &TaskAggregateInfo{PostStages: stagesOf(common.PostStageKindSR, common.PostStageKindV2A)}
	one := &TaskAggregateInfo{PostStages: stagesOf(common.PostStageKindSR)}

	cases := []struct {
		name  string
		agg   *TaskAggregateInfo
		stage int
		want  bool
	}{
		{"两段链·生成段刚完成", two, 1, true},
		{"两段链·sr 刚成功（曾判成 false 的那一格）", two, 2, true},
		{"两段链·v2a 在跑", two, 3, false},
		{"单段链·生成段刚完成", one, 1, true},
		{"单段链·sr 在跑", one, 2, false},
		{"没有声明后置段", &TaskAggregateInfo{}, 1, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.agg.Stage = c.stage
			require.Equal(t, c.want, c.agg.HasMorePostStages())
			// 两个接口必须永远同口径 —— 差一 bug 就是这么来的。
			_, ok := c.agg.NextPostStage()
			require.Equal(t, ok, c.agg.HasMorePostStages())
		})
	}
}

// nil 安全：调用方常在聚合链路（PrivateData.Aggregate 为 nil）上直接问。
func TestPostStageHelpersNilSafe(t *testing.T) {
	var agg *TaskAggregateInfo
	_, ok := agg.NextPostStage()
	require.False(t, ok)
	require.False(t, agg.HasMorePostStages())
	require.False(t, agg.IsPlaygroundPipeline())
}
