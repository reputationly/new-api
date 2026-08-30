package gpustackplus

import (
	"strings"
	"testing"
)

// LTX-2.5 请求整形与准入校验的回归测试。
//
// 这里锁住的每一条都是「写错了不会报错、只会默默 500 或默默不生效」的那类约定 ——
// 帧数栅格发错引擎直接拒、seconds 留在 body 里就是一条恒失败的路径、
// wan 的 target_video_length 混进来则是排查时的噪声。正因为静默,才必须由测试守住。

// mustApply 跑一遍整形并断言没被准入校验拒掉。
func mustApply(t *testing.T, body map[string]any, durationSec int) {
	t.Helper()
	if err := applyLTX25Request(body, durationSec); err != nil {
		t.Fatalf("applyLTX25Request 意外返回错误: %v", err)
	}
}

// 时长换算:**按尺寸**向上吸附到该尺寸的合法栅格。
//
// 栅格同时扛两个约束,少一个都会在生产上炸:
//   - 8k+1:引擎硬校验,发错直接 500;
//   - seq_len = P×T 被 SP(现网 4)整除:P=(W/32)×(H/32) 决定栅格粗细,
//     m = 4/gcd(P,4),合法帧数 F ≡ 8(m-1)+1 (mod 8m)。
//
// 曾经把 m=2 那一行(≡9 mod 16)当成全局常量,于是奇数 P 的尺寸一开放就 500 ——
// 2026-08-30 4 卡实测 544×544 的 361 帧报 `seq_len=13294 not divisible by
// sequence_parallel_size=4`,13294 = 289×46,与公式完全吻合。下面的 544x544/15s
// 与 /16s 两条就是那两发 500 的回归锁。
func TestLTX25DurationToFrames(t *testing.T) {
	cases := []struct {
		size        string
		durationSec int
		wantFrames  int
	}{
		// m=2(P≡2 mod 4):与按尺寸换算之前完全一致,纯增量不回归
		{"960x544", 5, 121},
		{"960x544", 10, 249},
		{"960x544", 15, 361},
		{"960x544", 18, 441},
		{"1248x704", 5, 121},
		{"1248x704", 10, 249},
		{"1248x704", 14, 345},
		{"928x704", 10, 249},
		// m=4(P 为奇数):15/16 秒必须落到 377/409,按老栅格给的 361/393 是 500
		{"544x544", 5, 121},
		{"544x544", 10, 249},
		{"544x544", 14, 345},
		{"544x544", 15, 377},
		{"544x544", 16, 409},
		{"736x544", 15, 377},
		// m=1(P≡0 mod 4):栅格最细,24d 向上取到最近的 8k+1
		{"704x704", 5, 121},
		{"704x704", 10, 241},
		{"704x704", 15, 361},
		// size 缺失(API 直连可能不给):退回 ≡9 (mod 16),对所有偶数 P 尺寸安全。
		// 档位词不再走这条兜底 —— 它由 ltx25ApplyCanvas 合成成像素串或就地 400,
		// 见 TestLTX25CanvasFromTokenAndRatio / TestLTX25RejectsIllegalSizeToken。
		{"", 10, 249},
	}
	for _, tc := range cases {
		body := map[string]any{}
		if tc.size != "" {
			body["size"] = tc.size
		}
		mustApply(t, body, tc.durationSec)

		got, ok := body["num_frames"].(int)
		if !ok || got != tc.wantFrames {
			t.Fatalf("%s/%ds: num_frames = %v, want %d", tc.size, tc.durationSec, body["num_frames"], tc.wantFrames)
		}
		if (got-1)%8 != 0 {
			t.Fatalf("%s/%ds: num_frames %d 不在 8k+1 栅格上,引擎会 500", tc.size, tc.durationSec, got)
		}
		// seq_len = P×T 必须被 SP 整除,否则多卡下去噪阶段直接 500。
		// 只在能解析出尺寸时验得了 —— 这正是按尺寸换算的全部意义。
		if w, h, okSize := ltx25DimsOf(tc.size); okSize {
			p := (w / 32) * (h / 32)
			if seq := p * ((got-1)/8 + 1); seq%ltx25SequenceParallelSize != 0 {
				t.Fatalf("%s/%ds: num_frames %d 的 seq_len=%d 除不尽 SP=%d,引擎会 500",
					tc.size, tc.durationSec, got, seq, ltx25SequenceParallelSize)
			}
		}
		// 实际时长不得短于对外承诺 —— 就近吸附会让 10 秒落到 233 帧(9.71 s),那是违约
		if float64(got)/24.0 < float64(tc.durationSec) {
			t.Fatalf("%s/%ds: %d 帧只有 %.3f s,短于承诺", tc.size, tc.durationSec, got, float64(got)/24.0)
		}
		if fps, ok := body["fps"].(int); !ok || fps != 24 {
			t.Fatalf("%s/%ds: fps = %v, want 24", tc.size, tc.durationSec, body["fps"])
		}
	}
}

