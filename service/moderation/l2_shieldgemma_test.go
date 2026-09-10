package moderation

import (
	"context"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/QuantumNous/new-api/types"
)

// L2 的失败矩阵。每条用例对应一个「写错了不会报错、只会静默判错」的地方。

func TestClassifyMedia(t *testing.T) {
	cases := []struct {
		name     string
		value    string
		wantType types.FileType
		wantOK   bool
	}{
		{"data-url 图片", "data:image/png;base64,AAAA", types.FileTypeImage, true},
		{"data-url 视频", "data:video/mp4;base64,AAAA", types.FileTypeVideo, true},
		// 音频这条是整张表里最要紧的：认成图片会让一个正常的语音克隆请求被
		// fail-close 拒掉，而拒绝理由显示成「审核服务不可用」，没人能反推到这里。
		{"data-url 音频必须跳过", "data:audio/wav;base64,AAAA", "", false},
		{"data-url 无 MIME", "data:;base64,AAAA", "", false},
		{"http 图片", "https://obs.example.com/ingest/2026/09/10/1/abc.png", types.FileTypeImage, true},
		{"http 视频", "https://obs.example.com/ingest/2026/09/10/1/abc.mp4", types.FileTypeVideo, true},
		{"http 音频必须跳过", "https://obs.example.com/ingest/2026/09/10/1/abc.wav", "", false},
		// 签名 URL 恒带 ?AccessKeyId=...&Signature=...，不去 query 就会拿签名串的尾巴当扩展名。
		{"签名 URL 带 query", "https://b.obs.com/k/abc.jpg?AccessKeyId=AK&Expires=1&Signature=x%2Fy.png", types.FileTypeImage, true},
		{"签名 URL 视频带 query", "https://b.obs.com/k/v.mp4?AccessKeyId=AK&Signature=zz", types.FileTypeVideo, true},
		{"task 引用跳过", "task:abc123", "", false},
		{"无扩展名 URL 跳过", "https://example.com/image", "", false},
		{"目录里有点但文件没扩展名", "https://example.com/a.b/file", "", false},
		{"空值", "", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotType, gotOK := ClassifyMedia(c.value)
			if gotOK != c.wantOK || gotType != c.wantType {
				t.Fatalf("ClassifyMedia(%q) = (%q, %v), want (%q, %v)",
					c.value, gotType, gotOK, c.wantType, c.wantOK)
			}
		})
	}
}

func TestProbabilityOfYes(t *testing.T) {
	// logprobs 里有 Yes：取它的概率，而不是看输出文本。这是 Score 能有连续值的来源。
	resp := newVisionResp("No", [][2]any{{"No", -0.01}, {"Yes", -2.0}})
	p, _, err := probabilityOfYes(resp)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if want := math.Exp(-2.0); math.Abs(p-want) > 1e-9 {
		t.Fatalf("P(Yes) = %v, want %v", p, want)
	}

	// Yes 不在 top-N：说明它低于第 N 名，远在任何合理阈值之下，按首 token 定值。
	resp = newVisionResp("No", [][2]any{{"No", -0.0}, {"no", -13.0}})
	p, _, err = probabilityOfYes(resp)
	if err != nil || p != 0 {
		t.Fatalf("Yes 不在 top-N 时应为 0，得到 p=%v err=%v", p, err)
	}

	// 无 logprobs：退回读输出文本，只有 0/1 两个值。
	resp = newVisionResp("Yes", nil)
	resp.Choices[0].LogProbs = nil
	p, _, err = probabilityOfYes(resp)
	if err != nil || p != 1 {
		t.Fatalf("无 logprobs 且输出 Yes 时应为 1，得到 p=%v err=%v", p, err)
	}

	// 既不是 Yes 也不是 No：**必须报错**。返回 0（当成安全）会让一个部署错模型的
	// 节点静默放行所有图片，这是 §6.4 点名不能有的那条路径。
	resp = newVisionResp("I cannot help with that.", nil)
	resp.Choices[0].LogProbs = nil
	if _, _, err := probabilityOfYes(resp); err == nil {
		t.Fatal("无法识别的输出必须报错，不能当成通过")
	}
}

