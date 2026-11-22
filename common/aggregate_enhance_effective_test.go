package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// EffectiveEnabled 的完整矩阵:锁定 / 未锁定 × 客户三态。
//
// 这是 prompt_optimizer 的核心语义:配置是**默认值**,客户显式传值优先,锁定后不认客户。
func TestAggregatePromptEnhanceEffectiveEnabled(t *testing.T) {
	b := func(v bool) *bool { return &v }
	on := b(true)
	off := b(false)

	var nilCfg *AggregatePromptEnhance
	require.False(t, nilCfg.EffectiveEnabled(on), "没有增强段时客户传 true 也不增强")

	defaultOn := &AggregatePromptEnhance{}
	require.True(t, defaultOn.EffectiveEnabled(nil), "漏写 enabled = 默认开")
	require.False(t, defaultOn.EffectiveEnabled(off), "客户传 false 覆盖默认开")
	require.True(t, defaultOn.EffectiveEnabled(on))

	defaultOff := &AggregatePromptEnhance{Enabled: off}
	require.False(t, defaultOff.EffectiveEnabled(nil))
	require.True(t, defaultOff.EffectiveEnabled(on), "语义变化点:默认关 + 客户传 true 要跑增强")

	lockedOn := &AggregatePromptEnhance{LockEnabled: on}
	require.True(t, lockedOn.EffectiveEnabled(off), "锁定后客户传 false 不算")
	require.False(t, lockedOn.AllowsClientOverride())

	lockedOff := &AggregatePromptEnhance{Enabled: off, LockEnabled: on}
	require.False(t, lockedOff.EffectiveEnabled(on), "锁定 + 默认关:客户传 true 也不算")
}
