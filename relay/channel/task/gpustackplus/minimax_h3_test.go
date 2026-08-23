package gpustackplus

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// MiniMax H3 请求整形的回归测试。
//
// 这里锁住的每一条都是「写错了不会报错、只会默默变差或默默不生效」的那类约定 ——
// 嵌套 extra_params(顶层同名键被引擎静默丢弃)、17n+5 而非 4n+1、round 而非 floor 的
// 画布对齐、以及面积钳位。正因为静默,才必须由测试而不是人眼守住。

// H3 部署:一个 FL2VA 分区同时挂「文生视频」与「关键帧」两个 tab。
// engine 声明是引擎族判据 —— 刻意用无特征的模型名,证明判据不依赖名字。
const h3Config = `{"models":{"video-h3":{"engine":"minimax-h3","tabs":{"text2video":{},"flf2v":{}}}}}`

// ── 画布推导 ────────────────────────────────────────────────────────────────

// 忠实复刻引擎 _resolve_output_canvas 的两个易错点:
//  1. 对齐是 round 不是 floor;
//  2. 超过面积上限时先等比缩再对齐。
//
// 768P/16:9 = 1344×768 正是钳位的产物:768×16/9 的面积 1,048,576 > 上限 1,032,192,
// 不钳位会算出 1376×768。这条对上了才说明钳位没漏。
func TestH3Canvas(t *testing.T) {
	cases := []struct {
		name      string
		shortEdge int
		ratio     string
		wantW     int
		wantH     int
	}{
		{"768P 16:9 触发面积钳位", 768, "16:9", 1344, 768},
		{"768P 9:16 触发面积钳位", 768, "9:16", 768, 1344},
		{"768P 21:9 触发面积钳位", 768, "21:9", 1536, 672},
		{"768P 4:3 未触发", 768, "4:3", 1024, 768},
		{"768P 1:1 未触发", 768, "1:1", 768, 768},
		{"768P 3:4 未触发", 768, "3:4", 768, 1024},
		// round 而非 floor:480×16/9 = 853.33,round(853.33/32)=27 → 864(floor 是 832)。
		{"480P 16:9 按 round 对齐", 480, "16:9", 864, 480},
		{"480P 4:3", 480, "4:3", 640, 480},
		{"480P 1:1", 480, "1:1", 480, 480},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, h := h3Canvas(tc.shortEdge, h3NamedAspectRatios[tc.ratio])
			if w != tc.wantW || h != tc.wantH {
				t.Fatalf("h3Canvas(%d, %s) = %dx%d, want %dx%d",
					tc.shortEdge, tc.ratio, w, h, tc.wantW, tc.wantH)
			}
			if w%32 != 0 || h%32 != 0 {
				t.Fatalf("画布两轴必须是 32 的倍数,得到 %dx%d", w, h)
			}
			if w*h > h3MaxOutputPixels {
				t.Fatalf("画布面积 %d 超过上限 %d", w*h, h3MaxOutputPixels)
			}
		})
	}
}

func TestH3ShortEdgeFromSizeToken(t *testing.T) {
	cases := map[string]int{
		"480P": 480, "768p": 768, " 720P ": 720,
		// 像素串不是档位词:必须取不出,否则会走进画布推导并算出错的短边。
		"832x480": 0, "1280x720": 0, "": 0, "P": 0, "abcP": 0,
	}
	for in, want := range cases {
		if got := h3ShortEdgeFromSizeToken(in); got != want {
			t.Fatalf("h3ShortEdgeFromSizeToken(%q) = %d, want %d", in, got, want)
		}
	}
}