func TestNormalizeYesNo(t *testing.T) {
	// 实测 top_logprobs 里同时出现过 "No" / "no" / " No" / "NO" 四种形态。
	for _, s := range []string{"No", "no", " No", "NO", "no.", "No, the image..."} {
		if got := normalizeYesNo(s); got != "no" {
			t.Fatalf("normalizeYesNo(%q) = %q, want no", s, got)
		}
	}
	for _, s := range []string{"Yes", "yes", " Yes", "YES", "Yes, it violates"} {
		if got := normalizeYesNo(s); got != "yes" {
			t.Fatalf("normalizeYesNo(%q) = %q, want yes", s, got)
		}
	}
	if got := normalizeYesNo("maybe"); got != "" {
		t.Fatalf("normalizeYesNo(maybe) = %q, want empty", got)
	}
}

func TestFreezeDurationForVisionStatus(t *testing.T) {
	// 这一条是 L2 与 L1 的核心分歧，也是最容易被「统一一下」改错的地方：
	// vLLM 拉不到图片时返回 500 而不是 400。冻结它等于让一个用户提交的坏图 URL
	// 把健康节点逐个摘掉，拦截模式下就是全站 503。
	if d := freezeDurationForVisionStatus(http.StatusInternalServerError); d != 0 {
		t.Fatalf("500 必须不冻结（图片拉取失败是请求的问题），得到 %v", d)
	}
	if d := freezeDurationForVisionStatus(http.StatusBadRequest); d != 0 {
		t.Fatalf("400 必须不冻结，得到 %v", d)
	}
	// 反向：真正指向节点的信号仍要冻结，否则熔断形同虚设。
	if d := freezeDurationForVisionStatus(0); d <= 0 {
		t.Fatal("连不上（status=0）必须冻结，这正是熔断存在的理由")
	}
	if d := freezeDurationForVisionStatus(http.StatusUnauthorized); d != authFreezeDuration {
		t.Fatalf("401 应冻结 %v，得到 %v", authFreezeDuration, d)
	}
	if d := freezeDurationForVisionStatus(http.StatusTooManyRequests); d != rateLimitFreezeDuration {
		t.Fatalf("429 应冻结 %v，得到 %v", rateLimitFreezeDuration, d)
	}
}

func TestYesThreshold(t *testing.T) {
	if yesThreshold(system_setting.StrictnessStandard) != 0.5 {
		t.Fatal("standard 应与模型自身的 argmax 切点一致")
	}
	if yesThreshold(system_setting.StrictnessStrict) >= yesThreshold(system_setting.StrictnessStandard) {
		t.Fatal("strict 的阈值必须更低（更容易判违规）")
	}
	if yesThreshold(system_setting.StrictnessLoose) <= yesThreshold(system_setting.StrictnessStandard) {
		t.Fatal("loose 的阈值必须更高（更不容易判违规）")
	}
	// 零值（策略没配严格度）必须落到 standard，不能落到 strict——
	// 默认更严会让没配过任何东西的站点一开审就大面积误杀。
	if yesThreshold("") != yesThreshold(system_setting.StrictnessStandard) {
		t.Fatal("零值严格度应按 standard 处理")
	}
}

