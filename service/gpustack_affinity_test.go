package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
)

func msg(role, content string) dto.Message {
	m := dto.Message{Role: role}
	m.SetStringContent(content)
	return m
}

func instances(n int) []GPUStackInstance {
	out := make([]GPUStackInstance, 0, n)
	for i := 1; i <= n; i++ {
		out = append(out, GPUStackInstance{ID: i, ModelID: 7, State: "running",
			WorkerIP: fmt.Sprintf("10.0.0.%d", i), Port: 40000 + i})
	}
	return out
}

// 亲和键必须在多轮之间不变，否则第二轮就会换实例、前缀缓存全废。
func TestAffinityPrefixIsStableAcrossTurns(t *testing.T) {
	doc := strings.Repeat("一份很长的文档。", 500)
	turn1 := []dto.Message{msg("user", doc)}
	turn2 := []dto.Message{
		msg("user", doc),
		msg("assistant", "第一轮的回答"),
		msg("user", "接着问"),
	}
	turn3 := append(append([]dto.Message{}, turn2...),
		msg("assistant", "第二轮的回答"), msg("user", "再问"))

	p1 := gpustackAffinityPrefix(turn1)
	p2 := gpustackAffinityPrefix(turn2)
	p3 := gpustackAffinityPrefix(turn3)

	if p1 == "" {
		t.Fatal("首轮的前缀不该为空")
	}
	if p2 != p3 {
		t.Errorf("第二、三轮的前缀应当一致\n第二轮: %.60q\n第三轮: %.60q", p2, p3)
	}
	// 第一轮只有一条消息，第二轮起多了 assistant——取前 2 条时会把它带进来，
	// 所以首轮与后续可以不同；关键是「后续各轮之间」稳定，那才是缓存生效的区间。
	if got := gpustackPickInstance(instances(5), p2); got != gpustackPickInstance(instances(5), p3) {
		t.Errorf("同一段对话在后续各轮必须选到同一实例，got %+v", got)
	}
}

// 前缀要截断：线上首条消息可达 80 万字符，整段拿去哈希既慢又无额外收益。
func TestAffinityPrefixIsBounded(t *testing.T) {
	huge := strings.Repeat("x", 500_000)
	p := gpustackAffinityPrefix([]dto.Message{msg("user", huge)})
	if len(p) > gpustackAffinityPrefixChars+64 {
		t.Errorf("前缀应被截断到约 %d 字节，实际 %d", gpustackAffinityPrefixChars, len(p))
	}
	if p == "" {
		t.Fatal("截断后不该为空")
	}
}

// 角色参与哈希：同样的文本但角色不同，不该被当成同一段对话。
func TestAffinityPrefixDistinguishesRole(t *testing.T) {
	a := gpustackAffinityPrefix([]dto.Message{msg("user", "同样的话")})
	b := gpustackAffinityPrefix([]dto.Message{msg("system", "同样的话")})
	if a == b {
		t.Error("角色不同的消息应产生不同的前缀")
	}
}

// 空消息、空内容都要降级成「不做亲和」，而不是钉到一个任意实例上。
func TestAffinityPrefixEmptyInputs(t *testing.T) {
	for name, in := range map[string][]dto.Message{
		"没有消息": {},
		"内容为空": {msg("user", "")},
		"只有空白": {msg("user", "   ")},
	} {
		if p := gpustackAffinityPrefix(in); p != "" {
			t.Errorf("%s 应得到空前缀，实际 %q", name, p)
		}
	}
}

// HRW 的意义就在这里：实例增减时只重映射 1/N，而取模会把所有会话打乱。
func TestPickInstanceRemapsOnlyAFractionWhenScalingOut(t *testing.T) {
	const n = 8
	before := instances(n)
	after := instances(n + 1)

	keys := make([]string, 0, 2000)
	for i := 0; i < 2000; i++ {
		keys = append(keys, fmt.Sprintf("会话-%d", i))
	}

	moved := 0
	for _, k := range keys {
		if gpustackPickInstance(before, k).ID != gpustackPickInstance(after, k).ID {
			moved++
		}
	}

	// 理论值 1/(n+1) ≈ 11.1%。给到 20% 的上限：超过它说明退化成了取模那类行为。
	ratio := float64(moved) / float64(len(keys))
	if ratio > 0.20 {
		t.Errorf("扩容一个实例后重映射比例 %.1f%%，HRW 应在 1/(N+1)≈11%% 附近", ratio*100)
	}
	if moved == 0 {
		t.Error("新实例从未被选中，说明哈希没有把它纳入")
	}
}