// 为什么体验区必须把 sizes 配成档位词而不是像素串。
//
// adaptor 转发顶层 size 时会用 AspectRatioFromSize 反推 aspect_ratio 覆盖 metadata 值,
// 而 gcd 约分出的 "26:15" 不在 H3 的六个具名值里,下发过去必被引擎拒。档位词匹配不到
// WxH 正则,于是不会覆盖用户选的具名比例。
func TestPixelSizeWouldPoisonAspectRatio(t *testing.T) {
	if got := common.AspectRatioFromSize("832x480"); got != "26:15" {
		t.Fatalf("前提变了:AspectRatioFromSize(832x480) = %q,预期 26:15", got)
	}
	if h3IsNamedAspectRatio("26:15") {
		t.Fatal("26:15 不该是 H3 的具名比例")
	}
	// 档位词取不出比例 → 不会覆盖 metadata 里的具名值。
	if got := common.AspectRatioFromSize("480P"); got != "" {
		t.Fatalf("档位词不该反推出比例,得到 %q", got)
	}
}

// ── 请求整形 ────────────────────────────────────────────────────────────────

func TestH3AppliesDurationAndStepsAndCanvas(t *testing.T) {
	body := map[string]any{"size": "768P", "aspect_ratio": "16:9"}
	applyMiniMaxH3Request(body, "t2v", 8, false, 0)

	extra, ok := body["extra_params"].(map[string]any)
	if !ok {
		t.Fatal("时长必须写进嵌套 extra_params:顶层同名键会被引擎静默丢弃")
	}
	if extra["duration"] != 8.0 {
		t.Fatalf("extra_params.duration = %v, want 8.0(float 秒)", extra["duration"])
	}
	if body["num_inference_steps"] != h3DefaultInferenceSteps {
		t.Fatalf("步数 = %v, want %d(引擎兜底 50 是 2.5 倍耗时)",
			body["num_inference_steps"], h3DefaultInferenceSteps)
	}
	if body["width"] != 1344 || body["height"] != 768 {
		t.Fatalf("画布 = %vx%v, want 1344x768", body["width"], body["height"])
	}
	// 档位词对引擎的 SizeStr 是非法值,画布既已由 width/height 确定就该删掉。
	if _, exists := body["size"]; exists {
		t.Fatal("推出 width/height 后不该再留档位词 size")
	}
}

// wan / InfiniteTalk 的专属字段对 H3 必须清掉。
// 留着不会报错(引擎 H3 分支根本不读),只会在排查时误导 —— 正是最难查的一类。
func TestH3DropsWanAndInfiniteTalkFields(t *testing.T) {
	body := map[string]any{
		"target_video_length": 129, // wan 的 4n+1 @16fps
		"video_duration":      15,  // InfiniteTalk 的输出时长上限
	}
	applyMiniMaxH3Request(body, "t2v", 8, false, 0)
	for _, k := range []string{"target_video_length", "video_duration"} {
		if _, exists := body[k]; exists {
			t.Fatalf("%s 是 wan/InfiniteTalk 专属,H3 请求里不该出现", k)
		}
	}
}

// metadata 是开放透传的(API 用户可直接下发引擎旋钮),这里只补默认、不覆盖用户意图。
func TestH3DoesNotOverrideExplicitValues(t *testing.T) {
	body := map[string]any{
		"size": "768P", "aspect_ratio": "16:9",
		"num_inference_steps": 50,
		"width":               1280, "height": 720,
		"extra_params": map[string]any{"duration": 12.5},
	}
	applyMiniMaxH3Request(body, "t2v", 8, false, 0)

	if body["num_inference_steps"] != 50 {
		t.Fatalf("用户显式给的步数被覆盖了:%v", body["num_inference_steps"])
	}
	if body["width"] != 1280 || body["height"] != 720 {
		t.Fatalf("用户显式给的画布被覆盖了:%vx%v", body["width"], body["height"])
	}
	extra := body["extra_params"].(map[string]any)
	if extra["duration"] != 12.5 {
		t.Fatalf("用户显式给的时长被覆盖了:%v", extra["duration"])
	}
}

