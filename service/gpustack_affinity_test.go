package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
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

// 在飞记账是全局状态，测试之间要清干净，不然计数会互相串味。
func resetGPUMemLoad() {
	gpustackMem.mu.Lock()
	defer gpustackMem.mu.Unlock()
	gpustackMem.entries = map[string]map[string]gpustackMemLoadEntry{}
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

	p1 := gpustackAffinityContentKey(turn1)
	p2 := gpustackAffinityContentKey(turn2)
	p3 := gpustackAffinityContentKey(turn3)

	if p1 == "" {
		t.Fatal("首轮的键不该为空")
	}
	if p2 != p3 {
		t.Errorf("第二、三轮的键应当一致\n第二轮: %.60q\n第三轮: %.60q", p2, p3)
	}
	// 第一轮只有一条消息，第二轮起多了 assistant——取前 2 条时会把它带进来，
	// 所以首轮与后续可以不同；关键是「后续各轮之间」稳定，那才是缓存生效的区间。
	if got := gpustackPickInstance(instances(5), p2); got != gpustackPickInstance(instances(5), p3) {
		t.Errorf("同一段对话在后续各轮必须选到同一实例，got %+v", got)
	}
}

// 前缀要截断：线上首条消息可达 80 万字符，整段拿去哈希既慢又无额外收益。
func TestAffinityKeyIsBounded(t *testing.T) {
	huge := strings.Repeat("x", 500_000)
	k := gpustackAffinityContentKey([]dto.Message{msg("user", huge)})
	// 单消息至多 头+中+尾 3×512 + 角色/分隔符开销。
	if len(k) > 3*gpustackAffinityKeySampleLen+64 {
		t.Errorf("键应被采样到约 %d 字节，实际 %d", 3*gpustackAffinityKeySampleLen, len(k))
	}
	if k == "" {
		t.Fatal("采样后不该为空")
	}
}

// 回归：10-18 事故的根因。两条消息共享同一巨大模板头（>2KB）但尾部内容不同
// 时，旧实现只哈希头部 2048B，逐字节相同 → 全量流量钉到同一实例。头/中/尾采样
// 必须让不同内容产生不同键。
func TestAffinityKeyDifferentiatesSharedTemplateTail(t *testing.T) {
	sharedHead := strings.Repeat("共享模板头部。", 300)
	docA := sharedHead + "文档A的正文" + strings.Repeat("a", 5000) + "A的结尾"
	docB := sharedHead + "文档B的正文" + strings.Repeat("b", 5000) + "B的结尾"
	ka := gpustackAffinityContentKey([]dto.Message{msg("user", docA)})
	kb := gpustackAffinityContentKey([]dto.Message{msg("user", docB)})
	if ka == kb {
		t.Error("共享模板头但正文不同的消息必须产生不同的键")
	}
}

// 同上但区分位在第二条消息：系统提示全共享（线上 ~370KB），首轮 user 不同。
func TestAffinityKeyDifferentiatesSameSystemPrompt(t *testing.T) {
	sys := strings.Repeat("You are an AI agent powered by DeepSeek Harness. ", 8000)
	ka := gpustackAffinityContentKey([]dto.Message{msg("system", sys), msg("user", "任务A")})
	kb := gpustackAffinityContentKey([]dto.Message{msg("system", sys), msg("user", "任务B")})
	if ka == kb {
		t.Error("共享系统提示下不同的首轮提问必须产生不同的键")
	}
}

// 同一份文档发起的不同会话仍然收拢——共享前缀缓存是收益，不能散掉。
func TestAffinityKeyConvergesIdenticalPrefix(t *testing.T) {
	doc := strings.Repeat("同一份大文档。", 3000)
	m := []dto.Message{msg("system", "提示"), msg("user", doc)}
	if gpustackAffinityContentKey(m) != gpustackAffinityContentKey(m) {
		t.Error("完全相同的请求必须产生相同的键")
	}
}