// ltx25DimsOf 是测试侧的小工具,复用生产解析口径。
func ltx25DimsOf(size string) (int, int, bool) {
	return ltx25Dims(map[string]any{"size": size})
}

// 吸附必须是**向上**的:栅格上的值原样保留,栅格外的值只能往大了走。
// 这条独立于具体尺寸,任何栅格都得成立。
func TestLTX25FramesNeverRoundDown(t *testing.T) {
	for _, size := range []string{"960x544", "544x544", "704x704", "1248x704"} {
		w, h, _ := ltx25DimsOf(size)
		step, first := ltx25FrameGrid(w, h)
		for d := 1; d <= 20; d++ {
			got := ltx25FramesForDuration(d, w, h, true)
			if got < d*24 {
				t.Fatalf("%s/%ds: %d 帧 < %d(向下吸附了)", size, d, got, d*24)
			}
			if (got-first)%step != 0 {
				t.Fatalf("%s/%ds: %d 不在 %d+%dk 栅格上", size, d, got, first, step)
			}
			// 只能是「够用的最小一格」:再退一格就不够了
			if got-step >= d*24 {
				t.Fatalf("%s/%ds: %d 吸得太远,%d 已经够用", size, d, got, got-step)
			}
		}
	}
}

// R2 准入:非 32 对齐的尺寸就地 400,且文案要给出最接近的合法尺寸。
// 700x400 短边合规、只违对齐,单独锁住对齐这一条的报法。
func TestLTX25RejectsUnalignedSize(t *testing.T) {
	body := map[string]any{"size": "700x400"}
	err := applyLTX25Request(body, 10)
	if err == nil {
		t.Fatal("700x400 应被拒(700 不是 32 的倍数)")
	}
	// 建议值指向菜单上的桶(700x400 的 1.75 最接近 16:9 那档),而不是
	// 「就近对齐到 32」算出来的 704x416 —— 那是个没配过也没定过价的野值
	if !strings.Contains(err.Error(), "1248x704") {
		t.Fatalf("错误文案要给出菜单上最接近的画幅 1248x704,实际: %v", err)
	}
	if _, ok := body["num_frames"]; ok {
		t.Fatal("尺寸不合法时不该再算帧数")
	}
}

// R2 准入:短边超 704 就地 400。1080p 交超分链路,不在这个模型上开。
func TestLTX25RejectsShortEdgeOverCap(t *testing.T) {
	err := applyLTX25Request(map[string]any{"size": "1920x1080"}, 5)
	if err == nil {
		t.Fatal("1920x1080 应被拒(短边 1080 > 704)")
	}
	if !strings.Contains(err.Error(), "短边上限") {
		t.Fatalf("两条全违时要报更根本的短边上限那条,实际: %v", err)
	}
}

