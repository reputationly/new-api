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