// 角色参与哈希：同样的文本但角色不同，不该被当成同一段对话。
func TestAffinityKeyDistinguishesRole(t *testing.T) {
	a := gpustackAffinityContentKey([]dto.Message{msg("user", "同样的话")})
	b := gpustackAffinityContentKey([]dto.Message{msg("system", "同样的话")})
	if a == b {
		t.Error("角色不同的消息应产生不同的键")
	}
}

// 空消息、空内容都要降级成「不做亲和」，而不是钉到一个任意实例上。
func TestAffinityKeyEmptyInputs(t *testing.T) {
	for name, in := range map[string][]dto.Message{
		"没有消息": {},
		"内容为空": {msg("user", "")},
		"只有空白": {msg("user", "   ")},
	} {
		if k := gpustackAffinityContentKey(in); k != "" {
			t.Errorf("%s 应得到空键，实际 %q", name, k)
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
		got := GPUStackAffinityBaseURL(nil, tc.setting, 1, "http://127.0.0.1:1", "m",
			&dto.GeneralOpenAIRequest{Messages: tc.messages})
		if got != "" {
			t.Errorf("%s 应返回空串，实际 %q", name, got)
		}
	}
}

// 分段采样的边界（整段/头中尾切换点 3×512=1536 字节附近）都不能 panic，
// 也不能写出超出预算的键。
func TestAffinityKeyNoPanicAroundSampleBoundary(t *testing.T) {
	for n := 1500; n <= 2100; n++ {
		msgs := []dto.Message{
			msg("user", strings.Repeat("x", n)),
			msg("assistant", strings.Repeat("y", 700)),
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("首条 %d 字节时 panic: %v", n, r)
				}
			}()
			k := gpustackAffinityContentKey(msgs)
			if len(k) > 2*(3*gpustackAffinityKeySampleLen)+128 {
				t.Fatalf("首条 %d 字节时键超预算: %d", n, len(k))
			}
		}()
	}
}

// 在飞数顶到上限后，同键请求溢出到 HRW 次选；释放一个位置后回到首选。
func TestPickSpillsOnInflightCount(t *testing.T) {
	resetGPUMemLoad()
	insts := instances(4)
	ranked := gpustackRankInstances(insts, "会话")
	for i := 0; i < 6; i++ {
		gpustackLoadAcquire(ranked[0].hashKey(), gpustackLoadMember(fmt.Sprintf("r%d", i), 1))
	}
	picked, spilled, _ := gpustackPickWithLoad(insts, "会话", 6, 3*1024*1024)
	if !spilled || picked != ranked[1] {
		t.Fatalf("首选在飞满时应溢出到次选 %+v，实际 %+v spilled=%v", ranked[1], picked, spilled)
	}
	gpustackLoadRelease(ranked[0].hashKey(), gpustackLoadMember("r0", 1))
	picked, spilled, _ = gpustackPickWithLoad(insts, "会话", 6, 3*1024*1024)
	if spilled || picked != ranked[0] {
		t.Fatalf("释放后应回到首选 %+v，实际 %+v spilled=%v", ranked[0], picked, spilled)
	}
}

// 字节维度才是 KV 饱和的主信号：一个大请求就该让后续同键请求溢出。
func TestPickSpillsOnInflightBytes(t *testing.T) {
	resetGPUMemLoad()
	insts := instances(4)
	ranked := gpustackRankInstances(insts, "会话")
	gpustackLoadAcquire(ranked[0].hashKey(), gpustackLoadMember("big", 4*1024*1024))
	picked, spilled, _ := gpustackPickWithLoad(insts, "会话", 6, 3*1024*1024)
	if !spilled || picked != ranked[1] {
		t.Fatalf("首选在飞字节超限时应溢出到次选 %+v，实际 %+v spilled=%v", ranked[1], picked, spilled)
	}
}