func TestVerdictFromScores(t *testing.T) {
	blockAll := &system_setting.ModerationPolicy{
		Categories: map[string]string{
			system_setting.CategorySexual:  system_setting.CategoryActionBlock,
			system_setting.CategoryIllegal: system_setting.CategoryActionLog,
			system_setting.CategoryViolent: system_setting.CategoryActionIgnore,
		},
	}

	t.Run("低于阈值全部放行", func(t *testing.T) {
		m := shieldGemmaModerator{strictness: system_setting.StrictnessStandard, policy: blockAll}
		v := m.verdictFromScores(imageScores{"sexual": 0.1, "dangerous": 0.2, "violence": 0.3})
		if v.Action != ActionPass {
			t.Fatalf("得到 %v，want pass", v.Action)
		}
	})

	t.Run("过阈值按类别处置", func(t *testing.T) {
		m := shieldGemmaModerator{strictness: system_setting.StrictnessStandard, policy: blockAll}
		if v := m.verdictFromScores(imageScores{"sexual": 0.9}); v.Action != ActionBlock {
			t.Fatalf("sexual 配了 block，得到 %v", v.Action)
		}
		// log = 放行但全量落库，正是 ActionReview 的语义
		if v := m.verdictFromScores(imageScores{"dangerous": 0.9}); v.Action != ActionReview {
			t.Fatalf("dangerous 配了 log，应为 review，得到 %v", v.Action)
		}
		// ignore 必须连类别都不留：留着会在日志里出现「判了类别但动作是 pass」的自相矛盾记录
		v := m.verdictFromScores(imageScores{"violence": 0.9})
		if v.Action != ActionPass || len(v.Categories) != 0 {
			t.Fatalf("violence 配了 ignore，应为 pass 且无类别，得到 %v / %v", v.Action, v.Categories)
		}
	})

	t.Run("严格度真的改变结论", func(t *testing.T) {
		// 0.3 落在 strict(0.2) 与 standard(0.5) 之间——这是阈值旋钮唯一有意义的证明：
		// 同一组分数在两个严格度下必须得出不同结论，否则这个旋钮就是摆设。
		scores := imageScores{"sexual": 0.3}
		loose := shieldGemmaModerator{strictness: system_setting.StrictnessStandard, policy: blockAll}
		strict := shieldGemmaModerator{strictness: system_setting.StrictnessStrict, policy: blockAll}
		if loose.verdictFromScores(scores).Action != ActionPass {
			t.Fatal("standard 下 0.3 应放行")
		}
		if strict.verdictFromScores(scores).Action != ActionBlock {
			t.Fatal("strict 下 0.3 应拦截")
		}
	})

	t.Run("多类别取最严", func(t *testing.T) {
		m := shieldGemmaModerator{strictness: system_setting.StrictnessStandard, policy: blockAll}
		v := m.verdictFromScores(imageScores{"sexual": 0.9, "dangerous": 0.95, "violence": 0.99})
		if v.Action != ActionBlock {
			t.Fatalf("有 block 类别时应为 block，得到 %v", v.Action)
		}
	})

	t.Run("未登记类别按 block", func(t *testing.T) {
		// 策略里没写的类别一律 block：宁可误拦一次，也不能因为「配置里没写」就放行。
		m := shieldGemmaModerator{
			strictness: system_setting.StrictnessStandard,
			policy:     &system_setting.ModerationPolicy{Categories: map[string]string{}},
		}
		if v := m.verdictFromScores(imageScores{"sexual": 0.9}); v.Action != ActionBlock {
			t.Fatalf("未登记类别应 block，得到 %v", v.Action)
		}
	})
}

func TestFramePositions(t *testing.T) {
	// 时长探不出来（直播流、损坏容器头）时只抽首帧，而不是拿 0 去算出三个相同位置。
	if got := framePositions(0); len(got) != 1 || got[0] != 0 {
		t.Fatalf("时长为 0 时应只抽首帧，得到 %v", got)
	}
	if got := framePositions(-1); len(got) != 1 {
		t.Fatalf("负时长应只抽首帧，得到 %v", got)
	}

	got := framePositions(60)
	if len(got) != 3 {
		t.Fatalf("正常视频应抽三帧，得到 %v", got)
	}
	if got[0] != 0 || got[1] != 30 || got[2] != 59.5 {
		t.Fatalf("位置应为 首/中/尾-0.5，得到 %v", got)
	}
	// 尾帧回退 0.5 秒：正好落在末尾时 seek 会落到最后一帧之后，抽出来是空。
	if got[2] >= 60 {
		t.Fatal("尾帧位置必须小于总时长，否则抽帧为空")
	}
	// 极短视频不能算出负数位置
	for _, d := range []float64{0.1, 0.4, 0.5} {
		for _, at := range framePositions(d) {
			if at < 0 {
				t.Fatalf("时长 %v 算出了负位置 %v", d, at)
			}
		}
	}
}