// 建议值必须落在**上线菜单的官方桶**上,而不只是「随便一个合法尺寸」。
//
// 两层要求,少一层都不够:
//  1. 自己合法 —— 这条是踩出来的:只按 32 就近对齐时,1280x720 的 720 会被抬到 736,
//     仍然超过 704 的短边上限,等于给了个假建议;
//  2. 在菜单上 —— 就近对齐还会算出 704x416 这种引擎收得下、但没配过也没定过价的野值。
//     运营只配 704P 的五个宽高比,建议值就该指向其中之一。
//
// 挑桶按**画幅**而不是面积:用户选的是构图。1280x720 要的是 16:9,给 1248x704(1.773)
// 才是同一个画幅;按面积挑会给出一个画幅不对的桶,构图整个变了。
func TestLTX25SuggestedSizeIsOfficialBucket(t *testing.T) {
	// 上线菜单的五个桶(含转置),建议值只能是它们之一
	menu := map[[2]int]bool{
		{1248, 704}: true, {704, 1248}: true, // 16:9 / 9:16
		{928, 704}: true, {704, 928}: true, // 4:3 / 3:4
		{704, 704}: true, // 1:1
	}
	for _, tc := range []struct{ w, h, wantW, wantH int }{
		{1280, 720, 1248, 704},  // 16:9 → 归到 16:9 的桶,不是把宽留在 1280
		{1920, 1080, 1248, 704}, // 同一个画幅归到同一个桶
		{720, 1280, 704, 1248},  // 竖版:跟随朝向自动转置
		{1080, 1920, 704, 1248},
		{1024, 768, 928, 704}, // 4:3
		{768, 1024, 704, 928}, // 3:4
		{1000, 1000, 704, 704},
		{700, 400, 1248, 704}, // 短边合规但不对齐;1.75 最接近 16:9 那个桶
	} {
		sw, sh := ltx25SuggestSize(tc.w, tc.h)
		if sw != tc.wantW || sh != tc.wantH {
			t.Fatalf("%dx%d 的建议值 = %dx%d, want %dx%d", tc.w, tc.h, sw, sh, tc.wantW, tc.wantH)
		}
		if !menu[[2]int{sw, sh}] {
			t.Fatalf("%dx%d 的建议值 %dx%d 不在上线菜单上", tc.w, tc.h, sw, sh)
		}
		if err := ltx25ValidateSize(sw, sh); err != nil {
			t.Fatalf("%dx%d 的建议值 %dx%d 自己就不合法: %v", tc.w, tc.h, sw, sh, err)
		}
	}
}

// 菜单上的五个桶必须条条自洽:合法、且 18 秒(菜单最长档)不超包络。
// 这是「上线菜单不会被自己的准入校验拒掉」的总闸 —— 破了就是配置一上线全站 400。
func TestLTX25OfficialMenuPassesItsOwnGates(t *testing.T) {
	for _, s := range [][2]int{
		{1248, 704}, {704, 1248}, {928, 704}, {704, 928}, {704, 704},
	} {
		w, h := s[0], s[1]
		if err := ltx25ValidateSize(w, h); err != nil {
			t.Fatalf("菜单尺寸 %dx%d 过不了尺寸校验: %v", w, h, err)
		}
		for _, d := range []int{5, 6, 10, 14, 18} {
			frames := ltx25FramesForDuration(d, w, h, true)
			if err := ltx25ValidateEnvelope(w, h, frames); err != nil {
				t.Fatalf("菜单组合 %dx%d / %ds(%d 帧)过不了包络: %v", w, h, d, frames, err)
			}
			// 顺带锁住多卡 SP 整除:菜单里每个组合都不能是那种「排队几分钟后 500」的
			p := (w / 32) * (h / 32)
			if seq := p * ((frames-1)/8 + 1); seq%ltx25SequenceParallelSize != 0 {
				t.Fatalf("菜单组合 %dx%d / %ds 的 seq_len=%d 除不尽 SP=%d",
					w, h, d, seq, ltx25SequenceParallelSize)
			}
		}
	}
}