// 所有候选都超限时退化成 least-loaded：不为亲和牺牲可用性。
func TestPickLeastLoadedWhenAllSaturated(t *testing.T) {
	resetGPUMemLoad()
	insts := instances(4)
	ranked := gpustackRankInstances(insts, "会话")
	for i, inst := range ranked {
		// 大家都超字节上限，但 ranked[2] 最轻。
		bytes := int64(5*1024*1024 + i*1024*1024)
		if i == 2 {
			bytes = 4*1024*1024 - 1 // 刚压线以下不行，要超上限但最少
		}
		gpustackLoadAcquire(inst.hashKey(), gpustackLoadMember(fmt.Sprintf("r%d", i), bytes))
	}
	picked, spilled, _ := gpustackPickWithLoad(insts, "会话", 6, 3*1024*1024)
	if !spilled || picked != ranked[2] {
		t.Fatalf("全饱和时应选最轻的 %+v，实际 %+v spilled=%v", ranked[2], picked, spilled)
	}
}

// 两个阈值都关时是纯亲和：满载也永远钉第一名，不做任何溢出。
func TestPickPureAffinityWhenCapsDisabled(t *testing.T) {
	resetGPUMemLoad()
	insts := instances(4)
	ranked := gpustackRankInstances(insts, "会话")
	gpustackLoadAcquire(ranked[0].hashKey(), gpustackLoadMember("big", 1<<62))
	picked, spilled, _ := gpustackPickWithLoad(insts, "会话", -1, -1)
	if spilled || picked != ranked[0] {
		t.Fatalf("阈值全关时应无条件选首选 %+v，实际 %+v spilled=%v", ranked[0], picked, spilled)
	}
}

// member 编码要往返一致：字节数嵌在尾段，reqID 里允许出现冒号。
func TestLoadMemberRoundTrip(t *testing.T) {
	m := gpustackLoadMember("req:a:b", 12345)
	if got := gpustackLoadMemberBytes(m); got != 12345 {
		t.Errorf("member %q 解析字节数 = %d，期望 12345", m, got)
	}
	if gpustackLoadMemberBytes("nobytes") != 0 || gpustackLoadMemberBytes("x:") != 0 {
		t.Error("缺字节段的 member 应解析为 0")
	}
}

// 构造 Claude Code 形状的请求：system 是数组、user content 也是数组，两者都带
// cache_control。经过 ClaudeToOpenAIRequest 后，这些消息的 Content 会是
// []dto.MediaContent——StringContent() 读不出来，正是 B1 的复现条件。
func claudeRequestForTest(system string, userTexts ...string) dto.ClaudeRequest {
	req := dto.ClaudeRequest{Model: "claude-sonnet-4-20250514"}
	sysText := system
	req.System = []dto.ClaudeMediaMessage{{
		Type:         "text",
		Text:         &sysText,
		CacheControl: []byte(`{"type":"ephemeral"}`),
	}}
	for _, text := range userTexts {
		t := text
		req.Messages = append(req.Messages, dto.ClaudeMessage{
			Role: "user",
			Content: []dto.ClaudeMediaMessage{{
				Type:         "text",
				Text:         &t,
				CacheControl: []byte(`{"type":"ephemeral"}`),
			}},
		})
	}
	return req
}

func claudeKeyForTest(t *testing.T, req dto.ClaudeRequest) string {
	t.Helper()
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
		ChannelType: constant.ChannelTypeOpenAI,
	}}
	converted, err := ClaudeToOpenAIRequest(req, info)
	if err != nil {
		t.Fatalf("Claude 转换失败: %v", err)
	}
	return gpustackAffinityContentKey(converted.Messages)
}

// B1 回归：同一个共享 system（Claude Code 的常态），不同会话的首条 user 必须
// 产生不同的键。修复前数组 content 被 StringContent 读成空串，所有会话只剩
// system 参与哈希，会全部塌到一台实例——10-18 事故的复现路径。
func TestClaudeMediaContentKeyDoesNotCollapse(t *testing.T) {
	sharedSystem := strings.Repeat("You are Claude Code, a shared system prompt. ", 2000)
	ka := claudeKeyForTest(t, claudeRequestForTest(sharedSystem, "任务A：修复登录 bug"))
	kb := claudeKeyForTest(t, claudeRequestForTest(sharedSystem, "任务B：重构支付模块"))
	if ka == "" {
		t.Fatal("Claude 数组 content 的键不该为空（system 与 user 都必须参与）")
	}
	if ka == kb {
		t.Fatal("共享 system 下不同首条 user 必须产生不同键，否则会话会塌到同一实例")
	}
}

