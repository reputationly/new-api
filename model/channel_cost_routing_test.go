package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

func chanWith(id int, weight uint) *Channel {
	w := weight
	return &Channel{Id: id, Weight: &w}
}

func idsOf(channels []*Channel) []int {
	out := make([]int, 0, len(channels))
	for _, c := range channels {
		out = append(out, c.Id)
	}
	return out
}

// TestFilterCheapestChannels_EmptyCostTableKeepsAll P2 上线时成本表是空的，
// 此时选路必须与改造前逐位相同——否则「加了个还没启用的功能」就把线上流量分布改了。
func TestFilterCheapestChannels_EmptyCostTableKeepsAll(t *testing.T) {
	withCostCache(t, map[int]map[string]float64{})

	in := []*Channel{chanWith(1, 10), chanWith(2, 20), chanWith(3, 30)}
	require.Equal(t, []int{1, 2, 3}, idsOf(filterCheapestChannels(in, "GLM-5")))
}

// TestFilterCheapestChannels_PicksCheapest 全部配了成本时严格选最便宜的一档，
// 同成本的并列保留以维持负载分担。
func TestFilterCheapestChannels_PicksCheapest(t *testing.T) {
	withCostCache(t, map[int]map[string]float64{
		1: {"GLM-5": 0.9},
		2: {"GLM-5": 0.6},
		3: {"GLM-5": 0.6},
	})

	got := idsOf(filterCheapestChannels(
		[]*Channel{chanWith(1, 10), chanWith(2, 10), chanWith(3, 10)}, "GLM-5"))

	require.Equal(t, []int{2, 3}, got,
		"同为最低成本的多个渠道必须都保留——同一供应商的多 key 冗余靠这个分担负载")
}

// TestFilterCheapestChannels_UnconfiguredNotDiscriminated 「未配成本」与最便宜的一档
// 并列保留，这是本函数唯一需要想清楚的地方（§5.5）。
//
// 两个看似自然的做法都是错的：
//   - 未配当 1.0 → 配了 0.6 的永远胜出，新加的渠道静默拿不到流量，无报错无日志；
//   - 未配当 0   → 反过来，认真配了成本的渠道全部失效。
func TestFilterCheapestChannels_UnconfiguredNotDiscriminated(t *testing.T) {
	withCostCache(t, map[int]map[string]float64{
		1: {"GLM-5": 0.6},
		2: {"GLM-5": 0.9},
		// 渠道 3 未配
	})

	got := idsOf(filterCheapestChannels(
		[]*Channel{chanWith(1, 10), chanWith(2, 10), chanWith(3, 10)}, "GLM-5"))

	require.Contains(t, got, 1, "最便宜的必须在")
	require.Contains(t, got, 3, "未配成本的不得被静默排除")
	require.NotContains(t, got, 2, "明确更贵的应被筛掉")
}

// TestFilterCheapestChannels_OtherModelUnaffected 成本是按 (渠道, 模型) 索引的，
// 另一个模型没配不影响本模型的筛选。
func TestFilterCheapestChannels_OtherModelUnaffected(t *testing.T) {
	withCostCache(t, map[int]map[string]float64{
		1: {"GLM-5": 0.6},
		2: {"Kimi-K3": 0.3},
	})

	got := idsOf(filterCheapestChannels(
		[]*Channel{chanWith(1, 10), chanWith(2, 10)}, "GLM-5"))

	require.Equal(t, []int{1, 2}, got,
		"渠道 2 在 GLM-5 上未配成本，应与最低档并列而非按它的 Kimi 成本参与比较")
}

func TestFilterCheapestChannels_SingleChannel(t *testing.T) {
	withCostCache(t, map[int]map[string]float64{1: {"GLM-5": 0.9}})
	in := []*Channel{chanWith(1, 10)}
	require.Equal(t, in, filterCheapestChannels(in, "GLM-5"))
}

// TestGetRandomSatisfiedChannel_WeightRecomputedAfterFilter 回归用例：sumWeight 必须
// 在成本筛选之后统计。
//
// 构造一个「被筛掉的渠道占了绝大部分权重」的场景：渠道 2 权重 1000 但更贵，会被筛
// 掉；若 sumWeight 仍按 1010 算，randomWeight 有 99% 的概率落进已被移除的区间，
// 加权循环走完选不中，函数返回 "channel not found"。
//
// 症状是**随机的、只在配了成本之后才出现的偶发失败**——最难归因的那一类，所以这条
// 跑 200 次而不是 1 次。
func TestGetRandomSatisfiedChannel_WeightRecomputedAfterFilter(t *testing.T) {
	prevCacheEnabled := commonMemoryCacheEnabledForTest(t, true)
	defer prevCacheEnabled()

	withCostCache(t, map[int]map[string]float64{
		1: {"GLM-5": 0.6},
		2: {"GLM-5": 0.9},
	})
	withChannelCache(t,
		map[string]map[string][]int{"default": {"GLM-5": {1, 2}}},
		map[int]*Channel{
			1: chanWith(1, 10),
			2: chanWith(2, 1000),
		})

	for i := 0; i < 200; i++ {
		got, err := GetRandomSatisfiedChannel("default", "GLM-5", 0)
		require.NoError(t, err, "第 %d 次选路失败——sumWeight 未在筛选后重算", i)
		require.NotNil(t, got)
		require.Equal(t, 1, got.Id, "只有最便宜的渠道 1 应被选中")
	}
}

// withChannelCache 直接铺设渠道选路缓存，绕开 DB。
func withChannelCache(t *testing.T, g2m2c map[string]map[string][]int, idm map[int]*Channel) {
	t.Helper()
	channelSyncLock.Lock()
	prevG, prevIDM := group2model2channels, channelsIDM
	group2model2channels, channelsIDM = g2m2c, idm
	channelSyncLock.Unlock()
	t.Cleanup(func() {
		channelSyncLock.Lock()
		group2model2channels, channelsIDM = prevG, prevIDM
		channelSyncLock.Unlock()
	})
}

// commonMemoryCacheEnabledForTest 临时打开内存缓存开关，返回还原函数。
// GetRandomSatisfiedChannel 在开关关闭时会直接走 DB，测不到本文件关心的选路逻辑。
func commonMemoryCacheEnabledForTest(t *testing.T, enabled bool) func() {
	t.Helper()
	prev := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = enabled
	return func() { common.MemoryCacheEnabled = prev }
}