// R2 准入:面积×帧数超包络就地 400,而不是让它排队几分钟后 OOM。
// 1248x704 的 30 秒是验收用例 #7。
func TestLTX25RejectsPixelEnvelope(t *testing.T) {
	err := applyLTX25Request(map[string]any{"size": "1248x704"}, 30)
	if err == nil {
		t.Fatal("1248x704 / 30s 应被拒(超显存包络)")
	}
	// 文案给出的「最长秒数」必须自己在包络内 —— 否则用户照着改还是失败
	maxFrames := ltx25MaxFramesForArea(1248, 704)
	if maxFrames <= 0 || 1248*704*maxFrames > ltx25MaxPixelFrames {
		t.Fatalf("建议的最大帧数 %d 自己就超包络", maxFrames)
	}
	// 实测最大点 1248×704×489(20.375 s)必须仍在包络内 —— 这是包络常量的标定点
	if 1248*704*489 > ltx25MaxPixelFrames {
		t.Fatal("包络常量卡掉了实测跑通的 1248x704x489")
	}
}

// 包络校验对**调用方自传帧数**这条路同样生效 —— 否则 API 用户能绕过体验区的档位
// 限制发出必然 OOM 的组合(设计文档 §五)。
func TestLTX25EnvelopeAppliesToCallerFrames(t *testing.T) {
	err := applyLTX25Request(map[string]any{"size": "1248x704", "num_frames": 729}, 0)
	if err == nil {
		t.Fatal("自传 729 帧 @1248x704 应被包络拒掉")
	}
}

// 反过来:合法的自传帧数不能被误伤,也不该被网关改写。
// 栅格合法性(8k+1 / SP 整除)刻意**不校验** —— SP 是部署侧的数,网关只是抄了现网的 4,
// 拿它硬拒调用方显式表达的意图误伤成本高于收益,真发错了引擎会拒。
func TestLTX25KeepsCallerFrames(t *testing.T) {
	body := map[string]any{"num_frames": 249}
	mustApply(t, body, 5) // 5 秒本会算成 121
	if got := body["num_frames"]; got != 249 {
		t.Fatalf("num_frames = %v, want 249(调用方取值被覆盖了)", got)
	}
	// 没被接管的请求也不该被补上 fps
	if _, ok := body["fps"]; ok {
		t.Fatal("调用方自传帧数时不应再补 fps")
	}
	// 不在 SP 栅格上的自传帧数照样放行,交给引擎判定
	body = map[string]any{"size": "544x544", "num_frames": 361}
	mustApply(t, body, 0)
	if got := body["num_frames"]; got != 361 {
		t.Fatalf("num_frames = %v, want 361", got)
	}
}

// seconds 必须被清掉。
//
// 引擎的 seconds 正则是 ^[1-9]\d*$(只收整数秒),而 frames = seconds×24 恒 ≡ 0 (mod 8),
// 永远取不到 8k+1 需要的余数 1 —— 实测 seconds=5 直接 500(got 120)。
// 只要这个键还在 body 里,就存在一条恒失败的路径。
func TestLTX25DropsSecondsAlways(t *testing.T) {
	// 有时长时
	body := map[string]any{"seconds": "5"}
	mustApply(t, body, 5)
	if _, ok := body["seconds"]; ok {
		t.Fatal("seconds 未被清除(有时长)")
	}
	// 没时长时同样要清:提前 return 的分支也不能把它漏下
	body = map[string]any{"seconds": "5"}
	mustApply(t, body, 0)
	if _, ok := body["seconds"]; ok {
		t.Fatal("seconds 未被清除(无时长)")
	}
	// 调用方自传帧数时同样要清
	body = map[string]any{"seconds": "5", "num_frames": 121}
	mustApply(t, body, 5)
	if _, ok := body["seconds"]; ok {
		t.Fatal("seconds 未被清除(自传 num_frames)")
	}
	// 被准入校验拒掉的那条路也要清:错误路径上 body 不会被发出去,但留着会让
	// 「先剥字段再校验」这个顺序悄悄反转,下一个人很容易把 delete 挪到 return 之后
	body = map[string]any{"seconds": "5", "size": "1280x720"}
	if err := applyLTX25Request(body, 5); err == nil {
		t.Fatal("1280x720 应被拒")
	}
	if _, ok := body["seconds"]; ok {
		t.Fatal("seconds 未被清除(校验失败路径)")
	}
}