// 关键帧不在网关侧推画布:FL2VA 的画幅永远跟随第一张图(引擎静默忽略 aspect_ratio),
// 而网关拿到的是 URL/base64,不解码就不知道宽高比。硬算只会算错。
func TestH3SkipsCanvasForKeyframeTaskTypes(t *testing.T) {
	for _, tt := range []string{"i2v", "l2va", "flf2v"} {
		t.Run(tt, func(t *testing.T) {
			body := map[string]any{"size": "480P", "aspect_ratio": "16:9"}
			applyMiniMaxH3Request(body, tt, 5, false, 0)
			if _, exists := body["width"]; exists {
				t.Fatalf("%s 不该由网关推画布", tt)
			}
			// 但时长与步数照常生效。
			if body["num_inference_steps"] != h3DefaultInferenceSteps {
				t.Fatalf("%s 的步数没设上", tt)
			}
		})
	}
}

// 比例不是具名值时不猜:引擎会就此报 400,它的错误信息比我们瞎猜清楚。
func TestH3SkipsCanvasOnNonNamedRatio(t *testing.T) {
	body := map[string]any{"size": "480P", "aspect_ratio": "26:15"}
	applyMiniMaxH3Request(body, "t2v", 5, false, 0)
	if _, exists := body["width"]; exists {
		t.Fatal("非具名比例不该推出画布")
	}
}

// ── 时长白名单绕过（评审）──────────────────────────────────────────────────
//
// 上游那道 durationOverrideKeys 只剥**顶层** metadata 键
// (target_video_length / video_length / num_frames / frames),而 H3 的时长走
// extra_params 嵌套对象,完全不在它射程内。不补这一层,调用方顶层老实发白名单内的
// duration=5、同时塞 extra_params.duration=15 就能让引擎按 15 秒出片。

func TestH3StripsNestedDurationWhenLocked(t *testing.T) {
	// 三个别名都要剥:只剥 duration 会被 duration_seconds 绕过,只剥这两个会被
	// target.duration_seconds 绕过(引擎优先级链见上游契约 §4.2)。
	body := map[string]any{
		"extra_params": map[string]any{
			"duration":         15.0,
			"duration_seconds": 15.0,
			"target":           map[string]any{"duration_seconds": 15.0},
		},
	}
	applyMiniMaxH3Request(body, "t2v", 5, true, 0) // durationLocked

	extra := body["extra_params"].(map[string]any)
	if extra["duration"] != 5.0 {
		t.Fatalf("锁定时应以白名单内的顶层时长为准,得到 %v", extra["duration"])
	}
	if _, exists := extra["duration_seconds"]; exists {
		t.Fatal("duration_seconds 别名没剥掉,仍可绕过白名单")
	}
	if _, exists := extra["target"]; exists {
		t.Fatal("target.duration_seconds 剥空后应连壳一起删掉")
	}
}

// target 里还有别的合法键时,只剥时长那个,不要误伤。
func TestH3KeepsOtherTargetKeysWhenLocked(t *testing.T) {
	body := map[string]any{
		"extra_params": map[string]any{
			"target": map[string]any{"duration_seconds": 15.0, "short_edge": 768},
		},
	}
	applyMiniMaxH3Request(body, "t2v", 5, true, 0)

	target := body["extra_params"].(map[string]any)["target"].(map[string]any)
	if _, exists := target["duration_seconds"]; exists {
		t.Fatal("target.duration_seconds 应被剥掉")
	}
	if target["short_edge"] != 768 {
		t.Fatalf("target 里的其它键不该被误伤,得到 %v", target["short_edge"])
	}
}

// 没配白名单时不动用户的嵌套时长 —— metadata 是开放透传的,API 用户本就可以
// 直接下发引擎旋钮,无端剥掉是另一种错。
func TestH3KeepsNestedDurationWhenUnlocked(t *testing.T) {
	body := map[string]any{"extra_params": map[string]any{"duration": 12.5}}
	applyMiniMaxH3Request(body, "t2v", 5, false, 0)
	if got := body["extra_params"].(map[string]any)["duration"]; got != 12.5 {
		t.Fatalf("未锁定时不该动用户的嵌套时长,得到 %v", got)
	}
}

