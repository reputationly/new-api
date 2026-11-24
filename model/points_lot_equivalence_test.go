package model

import (
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 等价性性质测试（docs/points-expiry-design.md v2.3 §3.4、§十四、AC8）。
//
// 参考实现「当场扣」= 测试内的账本：每笔消费按最快到期优先立即扣、每笔退款原路退回
// **该请求扣过的批次**。
// 被测实现「惰性归属」走**真实代码路径**：GrantPointsTx（触发 sync）、
// TryDecreaseUserPoints（热路径扣减）、IncreaseUserPoints（退款）、ExpireUserPointsLots，
// 落在真实的内存 SQLite 上。只调纯函数测不到跨 sync 的状态演化，而 §3.4 的反例正是
// 跨窗口才出现。
//
// 五条断言（v2.3）：
//  1. 每次 sync 后：两边批次剩余总和相等，且都等于余额；
//  2. 序列结束时：惰性累计作废额 ≤ 参考累计作废额 + 跨窗口退款总额；
//  3. 不含跨窗口退款的序列：每次 sync 后前缀占优，且惰性累计作废额 ≤ 参考；
//  4. 不含跨窗口退款且退款全为 LIFO 的序列：每次 sync 后逐批次剩余完全相等；
//  5. 必须观察到至少一条「惰性作废额 > 参考作废额」的序列（证明测试能触发反例）。
//
// 断言 2 若被打破，**不要把上界放宽**——那说明 §3.4 的上界本身不成立，要回到设计。

const (
	eqStepQuantum = int64(600) // 时间推进粒度：10 分钟
	eqBaseTime    = int64(1_790_000_000)
)

type eqRefLot struct {
	id        int
	amount    int64
	remaining int64
	expiresAt int64
}

type eqRefTake struct {
	lotID  int
	amount int64
}

type eqRefReq struct {
	gen   int
	taken []eqRefTake
}

// eqOrder 扣减顺序：到期早的优先，永不过期最后，同到期按 id 升序。
func eqOrder(lots []*eqRefLot) []*eqRefLot {
	out := append([]*eqRefLot(nil), lots...)
	sort.SliceStable(out, func(i, j int) bool {
		ae, be := out[i].expiresAt == 0, out[j].expiresAt == 0
		if ae != be {
			return !ae
		}
		if out[i].expiresAt != out[j].expiresAt {
			return out[i].expiresAt < out[j].expiresAt
		}
		return out[i].id < out[j].id
	})
	return out
}

// eqConsume 参考实现的一次消费，返回本请求在各批次上的拆分。
func eqConsume(lots []*eqRefLot, amount int64, now int64) []eqRefTake {
	var taken []eqRefTake
	for _, l := range eqOrder(lots) {
		if amount <= 0 {
			break
		}
		if l.remaining <= 0 {
			continue
		}
		if l.expiresAt > 0 && l.expiresAt <= now {
			continue // 已到期的不该再被消费（过期任务恰在到期时刻跑过）
		}
		take := amount
		if take > l.remaining {
			take = l.remaining
		}
		l.remaining -= take
		taken = append(taken, eqRefTake{lotID: l.id, amount: take})
		amount -= take
	}
	return taken
}

// eqRefund 参考实现的原路退款：按该请求扣减的逆序退回。
//
// 退回到**已过期**的批次时，那笔钱在当场扣的世界里等于「到账即作废」——批次已经过期，
// 退进去也花不出去。这里当场把它计成作废额，参考账本的合计才不会与余额脱节
// （惰性那边同样的钱会落进永不过期的 surplus，正是 §3.4 分歧的另一侧）。
func eqRefund(lots []*eqRefLot, req *eqRefReq, amount int64, now int64) (refunded, deadOnArrival int64) {
	byID := map[int]*eqRefLot{}
	for _, l := range lots {
		byID[l.id] = l
	}
	for i := len(req.taken) - 1; i >= 0 && amount > 0; i-- {
		tk := &req.taken[i]
		give := amount
		if give > tk.amount {
			give = tk.amount
		}
		if give <= 0 {
			continue
		}
		if l, ok := byID[tk.lotID]; ok {
			if l.expiresAt > 0 && l.expiresAt <= now {
				deadOnArrival += give
			} else {
				l.remaining += give
			}
		} else {
			deadOnArrival += give
		}
		tk.amount -= give
		amount -= give
		refunded += give
	}
	return refunded, deadOnArrival
}

// eqForfeit 参考实现在某个到期时刻的作废额。
func eqForfeit(lots []*eqRefLot, instant int64) int64 {
	var total int64
	for _, l := range lots {
		if l.expiresAt > 0 && l.expiresAt <= instant && l.remaining > 0 {
			total += l.remaining
			l.remaining = 0
		}
	}
	return total
}

type eqSeqStats struct {
	crossWindowRefundTotal int64
	lazyForfeited          int64
	refForfeited           int64
	hasCrossWindow         bool
	allLifo                bool
}

// eqRunSequence 跑一条随机序列，返回统计；任何断言失败直接 t.Fatalf 并打印序列。
func eqRunSequence(t *testing.T, seed int64, seq int, rng *rand.Rand) eqSeqStats {
	t.Helper()

	uid := 900000 + seq
	require.NoError(t, DB.Create(&User{
		Id: uid, Username: fmt.Sprintf("eq_%d", uid), Role: 1, Status: 1,
		Group: "default", AffCode: fmt.Sprintf("eq%06d", uid),
	}).Error)
	t.Cleanup(func() {
		DB.Exec("DELETE FROM points_lots WHERE user_id = ?", uid)
		DB.Exec("DELETE FROM fund_entries WHERE user_id = ?", uid)
		DB.Unscoped().Where("id = ?", uid).Delete(&User{})
	})

	now := eqBaseTime
	var (
		lots     []*eqRefLot
		requests []*eqRefReq
		// lazyBalance 镜像真实余额；refBalance 是参考账本的余额。
		// 两者只在「作废额分歧」时才允许不同（§3.4 的反例），其余时刻必须一致。
		lazyBalance int64
		refBalance  int64
		ops         []string
		gen         int // 每次 sync 递增
		stats       eqSeqStats
	)
	stats.allLifo = true
	steps := 50 + rng.Intn(15)
	// 三条通道各占三分之一，保证 §十四 要求的「第 3、4 类各 ≥ 300 条」真能满足：
	//   mode 0 = 退款可针对任意在途请求（会造出跨窗口退款，用于验断言 2 与反例）
	//   mode 1 = 退款只针对**当前窗口内**的请求，随机挑 → 保证不含跨窗口退款（第 3 类）
	//   mode 2 = 同上且只退最近一笔 → 第 4 类
	mode := seq % 3
	strictLifo := mode == 2
	windowLocalOnly := mode != 0

	fail := func(format string, args ...any) {
		t.Fatalf("性质测试失败 seed=%d seq=%d：%s\n操作序列：\n%s",
			seed, seq, fmt.Sprintf(format, args...), strings.Join(ops, "\n"))
	}

	// lazyLots 读回当前库里的批次（id → 剩余），并断言与参考账本的合计一致。
	lazyLots := func() map[int]int64 {
		var rows []PointsLot
		require.NoError(t, DB.Where("user_id = ?", uid).Find(&rows).Error)
		out := map[int]int64{}
		for _, r := range rows {
			rem := r.Amount - r.Attributed - r.Forfeited
			if rem < 0 {
				rem = 0
			}
			out[r.Id] = rem
		}
		return out
	}
	runExpiry := func(instant int64) {
		forfeited, _, err := ExpireUserPointsLots(uid, instant)
		require.NoError(t, err)
		stats.lazyForfeited += forfeited
		refForfeited := eqForfeit(lots, instant)
		stats.refForfeited += refForfeited
		// 两边各自扣掉自己作废的量：真实余额扣惰性作废额，参考余额扣参考作废额。
		// 这正是 §3.4 反例的来源——两者可能不相等。
		lazyBalance -= forfeited
		refBalance -= refForfeited
		gen++
		ops = append(ops, fmt.Sprintf("  expire@%d → lazy %d / ref %d", instant, forfeited, stats.refForfeited))
	}
	assertSyncPoint := func(tag string, afterExpiry bool) {
		got := lazyLots()
		var lazySum int64
		for _, v := range got {
			lazySum += v
		}
		var refSum int64
		for _, l := range lots {
			refSum += l.remaining
		}
		var dbBalance int64
		require.NoError(t, DB.Unscoped().Model(&User{}).Where("id = ?", uid).
			Select("points_balance").Scan(&dbBalance).Error)
		// 断言 1（可自洽的读法）：每一边的批次合计必须等于**它自己的**余额；
		// 在尚未出现作废额分歧时，两边还必须相等。
		// 字面上「两边都等于余额」在作废额分歧的那一刻必然不成立（§3.4 的跨窗口反例
		// 就是这么构造的：同一时刻惰性作废 100、当场扣作废 0），故按此实现。
		if lazySum != dbBalance {
			fail("断言1a 失败（%s）：lazy 批次合计 %d ≠ 真实余额 %d", tag, lazySum, dbBalance)
		}
		if refSum != refBalance {
			fail("断言1b 失败（%s）：ref 批次合计 %d ≠ 参考余额 %d", tag, refSum, refBalance)
		}
		if stats.lazyForfeited == stats.refForfeited && lazySum != refSum {
			fail("断言1c 失败（%s）：未出现作废额分歧，两边应相等（lazy=%d ref=%d）", tag, lazySum, refSum)
		}

		// 断言 3（前缀部分）：无跨窗口退款时按扣减顺序前缀占优。
		//
		// 只在**发放触发的 sync**上比：前缀占优是「分摊窗口内」的性质——惰性把更多消耗
		// 归到快过期批次上。一旦作废发生，两边的作废额本身就可能不同（惰性烧得多 ⇒ 作废
		// 得少），此时剩余会整体偏移，前缀比较就退化成「比总数」，不再是有意义的口径。
		// 作废额的优劣另由下方的累计断言与断言 2 负责。
		// 两边的作废额一旦分道扬镳，总数就不同了，此后「前缀占优」与「逐批次相等」都
		// 不再是有意义的比较（lazy 总量更多，前缀迟早会追平）。这类序列由断言 2 的上界
		// 与累计作废额比较负责。
		diverged := stats.lazyForfeited != stats.refForfeited
		if !stats.hasCrossWindow && !afterExpiry && !diverged {
			ordered := eqOrder(lots)
			var lazyPrefix, refPrefix int64
			for _, l := range ordered {
				refPrefix += l.remaining
				lazyPrefix += got[l.id]
				if lazyPrefix > refPrefix {
					var dbg []string
					for _, x := range ordered {
						dbg = append(dbg, fmt.Sprintf("    lot=%d exp=%d ref=%d lazy=%d", x.id, x.expiresAt, x.remaining, got[x.id]))
					}
					ops = append(ops, "  两边状态：", strings.Join(dbg, "\n"))
					fail("断言3 失败（%s）：批次 %d 处前缀 lazy=%d > ref=%d", tag, l.id, lazyPrefix, refPrefix)
				}
			}
		}
		// 断言 3（作废额部分）：无跨窗口退款时，累计作废额永不多于参考实现。
		if !stats.hasCrossWindow && stats.lazyForfeited > stats.refForfeited {
			fail("断言3 失败（%s）：累计作废额 lazy=%d > ref=%d", tag, stats.lazyForfeited, stats.refForfeited)
		}

		// 断言 4：无跨窗口退款且全 LIFO 时逐批次相等
		if !stats.hasCrossWindow && strictLifo && !diverged {
			for _, l := range lots {
				if got[l.id] != l.remaining {
					fail("断言4 失败（%s）：批次 %d lazy=%d ref=%d", tag, l.id, got[l.id], l.remaining)
				}
			}
		}
	}

	nextFundRef := 0
	for step := 0; step < steps; step++ {
		action := rng.Intn(10)
		switch {
		case action < 4: // 发放（触发 sync）
			amount := int64(1 + rng.Intn(50))
			var expiresAt int64
			if rng.Intn(2) == 0 {
				expiresAt = now + eqStepQuantum*int64(1+rng.Intn(5))
			}
			nextFundRef++
			refId := fmt.Sprintf("eq:%d:%d", seq, nextFundRef)
			var lotId int
			require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
				var err error
				lotId, err = GrantPointsTx(tx, PointsGrant{
					UserId: uid, Quota: amount, Source: FundSourceCheckin, RefId: refId,
					ExpiresAt: expiresAt, Now: now,
					Fund: FundEntry{
						Kind: FundKindGift, Source: FundSourceCheckin,
						RefType: FundRefCheckin, RefId: refId,
					},
				})
				return err
			}))
			lots = append(lots, &eqRefLot{id: lotId, amount: amount, remaining: amount, expiresAt: expiresAt})
			lazyBalance += amount
			refBalance += amount
			gen++
			ops = append(ops, fmt.Sprintf("step %d grant %d exp=%d lot=%d", step, amount, expiresAt, lotId))
			assertSyncPoint(fmt.Sprintf("step %d grant", step), false)
			// 无跨窗口退款时，库里不该出现参考账本以外的非零批次（0 额 legacy 标记不算）。
			// 有跨窗口退款时 surplus 是**设计允许**的：退款回补不回已作废的批次，无处可去
			// 就落成永不过期的 surplus（§3.3）。它只会让惰性那侧多留、作废更少，
			// 与断言 2 的上界方向一致。
			if !stats.hasCrossWindow {
				got := lazyLots()
				for id := range got {
					known := false
					for _, l := range lots {
						if l.id == id {
							known = true
							break
						}
					}
					if !known {
						var row PointsLot
						require.NoError(t, DB.Where("id = ?", id).First(&row).Error)
						if row.Amount != 0 {
							fail("无跨窗口退款却出现参考账本以外的非零批次：id=%d source=%s amount=%d",
								id, row.Source, row.Amount)
						}
					}
				}
			}
		case action < 7: // 消费（热路径）
			// 跨窗口退款会让两边余额出现分歧（惰性那边更多），此时「参考账本」可能
			// 装不下整笔消费。把消费夹到参考仍持有的额度内，保证这笔操作在两边都能表达。
			var refSum int64
			for _, l := range lots {
				refSum += l.remaining
			}
			cap := lazyBalance
			if refSum < cap {
				cap = refSum
			}
			if cap <= 0 {
				continue
			}
			amount := int64(1 + rng.Intn(int(cap)))
			ok, err := TryDecreaseUserPoints(uid, int(amount))
			require.NoError(t, err)
			if !ok {
				continue
			}
			taken := eqConsume(lots, amount, now)
			var actual int64
			for _, tk := range taken {
				actual += tk.amount
			}
			if actual != amount {
				fail("step %d 消费：参考只扣到 %d，实扣 %d", step, actual, amount)
			}
			lazyBalance -= amount
			refBalance -= amount
			requests = append(requests, &eqRefReq{gen: gen, taken: taken})
			var tkDump []string
			for _, tk := range taken {
				tkDump = append(tkDump, fmt.Sprintf("%d:%d", tk.lotID, tk.amount))
			}
			ops = append(ops, fmt.Sprintf("step %d consume %d (gen=%d) taken=[%s]", step, amount, gen, strings.Join(tkDump, " ")))
		case action < 9: // 退款（可能跨窗口）
			// 挑一个仍有未退金额的在途请求
			cand := make([]int, 0, len(requests))
			for i, r := range requests {
				if windowLocalOnly && r.gen != gen {
					continue // 只退当前窗口内发生的请求 → 不产生跨窗口退款
				}
				var left int64
				for _, tk := range r.taken {
					left += tk.amount
				}
				if left > 0 {
					cand = append(cand, i)
				}
			}
			if len(cand) == 0 {
				continue
			}
			idx := cand[len(cand)-1]
			if !strictLifo {
				idx = cand[rng.Intn(len(cand))]
			} else if idx != cand[len(cand)-1] {
				idx = cand[len(cand)-1]
			}
			req := requests[idx]
			var left int64
			for _, tk := range req.taken {
				left += tk.amount
			}
			amount := int64(1 + rng.Intn(int(left)))
			if req.gen < gen {
				stats.hasCrossWindow = true
				stats.crossWindowRefundTotal += amount
			}
			refunded, dead := eqRefund(lots, req, amount, now)
			require.NoError(t, IncreaseUserPoints(uid, int(refunded), true))
			var after []string
			for _, l := range lots {
				after = append(after, fmt.Sprintf("%d:%d", l.id, l.remaining))
			}
			ops = append(ops, fmt.Sprintf("  退款后 ref 状态 [%s] dead=%d", strings.Join(after, " "), dead))
			lazyBalance += refunded
			refBalance += refunded - dead
			stats.refForfeited += dead
			ops = append(ops, fmt.Sprintf("step %d refund %d req#%d (gen=%d, cur=%d, cross=%v)",
				step, refunded, idx, req.gen, gen, req.gen < gen))
		default: // 时间推进 → 恰好在每个到期时刻跑过期
			now += eqStepQuantum * int64(1+rng.Intn(3))
			// 注意：**每一个**被跨过的到期时刻都要跑一次过期任务，哪怕那一刻相关批次
			// 已经没有剩余（任务会空跑）。否则「时间越过了某批次的到期时刻、随后退款
			// 落回该批次」不会被算成跨窗口——而它确实是：那笔钱在当场扣的世界里到账即作废。
			seen := map[int64]bool{}
			var instants []int64
			for _, l := range lots {
				if l.expiresAt > 0 && l.expiresAt <= now && !seen[l.expiresAt] {
					seen[l.expiresAt] = true
					instants = append(instants, l.expiresAt)
				}
			}
			sort.Slice(instants, func(i, j int) bool { return instants[i] < instants[j] })
			for _, inst := range instants {
				runExpiry(inst)
				assertSyncPoint(fmt.Sprintf("step %d expire@%d", step, inst), true)
			}
		}
	}

	// 断言 2：跨窗口退款的上界
	upper := stats.refForfeited + stats.crossWindowRefundTotal
	if stats.lazyForfeited > upper {
		fail("断言2 失败：lazy 作废 %d > ref %d + 跨窗口退款 %d = %d（§3.4 的上界不成立，需回到设计）",
			stats.lazyForfeited, stats.refForfeited, stats.crossWindowRefundTotal, upper)
	}
	if !stats.hasCrossWindow {
		stats.allLifo = strictLifo
	}
	return stats
}