// wan / InfiniteTalk 专属字段必须清掉:引擎侧 LTX 分支从不读它们,
// 留着不报错,只会在排查时让人以为时长可控。
func TestLTX25DropsForeignEngineFields(t *testing.T) {
	body := map[string]any{
		"target_video_length": 81, // wan 的 4n+1 @16fps
		"video_duration":      30, // InfiniteTalk 的输出时长上限
	}
	mustApply(t, body, 5)
	for _, key := range []string{"target_video_length", "video_duration"} {
		if _, ok := body[key]; ok {
			t.Fatalf("%s 未被清除", key)
		}
	}
}

// 没给时长就不写帧数:由引擎按 pipeline 默认帧数决定,门面不替它猜。
func TestLTX25NoDurationLeavesFramesUnset(t *testing.T) {
	body := map[string]any{}
	mustApply(t, body, 0)
	if _, ok := body["num_frames"]; ok {
		t.Fatal("没给时长却写了 num_frames")
	}
	if _, ok := body["fps"]; ok {
		t.Fatal("没给时长却写了 fps")
	}
}

// metadata 反序列化出来的数字是 float64,自传帧数这条路必须认它 ——
// 只认 int 的话 API 用户传的 num_frames 会被当成「没传」而被网关覆盖,静默失效。
func TestLTX25AcceptsFloatCallerFrames(t *testing.T) {
	body := map[string]any{"num_frames": float64(249)}
	mustApply(t, body, 5)
	if got := body["num_frames"]; got != float64(249) {
		t.Fatalf("num_frames = %v, want 249(float 形态的调用方取值被覆盖了)", got)
	}
}

// ── 画布合成(档位词 + 具名比例 → 精确像素) ────────────────────────────────

// 体验区给 LTX 配的是「分辨率档位 + 宽高比」(与 H3 同一套填法),网关必须把它合成
// 引擎认的像素串。推出来的六个值必须**正好落在 4 卡实测过的官方桶上** —— 这不是巧合
// 而是选型时就按 round-to-32 反推的,一旦有人把对齐改成向下取整,4:3 会从 736 掉到 704
// (画幅偏 3%),这里就会红。
func TestLTX25CanvasFromTokenAndRatio(t *testing.T) {
	cases := []struct {
		token, ratio, want string
	}{
		{"704P", "16:9", "1248x704"},
		{"704P", "9:16", "704x1248"},
		{"704P", "4:3", "928x704"},
		{"704P", "3:4", "704x928"},
		{"704P", "1:1", "704x704"},
		{"544P", "16:9", "960x544"},
		{"544P", "9:16", "544x960"},
		{"544P", "4:3", "736x544"},
		{"544P", "3:4", "544x736"},
		{"544P", "1:1", "544x544"},
		// 大小写与空格都要收:运营手敲的值不保证规整
		{"704p", "16 : 9", "1248x704"},
	}
	for _, c := range cases {
		body := map[string]any{"size": c.token, "aspect_ratio": c.ratio}
		mustApply(t, body, 5)
		if got := body["size"]; got != c.want {
			t.Errorf("%s + %s → size = %v, want %s", c.token, c.ratio, got, c.want)
		}
	}
}