// ── 宽高比字段归一（评审第 2 条）────────────────────────────────────────────
//
// 体验区**不发 aspect_ratio**,它按 pipeline 标记二选一
// (useVideoGeneration.js:1290-1306):pipeline=false 发 metadata.ratio、
// pipeline=true 发 metadata.target_shape。而 H3 要 pipeline=false,所以真实请求里
// 到达后端的是 ratio —— 只读 aspect_ratio 会导致画布完全推不出来,并把 "480P"
// 这个非法 size 原样丢给引擎。

func TestH3AcceptsUIRatioField(t *testing.T) {
	// 体验区 pipeline=false 时的真实形态。
	body := map[string]any{"size": "768P", "ratio": "16:9"}
	applyMiniMaxH3Request(body, "t2v", 8, false, 0)

	if body["width"] != 1344 || body["height"] != 768 {
		t.Fatalf("UI 的 ratio 字段没被采纳,画布 = %vx%v", body["width"], body["height"])
	}
	if body["aspect_ratio"] != "16:9" {
		t.Fatalf("应归一成引擎认的 aspect_ratio,得到 %v", body["aspect_ratio"])
	}
	// 别名清掉,免得两个键打架。
	if _, exists := body["ratio"]; exists {
		t.Fatal("归一后不该再留 ratio 别名")
	}
}

func TestH3AspectRatioWinsOverRatioAlias(t *testing.T) {
	body := map[string]any{"size": "768P", "aspect_ratio": "4:3", "ratio": "16:9"}
	applyMiniMaxH3Request(body, "t2v", 8, false, 0)
	if body["aspect_ratio"] != "4:3" {
		t.Fatalf("显式 aspect_ratio 应优先,得到 %v", body["aspect_ratio"])
	}
	if body["width"] != 1024 || body["height"] != 768 {
		t.Fatalf("画布应按 4:3 算,得到 %vx%v", body["width"], body["height"])
	}
}

func TestH3DropsWanTargetShape(t *testing.T) {
	// pipeline=true 时体验区发的是 wan 的 720p 级固定值表,对 H3 既非 32 的倍数
	// 也不是我们要的档位,拿它反推**画布**只会得到错尺寸,故这个键本身照旧丢弃。
	body := map[string]any{"size": "768P", "ratio": "16:9", "target_shape": []any{720.0, 1280.0}}
	applyMiniMaxH3Request(body, "t2v", 8, false, 0)
	if _, exists := body["target_shape"]; exists {
		t.Fatal("target_shape 是 wan 专属,不该带到 H3")
	}
}

// 只发 target_shape、不发 ratio —— 这是 H3 在体验区的**真实**形态:H3 跑在自建
// gpustackplus 渠道上,被 video_pipeline_flag_migrated 迁移标成 pipeline:true,前端
// 于是走 target_shape 分支。原来这里直接删键,比例一路丢到 t2v 缺省分支补成 16:9,
// 用户选什么都出 16:9。
func TestH3RecoversAspectRatioFromTargetShape(t *testing.T) {
	cases := []struct {
		name  string
		shape []any
		want  string
		w, h  int
	}{
		// 前端 VIDEO_ASPECT_RATIO_TO_SHAPE 的手调固定值([height,width])。
		{"16:9", []any{720.0, 1280.0}, "16:9", 1344, 768},
		{"9:16", []any{1280.0, 720.0}, "9:16", 768, 1344},
		{"1:1", []any{960.0, 960.0}, "1:1", 768, 768},
		{"4:3", []any{768.0, 1024.0}, "4:3", 1024, 768},
		{"3:4", []any{1024.0, 768.0}, "3:4", 768, 1024},
		// 21:9 不在固定值表里,前端 aspectRatioToShape 按 ~720p 面积等比算 + 对齐 16,
		// 得 [624,1472],比真值偏 1.1% —— 容差必须容得下它。
		// 画布 1536x672 而非 1792x768:21:9 在 768 短边上超了面积上限,先等比缩再对齐。
		{"21:9", []any{624.0, 1472.0}, "21:9", 1536, 672},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body := map[string]any{"size": "768P", "target_shape": c.shape}
			applyMiniMaxH3Request(body, "t2v", 8, false, 0)
			if body["aspect_ratio"] != c.want {
				t.Fatalf("比例应从 target_shape 反推出 %s,得到 %v", c.want, body["aspect_ratio"])
			}
			if body["width"] != c.w || body["height"] != c.h {
				t.Fatalf("画布应为 %dx%d,得到 %vx%v", c.w, c.h, body["width"], body["height"])
			}
		})
	}
}

