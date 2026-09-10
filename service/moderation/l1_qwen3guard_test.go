package moderation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// TestMain 初始化共享的 HTTP client。
//
// service.GetHttpClient() 返回的是由 InitHttpClient() 填充的包级变量，生产由
// main.go 在启动时调用；测试进程不跑那条路，不初始化就会在第一次 Do 时空指针。
func TestMain(m *testing.M) {
	service.InitHttpClient()
	os.Exit(m.Run())
}

// L1 的失败路径矩阵。
//
// 这组测试存在的理由：L1 里混着熔断、并发、超时预算和 fail-close，而它们的正确性
// 全都体现在「出错时做了什么」上——恰恰是手工点几下最难覆盖的部分。此前这段代码
// 改一次就引入一个新缺陷（把超长输入判成服务故障、把调用方取消当成节点故障），
// 每次都是靠人工复查才发现的。矩阵一次性把三类失败的处置钉死：
//
//	节点坏了   → 冻结，摘出轮换
//	请求坏了   → 不冻结，换节点结果一样
//	我们放弃了 → 不冻结，节点没有任何问题
//
// 改动这段代码时若打破其中任何一条，这里会立刻见红。

// guardServer 起一个可编排行为的假审核节点。
func guardServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func respondGuard(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"` + body + `"}}]}`))
	}
}

func respondStatus(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}
}

func endpoint(name, baseURL string, timeoutMS int) system_setting.ModerationEndpoint {
	return system_setting.ModerationEndpoint{
		Name:       name,
		BaseURL:    baseURL,
		Model:      "qwen3guard",
		Modality:   "text",
		TimeoutMS:  timeoutMS,
		InputLimit: 4000,
		Enabled:    true,
	}
}

// newModerator 用「标准」策略：黄赌毒政治 block，暴力等 log。
func newModerator() qwen3GuardModerator {
	return qwen3GuardModerator{
		strictness: system_setting.StrictnessStandard,
		policy: &system_setting.ModerationPolicy{
			Name:       "标准",
			Strictness: system_setting.StrictnessStandard,
			Categories: map[string]string{
				system_setting.CategoryIllegal: system_setting.CategoryActionBlock,
				system_setting.CategoryViolent: system_setting.CategoryActionLog,
			},
		},
	}
}

// resetFreezes 每个用例都从干净的熔断状态开始——冻结是包级状态，用例间会串味。
func resetFreezes(t *testing.T) {
	t.Helper()
	ClearAllFreezes()
	t.Cleanup(ClearAllFreezes)
}

func TestParseVerdictSafeAndUnsafe(t *testing.T) {
	m := newModerator()

	if v := m.parseVerdict("Safety: Safe\nCategories: None"); v.Action != ActionPass {
		t.Fatalf("safe 应判 pass，实际 %s", v.Action)
	}
	v := m.parseVerdict("Safety: Unsafe\nCategories: Non-violent Illegal Acts")
	if v.Action != ActionBlock {
		t.Fatalf("illegal 配的是 block，实际 %s", v.Action)
	}
	// 类别处置为 log 时放行但留全量记录（ActionReview），不能升级成拦截。
	if v := m.parseVerdict("Safety: Unsafe\nCategories: Violent"); v.Action != ActionReview {
		t.Fatalf("violent 配的是 log，应判 review，实际 %s", v.Action)
	}
	// 模型没说安全 ≠ 安全：输出不合格式必须判 error 交给 fail 策略，不能当放行。
	if v := m.parseVerdict("I cannot help with that"); v.Action != ActionError {
		t.Fatalf("无法解析的输出应判 error，实际 %s", v.Action)
	}
	// 未登记的类别不能丢弃，否则等于把未知风险当安全。
	v = m.parseVerdict("Safety: Unsafe\nCategories: SomeBrandNewCategory")
	if len(v.Categories) != 1 || v.Categories[0] != system_setting.CategoryUnknownUpstream {
		t.Fatalf("未登记类别应落到 unknown_upstream，实际 %v", v.Categories)
	}
	if v.Action != ActionBlock {
		t.Fatalf("未登记类别按未登记即 block 处置，实际 %s", v.Action)
	}
}

// TestFreezeMatrix 各类失败对应的冻结处置。
func TestFreezeMatrix(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		wantFrozen bool
		reason     string
	}{
		{"400 请求本身的问题", http.StatusBadRequest, false, "换节点结果一样，冻结只会误伤健康节点"},
		{"401 凭证失效", http.StatusUnauthorized, true, "key 不对，长冻结"},
		{"403 无权限", http.StatusForbidden, true, "同 401"},
		{"429 限流", http.StatusTooManyRequests, true, "中等冻结"},
		{"500 上游故障", http.StatusInternalServerError, true, "短冻结"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resetFreezes(t)
			srv := guardServer(t, respondStatus(tc.status))
			eps := []system_setting.ModerationEndpoint{endpoint("n1", srv.URL, 2000)}

			_, err := newModerator().moderateSegment(context.Background(), "文本", eps)
			if err == nil {
				t.Fatal("上游报错时不能返回成功判定")
			}
			frozen := frozenUntil("n1").After(time.Now())
			if frozen != tc.wantFrozen {
				t.Fatalf("%s：期望冻结=%v 实际=%v（%s）", tc.name, tc.wantFrozen, frozen, tc.reason)
			}
		})
	}
}

// TestFreezeOnHangingNode 节点接受连接但不响应——最常见的故障形态，必须能摘掉。
func TestFreezeOnHangingNode(t *testing.T) {
	resetFreezes(t)
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	srv := guardServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
	})
	eps := []system_setting.ModerationEndpoint{endpoint("hang", srv.URL, 150)}

	if _, err := newModerator().moderateSegment(context.Background(), "文本", eps); err == nil {
		t.Fatal("节点不响应时不能返回成功判定")
	}
	if !frozenUntil("hang").After(time.Now()) {
		t.Fatal("挂起的节点必须被冻结，否则每个请求都要在它身上烧满超时")
	}
}

// TestNoFreezeOnCallerCancel 调用方取消/预算耗尽时绝不能冻结节点。
//
// 这是最容易搞错的一条：父 ctx 一死，后面每个节点的调用都会立刻失败并带回
// status=0，而 status=0 本来是要冻结的——于是一次被中断的请求会把所有节点一起
// 冻上，拦截模式下就是整个冻结窗口内全站 503。
func TestNoFreezeOnCallerCancel(t *testing.T) {
	resetFreezes(t)
	srv := guardServer(t, respondGuard("Safety: Safe\\nCategories: None"))
	eps := []system_setting.ModerationEndpoint{
		endpoint("n1", srv.URL, 2000),
		endpoint("n2", srv.URL, 2000),
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 调用方已经放弃

	if _, err := newModerator().moderateSegment(ctx, "文本", eps); err == nil {
		t.Fatal("父 context 已取消时不该返回成功判定")
	}
	for _, name := range []string{"n1", "n2"} {
		if frozenUntil(name).After(time.Now()) {
			t.Fatalf("节点 %s 被冻结了：调用方取消不是节点的问题，冻结它会把一次中断放大成全站拒绝", name)
		}
	}
}

// TestRotationSkipsBadNode 首个节点坏、次个节点好时应轮换成功，并且只冻结坏的那个。
func TestRotationSkipsBadNode(t *testing.T) {
	resetFreezes(t)
	bad := guardServer(t, respondStatus(http.StatusInternalServerError))
	good := guardServer(t, respondGuard("Safety: Unsafe\\nCategories: Non-violent Illegal Acts"))
	eps := []system_setting.ModerationEndpoint{
		endpoint("bad", bad.URL, 2000),
		endpoint("good", good.URL, 2000),
	}

	v, err := newModerator().moderateSegment(context.Background(), "文本", eps)
	if err != nil {
		t.Fatalf("应轮换到健康节点并给出判定，实际报错：%v", err)
	}
	if v.Action != ActionBlock {
		t.Fatalf("健康节点判了 unsafe/illegal，应为 block，实际 %s", v.Action)
	}
	if !frozenUntil("bad").After(time.Now()) {
		t.Fatal("故障节点应被冻结")
	}
	if frozenUntil("good").After(time.Now()) {
		t.Fatal("健康节点不该被冻结")
	}
}

// TestAllFrozenIsDistinctError 全部节点冻结时要给出可辨识的降级态，而不是普通调用失败。
func TestAllFrozenIsDistinctError(t *testing.T) {
	resetFreezes(t)
	srv := guardServer(t, respondGuard("Safety: Safe\\nCategories: None"))
	eps := []system_setting.ModerationEndpoint{endpoint("n1", srv.URL, 2000)}
	freezeEndpoint("n1", time.Minute)

	_, err := newModerator().moderateSegment(context.Background(), "文本", eps)
	if err == nil {
		t.Fatal("全部节点冻结时不能返回成功判定")
	}
	if !contains(err.Error(), "冻结") {
		t.Fatalf("错误信息要能区分「全被冻结」这种降级态，实际：%v", err)
	}
}

// TestSuccessClearsFreeze 调用成功要解除该节点的冻结，否则熔断永远不会自愈。
func TestSuccessClearsFreeze(t *testing.T) {
	resetFreezes(t)
	srv := guardServer(t, respondGuard("Safety: Safe\\nCategories: None"))
	eps := []system_setting.ModerationEndpoint{endpoint("n1", srv.URL, 2000)}

	// 先制造一条已过期但仍留在表里的冻结记录：节点因此可以被再次尝试，
	// 而记录是否被清掉正是这里要验的。
	//
	// 不能用负数时长——freezeEndpoint 对 d<=0 直接 return，压根不写记录，
	// 那样这个用例会永远绿灯（回退验证抓出过一次）。
	freezeEndpoint("n1", 10*time.Millisecond)
	time.Sleep(30 * time.Millisecond)
	if !hasFreezeRecord("n1") {
		t.Fatal("前置条件不成立：冻结记录本应还在表里")
	}

	if _, err := newModerator().moderateSegment(context.Background(), "文本", eps); err != nil {
		t.Fatalf("冻结已过期，应正常调用：%v", err)
	}
	// 必须查内部表而不是 FrozenEndpoints()：后者会过滤掉已过期的条目，
	// 于是不管清没清都返回空，同样是个永远绿灯的断言。
	if hasFreezeRecord("n1") {
		t.Fatal("成功调用后应清掉冻结记录，否则熔断表会一直攒着无用条目")
	}
}

// TestSplitByRunesOverlap 分段必须重叠，否则违规内容卡在切分点会被切成两段无害片段。
func TestSplitByRunesOverlap(t *testing.T) {
	if got := splitByRunes("短文本", 100); len(got) != 1 {
		t.Fatalf("不超限时不该分段，实际 %d 段", len(got))
	}

	limit := segmentOverlap + 100
	text := string(make([]rune, limit*3))
	segs := splitByRunes(text, limit)
	if len(segs) < 2 {
		t.Fatalf("超限文本应分段，实际 %d 段", len(segs))
	}
	// 步长必须小于 limit，差值即重叠量。
	step := limit - segmentOverlap
	if got := len([]rune(segs[0])); got != limit {
		t.Fatalf("首段长度应为 limit=%d，实际 %d", limit, got)
	}
	total := len([]rune(text))
	// 覆盖必须完整：最后一段要落到文本末尾，不能有尾巴没被扫到。
	lastStart := (len(segs) - 1) * step
	if lastStart+len([]rune(segs[len(segs)-1])) < total {
		t.Fatal("分段未覆盖到文本末尾，会漏审尾部内容")
	}
}

// TestTotalBudgetCountsEndpoints 预算必须把节点数算进去，否则轮换到次节点时 ctx 已过期。
func TestTotalBudgetCountsEndpoints(t *testing.T) {
	one := []system_setting.ModerationEndpoint{endpoint("n1", "http://x", 2000)}
	two := append([]system_setting.ModerationEndpoint{}, one...)
	two = append(two, endpoint("n2", "http://y", 2000))

	single := totalBudget(one, 1)
	double := totalBudget(two, 1)
	if double <= single {
		t.Fatalf("两个节点的预算(%v)必须大于单节点(%v)，否则慢节点吃满后没时间轮换", double, single)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// hasFreezeRecord 报告熔断表里是否还留着该节点的条目（不论是否已过期）。
func hasFreezeRecord(name string) bool {
	freezeMu.RLock()
	defer freezeMu.RUnlock()
	_, ok := freezeUntil[name]
	return ok
}