// 合成出的画布要接着喂给按尺寸算的帧数栅格 —— 两段是串起来用的,分开测会漏掉
// 「画布对了但栅格按旧的 mod 16 算」这种半截生效。
//
// 544x544 是 P=289(奇数)⇒ m=4 ⇒ F ≡ 25 (mod 32),15 秒必须落 377 而不是 361;
// 361 正是 2026-08-30 实测报 seq_len=13294 的那一发。
func TestLTX25CanvasFeedsFrameGrid(t *testing.T) {
	cases := []struct {
		token, ratio string
		durationSec  int
		wantSize     string
		wantFrames   int
	}{
		{"544P", "1:1", 15, "544x544", 377},
		{"544P", "1:1", 10, "544x544", 249},
		{"544P", "16:9", 15, "960x544", 361},
		{"704P", "16:9", 14, "1248x704", 345},
		{"704P", "1:1", 14, "704x704", 337}, // m=1,栅格最细:24×14=336 → 337
	}
	for _, c := range cases {
		body := map[string]any{"size": c.token, "aspect_ratio": c.ratio}
		mustApply(t, body, c.durationSec)
		if got := body["size"]; got != c.wantSize {
			t.Errorf("%s + %s → size = %v, want %s", c.token, c.ratio, got, c.wantSize)
		}
		if got := body["num_frames"]; got != c.wantFrames {
			t.Errorf("%s + %s @%ds → num_frames = %v, want %d", c.token, c.ratio, c.durationSec, got, c.wantFrames)
		}
	}
}

// 非法档位词就地 400,不放行到引擎。
//
// 放行的代价见 2026-08-31 现网:用户拿到的是引擎的 pydantic 报错
// (`String should match pattern '^\d+x\d+$'`),既看不出该填什么,也不知道该找谁改。
// 540P / 720P 这两个尤其要挡:它们是行业通行档位,但 720 不是 32 的倍数、也超短边上限,
// 静默映射到 704 会让人以为拿到了 720p。
func TestLTX25RejectsIllegalSizeToken(t *testing.T) {
	for _, size := range []string{"720P", "540P", "1080P", "2K", "4k", "abc", "704"} {
		body := map[string]any{"size": size, "aspect_ratio": "16:9"}
		err := applyLTX25Request(body, 5)
		if err == nil {
			t.Errorf("size=%q 应被拒,却放行了(size 留在 body 上 = 引擎侧 400)", size)
			continue
		}
		if !strings.Contains(err.Error(), "LTX-2.5") {
			t.Errorf("size=%q 的错误文案没点名模型: %v", size, err)
		}
	}
}

// 档位词必须配具名比例:推不出画布时**宁可 400 也不降级**。
// 清掉档位词让引擎回落默认画布(960×544)是静默换档,比报错难查得多。
func TestLTX25TokenRequiresNamedRatio(t *testing.T) {
	for _, ar := range []string{"", "30:17", "1.78", "16/9"} {
		body := map[string]any{"size": "704P", "aspect_ratio": ar}
		if err := applyLTX25Request(body, 5); err == nil {
			t.Errorf("aspect_ratio=%q 时应拒绝(无法推出画布),却放行了", ar)
		}
	}
}

// 像素串这条路不受影响:API 直连与"运营改填精确像素"都走它。
// adaptor 会用 AspectRatioFromSize 从像素串反推一个 aspect_ratio 覆盖进来(如 30:17),
// 那不是具名比例 —— 若画布合成误把它当成"要合成"的信号,这条会红。
func TestLTX25PixelSizeUntouchedByCanvas(t *testing.T) {
	body := map[string]any{"size": "1248x704", "aspect_ratio": "30:17"}
	mustApply(t, body, 10)
	if got := body["size"]; got != "1248x704" {
		t.Fatalf("像素串被改写成 %v", got)
	}
	if got := body["num_frames"]; got != 249 {
		t.Fatalf("num_frames = %v, want 249", got)
	}
}