// ratio 是调用方直接表达的比例,target_shape 是反推来的,前者更权威。
func TestH3RatioWinsOverTargetShape(t *testing.T) {
	body := map[string]any{"size": "768P", "ratio": "4:3", "target_shape": []any{720.0, 1280.0}}
	applyMiniMaxH3Request(body, "t2v", 8, false, 0)
	if body["aspect_ratio"] != "4:3" {
		t.Fatalf("显式 ratio 应优先于 target_shape 反推,得到 %v", body["aspect_ratio"])
	}
}

// 反推不出来就别硬猜:偏离所有具名值超过容差时保持缺失,交给 t2v 的缺省分支补 16:9
// (那是有意的兜底),而不是塞一个最近但明显不对的比例。
func TestH3IgnoresOffGridTargetShape(t *testing.T) {
	// 1000/500 = 2.0,离最近的 21:9(2.333) 偏 14%、离 16:9(1.778) 偏 12%,都超容差。
	body := map[string]any{"size": "768P", "target_shape": []any{500.0, 1000.0}}
	applyMiniMaxH3Request(body, "t2v", 8, false, 0)
	if body["aspect_ratio"] != h3DefaultAspectRatio {
		t.Fatalf("推不出比例时应回落缺省 %s,得到 %v", h3DefaultAspectRatio, body["aspect_ratio"])
	}
}

func TestH3AspectRatioFromTargetShapeRejectsGarbage(t *testing.T) {
	for _, v := range []any{nil, "16:9", []any{}, []any{720.0}, []any{0.0, 1280.0}, []any{"a", "b"}} {
		if got := h3AspectRatioFromTargetShape(v); got != "" {
			t.Fatalf("非法 target_shape %#v 不该推出比例,得到 %q", v, got)
		}
	}
}

// 档位词对引擎的 SizeStr 是非法值。推不出画布时也**必须**清掉 —— 留着是硬解析错误,
// 清掉则降级成引擎按 short_edge=768 自算,是可接受的。
func TestH3AlwaysDropsResolutionToken(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		task string
	}{
		{"比例缺失", map[string]any{"size": "480P"}, "t2v"},
		{"比例非具名", map[string]any{"size": "480P", "aspect_ratio": "26:15"}, "t2v"},
		{"调用方自带画布", map[string]any{"size": "480P", "width": 832, "height": 480}, "t2v"},
		{"关键帧", map[string]any{"size": "480P"}, "flf2v"},
		{"关键帧-尾帧", map[string]any{"size": "768P"}, "l2va"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			applyMiniMaxH3Request(tc.body, tc.task, 5, false, 0)
			if _, exists := tc.body["size"]; exists {
				t.Fatalf("档位词 size 必须清掉,残留 %v", tc.body["size"])
			}
		})
	}
}

// 像素串是引擎认的合法 SizeStr,不该被误删。
func TestH3KeepsPixelSizeString(t *testing.T) {
	body := map[string]any{"size": "832x480", "aspect_ratio": "16:9"}
	applyMiniMaxH3Request(body, "t2v", 5, false, 0)
	if body["size"] != "832x480" {
		t.Fatalf("像素串 size 应保留,得到 %v", body["size"])
	}
}