// 同一会话多轮之间键必须不变。Claude 每轮重发完整历史，前两条非空消息
// （system + 首条 user）始终不变；后续追加的 assistant/tool_result 不能影响键。
func TestClaudeMediaContentKeyStableAcrossTurns(t *testing.T) {
	system := "共享系统提示"
	req1 := claudeRequestForTest(system, "第一轮的问题")
	k1 := claudeKeyForTest(t, req1)

	req2 := claudeRequestForTest(system, "第一轮的问题")
	req2.Messages = append(req2.Messages,
		dto.ClaudeMessage{Role: "assistant", Content: []dto.ClaudeMediaMessage{{Type: "text", Text: strPtr("第一轮回答")}}},
		dto.ClaudeMessage{Role: "user", Content: []dto.ClaudeMediaMessage{{Type: "text", Text: strPtr("第二轮追问")}}},
	)
	k2 := claudeKeyForTest(t, req2)

	req3 := claudeRequestForTest(system, "第一轮的问题")
	req3.Messages = append(req3.Messages,
		dto.ClaudeMessage{Role: "assistant", Content: []dto.ClaudeMediaMessage{{Type: "text", Text: strPtr("第一轮回答")}}},
		dto.ClaudeMessage{Role: "user", Content: []dto.ClaudeMediaMessage{{Type: "text", Text: strPtr("第二轮追问")}}},
		dto.ClaudeMessage{Role: "assistant", Content: []dto.ClaudeMediaMessage{{Type: "tool_use", Id: "t1", Name: "bash", Input: map[string]any{"cmd": "ls"}}}},
		dto.ClaudeMessage{Role: "user", Content: []dto.ClaudeMediaMessage{{Type: "tool_result", ToolUseId: "t1", Content: "file.txt"}}},
	)
	k3 := claudeKeyForTest(t, req3)

	if k1 == "" || k2 == "" || k3 == "" {
		t.Fatalf("多轮键不该为空: %q %q %q", k1, k2, k3)
	}
	if k1 != k2 || k2 != k3 {
		t.Fatalf("同一会话各轮键必须一致\n第1轮: %.80q\n第2轮: %.80q\n第3轮: %.80q", k1, k2, k3)
	}
}

// chat 入口的回归：string、[]any、[]dto.MediaContent 三种同文本形态必须给出
// 同一个键。前两种是现有 chat 请求的形态（键保持不变），第三种是新修复的。
func TestContentKeySameTextAcrossContentShapes(t *testing.T) {
	const text = "同一段文本"

	stringMsg := msg("user", text)

	arrayMsg := dto.Message{Role: "user", Content: []any{
		map[string]any{"type": "text", "text": text},
	}}

	mediaMsg := dto.Message{Role: "user"}
	mediaMsg.SetMediaContent([]dto.MediaContent{{Type: dto.ContentTypeText, Text: text}})

	ks := gpustackAffinityContentKey([]dto.Message{stringMsg})
	ka := gpustackAffinityContentKey([]dto.Message{arrayMsg})
	km := gpustackAffinityContentKey([]dto.Message{mediaMsg})

	if ks == "" {
		t.Fatal("string content 的键不该为空")
	}
	if ks != ka || ks != km {
		t.Fatalf("同文本不同 content 形态应得到同一键: string=%.40q []any=%.40q media=%.40q", ks, ka, km)
	}
}

// 纯图片消息仍要跳过：没有文本可区分，拿它成键会把所有纯图片请求钉到一台。
func TestContentKeySkipsMediaOnlyContent(t *testing.T) {
	m := dto.Message{Role: "user"}
	m.SetMediaContent([]dto.MediaContent{{
		Type:     dto.ContentTypeImageURL,
		ImageUrl: &dto.MessageImageUrl{Url: "data:image/png;base64,AAAA"},
	}})
	if k := gpustackAffinityContentKey([]dto.Message{m}); k != "" {
		t.Fatalf("纯图片消息不该产生键，实际 %q", k)
	}
}

func strPtr(s string) *string { return &s }