// 调用方自带 width/height 时不推画布,但档位词仍要清掉 —— 留着就是引擎侧 400。
func TestLTX25CallerCanvasDropsSizeToken(t *testing.T) {
	body := map[string]any{"width": 1248, "height": 704, "size": "704P"}
	mustApply(t, body, 10)
	if _, ok := body["size"]; ok {
		t.Fatalf("调用方自带画布时档位词没被清掉: %v", body["size"])
	}
	if body["width"] != 1248 || body["height"] != 704 {
		t.Fatalf("调用方的 width/height 被改动: %v x %v", body["width"], body["height"])
	}
}

// 体验区**不发 aspect_ratio**:它按引擎族二选一,LTX 落的是 wan 那条
// `metadata.target_shape=[h,w]`(usePipeline 为 true 且引擎族不是 H3)。
//
// 这条是 P1 的回归锁:只读 aspect_ratio 的话,运营把 sizes 配成档位词之后,
// 体验区每一发文生视频都是 400 —— 不是推断,是实跑复现过的。
// 值取自前端的 VIDEO_ASPECT_RATIO_TO_SHAPE(手调过的固定表)。
func TestLTX25CanvasFromTargetShape(t *testing.T) {
	cases := []struct {
		token string
		shape []any
		want  string
	}{
		{"704P", []any{720.0, 1280.0}, "1248x704"}, // 16:9
		{"704P", []any{1280.0, 720.0}, "704x1248"}, // 9:16
		{"704P", []any{960.0, 960.0}, "704x704"},   // 1:1
		{"544P", []any{768.0, 1024.0}, "736x544"},  // 4:3
		{"544P", []any{1024.0, 768.0}, "544x736"},  // 3:4
	}
	for _, c := range cases {
		body := map[string]any{"size": c.token, "target_shape": c.shape}
		mustApply(t, body, 5)
		if got := body["size"]; got != c.want {
			t.Errorf("%s + target_shape%v → size = %v, want %s", c.token, c.shape, got, c.want)
		}
		if _, ok := body["target_shape"]; ok {
			t.Errorf("%s: target_shape 是 wan 专属键,取完比例必须删掉", c.token)
		}
	}
}

// ratio 是第三方渠道的原生形态,直连调用方也可能直接发它。与 H3 同一套优先级:
// aspect_ratio > ratio > target_shape。
func TestLTX25AspectRatioAliasPriority(t *testing.T) {
	// ratio 别名生效
	body := map[string]any{"size": "704P", "ratio": "4:3"}
	mustApply(t, body, 5)
	if got := body["size"]; got != "928x704" {
		t.Fatalf("ratio 别名没生效: size = %v, want 928x704", got)
	}
	if _, ok := body["ratio"]; ok {
		t.Fatal("ratio 归一后应删掉,免得与 aspect_ratio 两个键打架")
	}

	// 三者同在时,显式 aspect_ratio 最权威
	body = map[string]any{
		"size":         "704P",
		"aspect_ratio": "1:1",
		"ratio":        "4:3",
		"target_shape": []any{720.0, 1280.0},
	}
	mustApply(t, body, 5)
	if got := body["size"]; got != "704x704" {
		t.Fatalf("显式 aspect_ratio 没压过别名: size = %v, want 704x704", got)
	}
}

// wan 专属键在**像素串**这条路上同样要清掉 —— 早退分支容易漏。
// 与 target_video_length / video_duration 同一个标准:引擎不读的键不留在 body 上。
func TestLTX25DropsTargetShapeOnPixelPath(t *testing.T) {
	body := map[string]any{
		"size":         "1248x704",
		"target_shape": []any{720.0, 1280.0},
		"ratio":        "16:9",
	}
	mustApply(t, body, 10)
	if _, ok := body["target_shape"]; ok {
		t.Error("像素串路径上 target_shape 没被清掉")
	}
	if _, ok := body["ratio"]; ok {
		t.Error("像素串路径上 ratio 没被清掉")
	}
	if got := body["size"]; got != "1248x704" {
		t.Errorf("像素串被改写成 %v", got)
	}
}