// ── task_type 解析 ──────────────────────────────────────────────────────────

// l2va 与 i2v 输入形态完全相同(都是 1 张图),只能由显式 task_type 定夺。
func TestH3KeyframeThreeStates(t *testing.T) {
	setVideoConfig(t, h3Config)
	cases := []struct {
		name string
		req  relaycommon.TaskSubmitReq
		want string
	}{
		{"仅首帧 → i2v", relaycommon.TaskSubmitReq{
			Images: []string{"a"}, Metadata: map[string]any{"task_type": "i2v"}}, "i2v"},
		{"仅尾帧 → l2va", relaycommon.TaskSubmitReq{
			Images: []string{"a"}, Metadata: map[string]any{"task_type": "l2va"}}, "l2va"},
		{"首尾帧 → flf2v", relaycommon.TaskSubmitReq{
			Images: []string{"a", "b"}, Metadata: map[string]any{"task_type": "flf2v"}}, "flf2v"},
		{"无输入 → t2v", relaycommon.TaskSubmitReq{}, "t2v"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := taskTypeOfRequest(&tc.req, "video-h3", "video-h3")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// 回归防线:l2va 进了「关键帧」tab 的候选集之后,现有 wan 关键帧模型收到 1 张图
// 仍必须收敛到 i2v,而不是变成「i2v/l2va 分不开」的 400。
//
// 这正是 taskTypesCompatibleWithInputs 里**故意不加 l2va** 的理由 —— 那条注释若被
// 「补全」掉,本测试会红。
func TestL2VADoesNotBreakExistingKeyframeResolution(t *testing.T) {
	setVideoConfig(t, `{"models":{"wan2.2-i2v":{"tabs":{"flf2v":{}}}}}`)
	req := relaycommon.TaskSubmitReq{Images: []string{"a"}}
	got, err := taskTypeOfRequest(&req, "wan2.2-i2v", "wan2.2-i2v")
	if err != nil {
		t.Fatalf("1 张图应收敛到 i2v,却报错:%v", err)
	}
	if got != "i2v" {
		t.Fatalf("got %q, want i2v", got)
	}
}

// 名字推断只是兜底(模型没配进体验区时才走)。ref2va 这条是真的改变行为:
// 不加分支会落 t2v,数字人直连请求带图必被 textOnlyTaskTypes 判死。
func TestH3NameInferenceFallback(t *testing.T) {
	setVideoConfig(t, "")
	cases := map[string]string{
		"minimax-h3-fl2va": "t2v",
		// 引擎分区名 ref2va → 门面词表 r2va(不是 s2v:那是 InfiniteTalk 的数字人)。
		"minimax-h3-ref2va": "r2va",
		// 裸 h3 不该被匹配:误伤面太大。
		"MiniMax-H3": "t2v",
		// 不能误伤既有模型。
		"wan2.2-flf2v": "flf2v",
		"ltx2-v2a":     "v2a",
		"infinitetalk": "s2v",
	}
	for name, want := range cases {
		if got := inferTaskType(name); got != want {
			t.Fatalf("inferTaskType(%q) = %q, want %q", name, got, want)
		}
	}
}

// 引擎族判据必须是配置声明,不是模型名。
func TestVideoEngineFamilyIsDeclarationNotName(t *testing.T) {
	setVideoConfig(t, h3Config)
	if got := common.VideoEngineFamilyForModel("video-h3"); got != common.VideoEngineMinimaxH3 {
		t.Fatalf("声明了 engine 却没读到:%q", got)
	}
	// 名字里带 h3 但没声明 → 不认。
	if got := common.VideoEngineFamilyForModel("minimax-h3-fl2va"); got != "" {
		t.Fatalf("未声明 engine 的模型不该被当成 H3,得到 %q", got)
	}
	// 多候选名(公开名 + 重定向后的上游名)任一命中即可。
	if got := common.VideoEngineFamilyForModel("public-alias", "video-h3"); got != common.VideoEngineMinimaxH3 {
		t.Fatalf("候选名任一命中即可,得到 %q", got)
	}
}

// 步数必须与引擎族正交:蒸馏版(Turbo8,标定 8 步)要照样声明 engine 才能拿到请求整形
// (时长下发 / 17n+5 栅格 / aspect_ratio 归一 / 时长白名单加固),若步数按引擎族一刀切,
// 它就会被强塞基座的 20 步 —— 速度优势全丢,还会跑到远超标定步数。
func TestVideoInferenceStepsIsPerModel(t *testing.T) {
	setVideoConfig(t, `{"models":{
		"video-h3":{"engine":"minimax-h3"},
		"video-h3-turbo8":{"engine":"minimax-h3","defaultSteps":8},
		"video-h3-zero":{"engine":"minimax-h3","defaultSteps":0}
	}}`)
	if got := common.VideoInferenceStepsForModel("video-h3-turbo8"); got != 8 {
		t.Fatalf("蒸馏版步数 = %d, want 8", got)
	}
	// 没配 → 0,由调用方回落引擎族基座档。
	if got := common.VideoInferenceStepsForModel("video-h3"); got != 0 {
		t.Fatalf("未配 defaultSteps 应返回 0,得到 %d", got)
	}
	// 0/负数当没配:步数不存在「0 = 不限」的语义,别套 maxInputMB 那套。
	if got := common.VideoInferenceStepsForModel("video-h3-zero"); got != 0 {
		t.Fatalf("defaultSteps=0 应当没配处理,得到 %d", got)
	}
	// 候选名(公开名 + 重定向后的上游名)任一命中即可,与引擎族同一组候选。
	if got := common.VideoInferenceStepsForModel("public-alias", "video-h3-turbo8"); got != 8 {
		t.Fatalf("候选名任一命中即可,得到 %d", got)
	}
}

// 模型配了步数就按它下发;没配才回落基座档;调用方显式给的仍然最优先。
func TestH3StepsFollowModelConfig(t *testing.T) {
	body := map[string]any{"size": "480P", "aspect_ratio": "16:9"}
	applyMiniMaxH3Request(body, "t2v", 5, false, 8)
	if body["num_inference_steps"] != 8 {
		t.Fatalf("步数 = %v, want 8(蒸馏版按模型配置走)", body["num_inference_steps"])
	}

	body = map[string]any{"num_inference_steps": 50}
	applyMiniMaxH3Request(body, "t2v", 5, false, 8)
	if body["num_inference_steps"] != 50 {
		t.Fatalf("调用方显式给的步数被模型配置覆盖了:%v", body["num_inference_steps"])
	}
}

// 参考生视频要推画布:Ref2VA 接受具名 aspect_ratio(不传默认 16:9),与关键帧不同。
// 不推的话引擎按 short_edge=768 自算,每条多花一倍时间。
func TestH3AppliesCanvasForR2VA(t *testing.T) {
	body := map[string]any{"size": "480P", "aspect_ratio": "16:9"}
	applyMiniMaxH3Request(body, "r2va", 5, false, 0)
	if body["width"] != 864 || body["height"] != 480 {
		t.Fatalf("r2va 应推出 480P 画布,得到 %vx%v", body["width"], body["height"])
	}
	if _, exists := body["size"]; exists {
		t.Fatal("档位词应被清掉")
	}
}

// t2va 不带任何比例字段时必须补上默认值,否则引擎硬校验直接 400
// (`t2va requires an explicit aspect_ratio`)—— 六种玩法里只有它会因为"没传"整条挂掉。
// 2026-08-13 现网 12 条请求挂了 5 条,全是直连侧没带比例的 t2va。
//
// 补默认还必须发生在推画布之前:否则 h3ApplyCanvas 取不到具名比例,走"清掉档位词交给
// 引擎"的降级路径,用户选的 480P 会变成引擎自推的 768p,GPU 时间翻倍。
func TestH3DefaultsAspectRatioForT2VA(t *testing.T) {
	body := map[string]any{"size": "480P"}
	applyMiniMaxH3Request(body, "t2v", 5, false, 0)
	if body["aspect_ratio"] != "16:9" {
		t.Fatalf("t2va 缺省比例 = %v, want 16:9", body["aspect_ratio"])
	}
	if body["width"] != 864 || body["height"] != 480 {
		t.Fatalf("补了默认比例就该推得出 480P 画布,得到 %vx%v", body["width"], body["height"])
	}
}

// 显式传值一律不动,包括不在具名表内的值:那是"传错"不是"没传",该让引擎把 400 报回去,
// 而不是被我们悄悄改成 16:9 —— 用户会拿到一个自己没要过的画幅还不知道。
func TestH3DoesNotOverrideExplicitAspectRatio(t *testing.T) {
	body := map[string]any{"size": "480P", "aspect_ratio": "9:16"}
	applyMiniMaxH3Request(body, "t2v", 5, false, 0)
	if body["aspect_ratio"] != "9:16" {
		t.Fatalf("显式比例被覆盖:%v", body["aspect_ratio"])
	}

	body = map[string]any{"size": "480P", "ratio": "4:3"}
	applyMiniMaxH3Request(body, "t2v", 5, false, 0)
	if body["aspect_ratio"] != "4:3" {
		t.Fatalf("体验区的 ratio 归一后不该被默认值顶掉:%v", body["aspect_ratio"])
	}

	body = map[string]any{"size": "480P", "aspect_ratio": "26:15"}
	applyMiniMaxH3Request(body, "t2v", 5, false, 0)
	if body["aspect_ratio"] != "26:15" {
		t.Fatalf("非具名比例应原样交给引擎去拒,得到 %v", body["aspect_ratio"])
	}
}

// 关键帧不补:FL2VA 的画幅永远跟随第一张图,引擎静默忽略 aspect_ratio。补了不会报错,
// 但会让排查时误以为画幅是这个比例决定的。
func TestH3DoesNotDefaultAspectRatioForKeyframeTasks(t *testing.T) {
	for _, tt := range []string{"i2v", "flf2v", "l2va"} {
		body := map[string]any{"size": "480P"}
		applyMiniMaxH3Request(body, tt, 5, false, 0)
		if _, exists := body["aspect_ratio"]; exists {
			t.Fatalf("%s 不该被补默认比例:%v", tt, body["aspect_ratio"])
		}
	}
}

// 调用方自己定了像素画布、但没给比例时,**照样要补**。这条钉的是一个容易被"优化"掉的
// 行为:直觉上"有了 width/height 还补比例"是冗余,但引擎 _resolve_shape 先无条件做比例
// 必填校验、再判断画布是否缺省,所以不补就是 400(实测 43ms 返回 t2va requires an
// explicit aspect_ratio),而直连调用方按像素下发画布正是常态。
// 补上的比例对出片无影响:画布显式时引擎只拿它过 [0.25,4] 区间校验(实测 832x480 配
// 矛盾的 9:16 仍出 832x480)。
func TestH3DefaultsAspectRatioEvenWithExplicitCanvas(t *testing.T) {
	body := map[string]any{"width": 832, "height": 480}
	applyMiniMaxH3Request(body, "t2v", 5, false, 0)
	if body["aspect_ratio"] != "16:9" {
		t.Fatalf("像素画布也要补比例,否则引擎必填校验直接 400;得到 %v", body["aspect_ratio"])
	}
	if body["width"] != 832 || body["height"] != 480 {
		t.Fatalf("调用方的画布被改了:%vx%v", body["width"], body["height"])
	}
}
