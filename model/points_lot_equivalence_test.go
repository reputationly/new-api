package model

import (
	"math/rand"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// 等价性性质测试（docs/points-expiry-design.md §3.4 与 §十四）。
//
// 参考实现「当场扣」：每笔消费按「到期最早的先扣」立即扣减；每笔退款原路退回
// **该请求扣过的批次**（逆序退）。
// 被测实现「惰性归属」：两次 sync 之间只改余额，最后把净消耗一次性按同一顺序贪心分摊。
//
// 两者**并不逐批次相等**（反例见 §3.4）。可证明的结论只有：
//   1. 总剩余相等；
//   2. 惰性实现烧掉的快到期积分只多不少 ⇒ 按前缀看，惰性剩余 ≤ 当场扣剩余；
//   3. 因而累计作废额 ≤ 当场扣（作废集恰是扣减顺序的一个前缀）；
//   4. 退款始终后进先出（只退最近一笔请求）时，两者逐批次完全相等。
//
// 这四条就是本测试的断言。别把它们"加强"成逐批次全等——那会把一个正确的实现判红。

type eqRequest struct {
	taken []eqTaken
}

type eqTaken struct {
	lotId  int
	amount int64
}

type eqLot struct {
	id        int
	amount    int64
	expiresAt int64
}

// eqOrder 扣减顺序：到期早的优先，永不过期最后，同到期按 id 升序。
func eqOrder(lots []eqLot) []eqLot {
	ordered := append([]eqLot(nil), lots...)
	sort.SliceStable(ordered, func(i, j int) bool {
		ae, be := ordered[i].expiresAt == 0, ordered[j].expiresAt == 0
		if ae != be {
			return !ae
		}
		if ordered[i].expiresAt != ordered[j].expiresAt {
			return ordered[i].expiresAt < ordered[j].expiresAt
		}
		return ordered[i].id < ordered[j].id
	})
	return ordered
}

func TestPointsLotsEquivalence(t *testing.T) {
	const (
		seed            = int64(20261124)
		iterations      = 2400
		wantLifoAtLeast = 500
	)
	rng := rand.New(rand.NewSource(seed))
	t.Logf("等价性性质测试 seed=%d iterations=%d", seed, iterations)

	now := shanghaiTime(2026, 6, 1, 0, 5, 0)
	lifoCount := 0
	divergedCount := 0

	for iter := 0; iter < iterations; iter++ {
		lifo := rng.Intn(2) == 0
		if lifo {
			lifoCount++
		}

		// 造 2~6 个批次：偶数下标带明确到期时间（都早于 now，便于直接比作废额），
		// 奇数下标永不过期。
		n := 2 + rng.Intn(5)
		lots := make([]eqLot, 0, n)
		var total int64
		for i := 0; i < n; i++ {
			amount := int64(1 + rng.Intn(100))
			var exp int64
			if i%2 == 0 {
				exp = now - int64(86400*(1+rng.Intn(30)))
			}
			lots = append(lots, eqLot{id: i + 1, amount: amount, expiresAt: exp})
			total += amount
		}

		eagerRemaining := map[int]int64{}
		for _, l := range lots {
			eagerRemaining[l.id] = l.amount
		}
		order := eqOrder(lots)

		balance := total
		var requests []eqRequest
		ops := 1 + rng.Intn(6)

		for op := 0; op < ops; op++ {
			// 有一定概率先退款，再消费
			if len(requests) > 0 && rng.Intn(3) == 0 {
				idx := len(requests) - 1
				if !lifo {
					idx = rng.Intn(len(requests))
				}
				var available int64
				for _, tk := range requests[idx].taken {
					available += tk.amount
				}
				if available > 0 {
					refund := int64(1 + rng.Intn(int(available)))
					var refunded int64
					// 参考实现：逆序原路退回该请求扣过的批次
					for i := len(requests[idx].taken) - 1; i >= 0 && refund > 0; i-- {
						tk := &requests[idx].taken[i]
						give := refund
						if give > tk.amount {
							give = tk.amount
						}
						if give <= 0 {
							continue
						}
						eagerRemaining[tk.lotId] += give
						tk.amount -= give
						refund -= give
						refunded += give
					}
					balance += refunded
					continue
				}
			}

			if balance <= 0 {
				continue
			}
			consume := int64(1 + rng.Intn(int(balance)))
			req := eqRequest{}
			for _, l := range order {
				if consume <= 0 {
					break
				}
				take := consume
				if take > eagerRemaining[l.id] {
					take = eagerRemaining[l.id]
				}
				if take <= 0 {
					continue
				}
				eagerRemaining[l.id] -= take
				req.taken = append(req.taken, eqTaken{lotId: l.id, amount: take})
				consume -= take
			}
			var taken int64
			for _, tk := range req.taken {
				taken += tk.amount
			}
			if taken > 0 {
				requests = append(requests, req)
				balance -= taken
			}
		}

		// 惰性实现：把窗口内的净消耗一次性分摊
		modelLots := make([]PointsLot, 0, len(lots))
		for _, l := range lots {
			modelLots = append(modelLots, PointsLot{
				Id: l.id, Amount: l.amount, ExpiresAt: l.expiresAt, Status: PointsLotStatusActive,
			})
		}
		mutations, surplus := allocatePointsLots(modelLots, balance, now)
		require.Zero(t, surplus, "窗口内净消耗不会产生 surplus（iter=%d）", iter)
		lazyRemaining := map[int]int64{}
		for _, l := range lots {
			lazyRemaining[l.id] = l.amount
		}
		for _, m := range mutations {
			lazyRemaining[m.LotId] -= m.AttributedDelta
		}

		// 断言 1：总剩余相等，且等于余额
		var eagerTotal, lazyTotal int64
		for _, l := range lots {
			eagerTotal += eagerRemaining[l.id]
			lazyTotal += lazyRemaining[l.id]
		}
		require.Equal(t, eagerTotal, lazyTotal, "总剩余应相等（iter=%d）", iter)
		require.Equal(t, balance, lazyTotal, "总剩余应等于余额（iter=%d）", iter)

		// 断言 2 + 3：按扣减顺序取前缀，惰性剩余 ≤ 当场扣剩余
		var eagerPrefix, lazyPrefix int64
		for _, l := range order {
			eagerPrefix += eagerRemaining[l.id]
			lazyPrefix += lazyRemaining[l.id]
			require.LessOrEqual(t, lazyPrefix, eagerPrefix,
				"前缀剩余：惰性不得多于当场扣（iter=%d lot=%d）", iter, l.id)
		}
		// 作废额：所有带到期时间的批次恰是扣减顺序的一个前缀，故由上面的前缀断言蕴含
		var eagerForfeit, lazyForfeit int64
		for _, l := range order {
			if l.expiresAt == 0 {
				break
			}
			eagerForfeit += eagerRemaining[l.id]
			lazyForfeit += lazyRemaining[l.id]
		}
		require.LessOrEqual(t, lazyForfeit, eagerForfeit, "作废额：惰性不得多于当场扣（iter=%d）", iter)

		// 断言 4：LIFO 子集逐批次相等
		if lifo {
			for _, l := range lots {
				require.Equal(t, eagerRemaining[l.id], lazyRemaining[l.id],
					"LIFO 子集应逐批次相等（iter=%d lot=%d）", iter, l.id)
			}
		} else {
			// 非 LIFO 应当真的观察到分歧，否则本测试没有区分能力（等价性断言空转）
			for _, l := range lots {
				if eagerRemaining[l.id] != lazyRemaining[l.id] {
					divergedCount++
					break
				}
			}
		}
	}

	require.GreaterOrEqual(t, lifoCount, wantLifoAtLeast, "LIFO 子集条数不足")
	require.Greater(t, divergedCount, 0, "非 LIFO 序列从未出现分歧，说明测试无区分能力")
	t.Logf("等价性性质测试通过：随机序列 %d 条，LIFO 子集 %d 条，非 LIFO 中出现逐批次分歧 %d 条",
		iterations, lifoCount, divergedCount)
}