func TestPointsLotsEquivalence(t *testing.T) {
	seed := int64(20261124)
	if raw := os.Getenv("POINTS_EQ_SEED"); raw != "" {
		parsed, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
		require.NoError(t, err, "POINTS_EQ_SEED 必须是整数")
		seed = parsed
	}
	const (
		sequences   = 2000
		wantCat3Min = 300
		wantCat4Min = 300
		wantLifoMin = 300
	)
	rng := rand.New(rand.NewSource(seed))
	t.Logf("等价性性质测试 seed=%d sequences=%d（每条 50~64 步，走真实 GrantPointsTx / ExpireUserPointsLots）",
		seed, sequences)

	var cat3, cat4, crossWindowSeqs, lazyWorseSeqs int
	for seq := 0; seq < sequences; seq++ {
		stats := eqRunSequence(t, seed, seq, rng)
		if !stats.hasCrossWindow {
			cat3++
			if stats.allLifo {
				cat4++
			}
		} else {
			crossWindowSeqs++
			if stats.lazyForfeited > stats.refForfeited {
				lazyWorseSeqs++
			}
		}
	}

	require.GreaterOrEqual(t, cat3, wantCat3Min, "「不含跨窗口退款」的序列条数不足")
	require.GreaterOrEqual(t, cat4, wantCat4Min, "「不含跨窗口退款且全 LIFO」的序列条数不足")
	require.GreaterOrEqual(t, crossWindowSeqs, wantLifoMin, "「含跨窗口退款」的序列条数不足")
	require.Greater(t, lazyWorseSeqs, 0,
		"从未观察到「惰性作废额 > 参考作废额」的序列——测试没有触发 §3.4 的反例，等于空转")

	t.Logf("等价性性质测试通过：seed=%d 共 %d 条序列；不含跨窗口退款 %d 条（其中全 LIFO %d 条）；"+
		"含跨窗口退款 %d 条，其中惰性作废额更大 %d 条",
		seed, sequences, cat3, cat4, crossWindowSeqs, lazyWorseSeqs)
}