func TestAcquireImageSlotWaitsInsteadOfFailing(t *testing.T) {
	// 闸满时必须等待而不是立即失败。
	//
	// 立即失败会把「资源忙」上报成 ActionError，再被 fail-close 兜成 503——
	// 而闸满在图片侧是常态：单个请求带 4 段视频就要 4×3×3=36 个名额，已经超过全闸。
	// 那时 GPU 其实闲着，正常请求却被自己的限流打成服务故障。
	held := 0
	defer func() {
		for i := 0; i < held; i++ {
			releaseImageSlot()
		}
	}()
	for i := 0; i < imageConcurrency; i++ {
		if err := acquireImageSlot(context.Background()); err != nil {
			t.Fatalf("占满闸时第 %d 个就失败了: %v", i, err)
		}
		held++
	}

	// 闸已满：带 deadline 的等待应当等到超时，而不是瞬间返回。
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := acquireImageSlot(ctx)
	elapsed := time.Since(start)
	if err == nil {
		held++
		t.Fatal("闸已满时不该拿到名额")
	}
	if elapsed < 100*time.Millisecond {
		t.Fatalf("闸满时应等到 ctx 超时才失败，实际只用了 %v —— 说明又变回了立即失败", elapsed)
	}

	// 有名额释放出来时应当立刻拿到，不必等满 deadline。
	releaseImageSlot()
	held--
	ctx2, cancel2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel2()
	start = time.Now()
	if err := acquireImageSlot(ctx2); err != nil {
		t.Fatalf("有空位时应当拿到名额: %v", err)
	}
	held++
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Fatalf("有空位却等了 %v", took)
	}
}

func TestImageBudgetCountsEndpoints(t *testing.T) {
	one := []system_setting.ModerationEndpoint{{TimeoutMS: 5000}}
	two := []system_setting.ModerationEndpoint{{TimeoutMS: 5000}, {TimeoutMS: 5000}}
	// 预算必须把节点数算进去：judgeOne 串行轮换节点，不乘的话慢节点吃满超时后，
	// 轮换到次节点的调用是在一个已过期的 context 上发出的，多节点在最需要它的
	// 故障形态（慢节点）下完全不起作用。
	if imageBudget(two) <= imageBudget(one) {
		t.Fatalf("两个节点的预算必须大于一个节点：%v vs %v", imageBudget(two), imageBudget(one))
	}
	// 上界：图片审核在同步路径上，用户在等提交返回。
	huge := []system_setting.ModerationEndpoint{{TimeoutMS: 100000}, {TimeoutMS: 100000}}
	if imageBudget(huge) > 30*time.Second {
		t.Fatalf("预算上界应为 30s，得到 %v", imageBudget(huge))
	}
	// 没填超时时要有个可用的默认值，不能是 0（0 会让 context 立即过期）。
	if imageBudget([]system_setting.ModerationEndpoint{{}}) <= 0 {
		t.Fatal("未配置超时时预算不能为 0")
	}
}

// newVisionResp 构造一个视觉判定响应。tops 为 nil 时 logprobs 结构仍在但候选为空。
func newVisionResp(content string, tops [][2]any) *visionResponse {
	resp := &visionResponse{}
	resp.Choices = make([]struct {
		Message  chatMessage `json:"message"`
		LogProbs *struct {
			Content []struct {
				Token       string `json:"token"`
				TopLogProbs []struct {
					Token   string  `json:"token"`
					LogProb float64 `json:"logprob"`
				} `json:"top_logprobs"`
			} `json:"content"`
		} `json:"logprobs"`
	}, 1)
	resp.Choices[0].Message = chatMessage{Role: "assistant", Content: content}

	lp := &struct {
		Content []struct {
			Token       string `json:"token"`
			TopLogProbs []struct {
				Token   string  `json:"token"`
				LogProb float64 `json:"logprob"`
			} `json:"top_logprobs"`
		} `json:"content"`
	}{}
	lp.Content = make([]struct {
		Token       string `json:"token"`
		TopLogProbs []struct {
			Token   string  `json:"token"`
			LogProb float64 `json:"logprob"`
		} `json:"top_logprobs"`
	}, 1)
	lp.Content[0].Token = content
	for _, tp := range tops {
		lp.Content[0].TopLogProbs = append(lp.Content[0].TopLogProbs, struct {
			Token   string  `json:"token"`
			LogProb float64 `json:"logprob"`
		}{Token: tp[0].(string), LogProb: tp[1].(float64)})
	}
	resp.Choices[0].LogProbs = lp
	return resp
}
