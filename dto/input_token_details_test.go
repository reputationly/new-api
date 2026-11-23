package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
)

// 网宿 edgecloudapp 的真实 usage 片段（2026-11-23 实测）：
// 缓存写入不叫 cached_creation_tokens，而是 cache_write_tokens + creation_tokens
// 两个同义字段；TTL 明细放在 eca_cache_creation_details（键名沿用 Claude 的
// ephemeral_5m/1h，实际 TTL 是 30m / 12h）。漏读任何一处，缓存写入就会退回
// 输入价计费，长期少收。
const gatewayUsageCacheWrite = `{
  "prompt_tokens": 1452,
  "completion_tokens": 10,
  "total_tokens": 1462,
  "prompt_tokens_details": {
    "cached_tokens": 0,
    "cache_write_tokens": 1449,
    "creation_tokens": 1449,
    "eca_cache_creation_details": {"ephemeral_5m_input_tokens": 1449}
  }
}`

func TestInputTokenDetailsGatewayCacheWriteAliases(t *testing.T) {
	var usage Usage
	require.NoError(t, common.UnmarshalJsonStr(gatewayUsageCacheWrite, &usage))

	require.Equal(t, 1452, usage.PromptTokens)
	require.Equal(t, 0, usage.PromptTokensDetails.CachedTokens)
	// 三个字段任一有值都要算进总数。
	require.Equal(t, 1449, usage.PromptTokensDetails.CacheWriteTokensTotal())

	shortTTL, longTTL := usage.PromptTokensDetails.CacheWriteSplit()
	require.Equal(t, 1449, shortTTL)
	require.Equal(t, 0, longTTL)
}

func TestCacheWriteTokensTotalPrefersLargestAlias(t *testing.T) {
	cases := []struct {
		name  string
		in    InputTokenDetails
		total int
	}{
		{name: "无缓存写入", in: InputTokenDetails{}, total: 0},
		{name: "历史字段", in: InputTokenDetails{CachedCreationTokens: 100}, total: 100},
		{name: "仅 cache_write_tokens", in: InputTokenDetails{CacheWriteTokens: 200}, total: 200},
		{name: "仅 creation_tokens", in: InputTokenDetails{CreationTokens: 300}, total: 300},
		{
			name:  "别名同值不重复计",
			in:    InputTokenDetails{CacheWriteTokens: 1449, CreationTokens: 1449},
			total: 1449,
		},
		{
			name:  "历史字段更大时以它为准",
			in:    InputTokenDetails{CachedCreationTokens: 500, CacheWriteTokens: 200},
			total: 500,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.total, tc.in.CacheWriteTokensTotal())
		})
	}
}

func TestCacheWriteSplitRequiresDetails(t *testing.T) {
	// 没有 TTL 明细时必须返回 (0, 0)：调用方据此退回「单一缓存写入价」，
	// 否则会把总量当成短 TTL 那一档、长 TTL 档永远算 0。
	noDetails := InputTokenDetails{CacheWriteTokens: 1449}
	require.Equal(t, 1449, noDetails.CacheWriteTokensTotal())
	shortTTL, longTTL := noDetails.CacheWriteSplit()
	require.Zero(t, shortTTL)
	require.Zero(t, longTTL)

	split := InputTokenDetails{
		EcaCacheCreationDetails: &EcaCacheCreationDetails{
			Ephemeral5mInputTokens: 1200,
			Ephemeral1hInputTokens: 249,
		},
	}
	shortTTL, longTTL = split.CacheWriteSplit()
	require.Equal(t, 1200, shortTTL)
	require.Equal(t, 249, longTTL)
}

// 汇总缺失时以 TTL 明细之和为准：上游只给 eca_cache_creation_details 时，
// 汇总字段是 0，不算进总数会让这批 token 既留在输入里又按分量价计一遍。
func TestCacheWriteTokensTotalFallsBackToSplitSum(t *testing.T) {
	onlySplit := InputTokenDetails{
		EcaCacheCreationDetails: &EcaCacheCreationDetails{
			Ephemeral5mInputTokens: 30,
			Ephemeral1hInputTokens: 10,
		},
	}
	require.Equal(t, 40, onlySplit.CacheWriteTokensTotal())

	// 汇总偏小时同样取明细之和
	partial := InputTokenDetails{
		CacheWriteTokens: 5,
		EcaCacheCreationDetails: &EcaCacheCreationDetails{
			Ephemeral5mInputTokens: 30,
			Ephemeral1hInputTokens: 10,
		},
	}
	require.Equal(t, 40, partial.CacheWriteTokensTotal())

	// 汇总更大时保留汇总
	bigger := InputTokenDetails{
		CacheWriteTokens: 50,
		EcaCacheCreationDetails: &EcaCacheCreationDetails{
			Ephemeral5mInputTokens: 20,
			Ephemeral1hInputTokens: 10,
		},
	}
	require.Equal(t, 50, bigger.CacheWriteTokensTotal())
}

// 两份明细都在且内容相同时不能累加成两倍；主来源为零的字段才回退。
func TestMergeInputDetailsDoesNotAccumulate(t *testing.T) {
	dst := InputTokenDetails{
		CachedTokens:     100,
		CacheWriteTokens: 40,
		ImageTokens:      7,
	}
	src := InputTokenDetails{
		CachedTokens:     100,
		CacheWriteTokens: 40,
		ImageTokens:      7,
		TextTokens:       90,
		EcaCacheCreationDetails: &EcaCacheCreationDetails{
			Ephemeral5mInputTokens: 40,
		},
	}

	dst.MergeInputDetails(&src)

	require.Equal(t, 100, dst.CachedTokens)
	require.Equal(t, 40, dst.CacheWriteTokens)
	require.Equal(t, 7, dst.ImageTokens)
	require.Equal(t, 90, dst.TextTokens)
	require.NotNil(t, dst.EcaCacheCreationDetails)
}

func TestMergeInputDetailsFallsBackWhenPrimaryEmpty(t *testing.T) {
	var dst InputTokenDetails
	src := InputTokenDetails{
		CachedTokens:     60,
		CacheWriteTokens: 40,
		CreationTokens:   40,
		TextTokens:       100,
	}

	dst.MergeInputDetails(&src)

	require.Equal(t, 60, dst.CachedTokens)
	require.Equal(t, 40, dst.CacheWriteTokens)
	require.Equal(t, 40, dst.CreationTokens)
	require.Equal(t, 100, dst.TextTokens)
	require.Equal(t, 40, dst.CacheWriteTokensTotal())

	// nil 入参不 panic
	dst.MergeInputDetails(nil)
}