// 分布不能偏到某一台上，否则亲和会变成把流量钉死在一个实例。
func TestPickInstanceSpreadsAcrossInstances(t *testing.T) {
	insts := instances(5)
	hits := map[int]int{}
	for i := 0; i < 5000; i++ {
		hits[gpustackPickInstance(insts, fmt.Sprintf("会话-%d", i)).ID]++
	}
	if len(hits) != len(insts) {
		t.Fatalf("应覆盖全部 %d 个实例，实际只用到 %d 个", len(insts), len(hits))
	}
	for id, n := range hits {
		if share := float64(n) / 5000; share < 0.10 || share > 0.30 {
			t.Errorf("实例 %d 占比 %.1f%%，偏离均分 20%% 过多", id, share*100)
		}
	}
}

func TestDirectBaseURLAndHashKey(t *testing.T) {
	inst := GPUStackInstance{ID: 23, ModelID: 7, WorkerIP: "10.0.0.7", Port: 40006}
	if got := inst.DirectBaseURL(); got != "http://10.0.0.7:40006" {
		t.Errorf("直连地址 = %q，期望 http://10.0.0.7:40006", got)
	}
	// HRW 的盐必须是身份而不是地址：换了端口仍要落到同一个哈希桶，否则实例重启
	// 会把它承接的那批会话全部重映射，缓存白丢。
	moved := GPUStackInstance{ID: 23, ModelID: 7, WorkerIP: "10.0.0.9", Port: 41111}
	if inst.hashKey() != moved.hashKey() {
		t.Errorf("换地址后 hashKey 变了：%q vs %q", inst.hashKey(), moved.hashKey())
	}
	if inst.hashKey() != "model-7-23" {
		t.Errorf("hashKey = %q，期望 model-7-23", inst.hashKey())
	}
}

// 开关关、key 为空、消息为空都必须返回空串（不发头 → 退回 GPUStack 自己的分流），
// 而且都不能去打管理 API。
func TestAffinityHeaderDeclinesWithoutConfig(t *testing.T) {
	cases := map[string]struct {
		setting  dto.ChannelSettings
		messages []dto.Message
	}{
		"开关未开": {
			dto.ChannelSettings{GPUStackAffinityKey: "k"},
			[]dto.Message{msg("user", "你好")},
		},
		"没有 key": {
			dto.ChannelSettings{GPUStackAffinity: true},
			[]dto.Message{msg("user", "你好")},
		},
		"消息为空": {
			dto.ChannelSettings{GPUStackAffinity: true, GPUStackAffinityKey: "k"},
			nil,
		},
	}
	for name, tc := range cases {
		// BaseURL 指向一个必然连不通的地址：真去打了就会超时，测试会明显变慢。
		got := GPUStackAffinityBaseURL(tc.setting, 1, "http://127.0.0.1:1", "m", tc.messages)
		if got != "" {
			t.Errorf("%s 应返回空串，实际 %q", name, got)
		}
	}
}

// 回归：首条内容落在 2033~2040 字节时，第二条消息上的剩余预算会变成负数，
// 旧实现 content[:负数] 直接 panic。这正是本功能的目标流量形状（模板化的
// 2 KB 首条消息），而原有测试只覆盖了极小和 500 KB 两端，刚好跳过这个窗口。
func TestAffinityPrefixNoPanicAroundBudgetBoundary(t *testing.T) {
	for n := 2000; n <= 2100; n++ {
		msgs := []dto.Message{
			msg("user", strings.Repeat("x", n)),
			msg("assistant", "第一轮的回答"),
			msg("user", "接着问"),
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("首条 %d 字节时 panic: %v", n, r)
				}
			}()
			if p := gpustackAffinityPrefix(msgs); len(p) > gpustackAffinityPrefixChars {
				t.Fatalf("首条 %d 字节时前缀超预算: %d", n, len(p))
			}
		}()
	}
}

// 预算耗尽后不再写入，但已写入的内容要保留（否则整条键变空、退回随机分流）。
func TestAffinityPrefixStaysWithinBudget(t *testing.T) {
	msgs := []dto.Message{
		msg("user", strings.Repeat("a", 5000)),
		msg("assistant", strings.Repeat("b", 5000)),
	}
	p := gpustackAffinityPrefix(msgs)
	if p == "" {
		t.Fatal("首条内容足够，不该得到空前缀")
	}
	if len(p) > gpustackAffinityPrefixChars {
		t.Errorf("前缀 %d 字节，超出预算 %d", len(p), gpustackAffinityPrefixChars)
	}
}
