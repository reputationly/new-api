package gpustackplus

import (
	"io"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// 生图画幅整形的回归测试。
//
// 这一组锁的是一个**已经在现网出过的**故障:qwen-image 在体验区选 1:1,出来三张横图。
// 根因是异步链路只搬了同步链路一半的画幅逻辑,而漏掉的那一半失效时完全静默 ——
// 请求 200、图也出、只是画幅是引擎默认的 16:9。

// 比例词是运营对文生图的惯用填法,也正是出故障的那一种。
func TestImageShapeAcceptsRatioTokens(t *testing.T) {
	for _, tc := range []struct{ size, want string }{
		{"1:1", "1:1"},
		{"16:9", "16:9"},
		{"9:16", "9:16"},
		{"4:3", "4:3"},
		{"3:4", "3:4"},
	} {
		body := map[string]any{}
		applyImageShape(body, tc.size)
		if got := body["aspect_ratio"]; got != tc.want {
			t.Fatalf("size=%q: aspect_ratio = %v, want %q —— 不下发就会落到引擎写死的 16:9",
				tc.size, got, tc.want)
		}
		// 比例词推不出精确像素,不该凭空造一个 target_shape 出来
		if _, ok := body["target_shape"]; ok {
			t.Fatalf("size=%q: 比例词不该产出 target_shape(%v)", tc.size, body["target_shape"])
		}
	}
}

// 精确像素:既要约分出 aspect_ratio 兜底,也要给出 target_shape ——
// 引擎优先用后者按 2 元素精确像素出图,只发 aspect_ratio 会退回离散分辨率表。
func TestImageShapeAcceptsPixelSize(t *testing.T) {
	body := map[string]any{}
	applyImageShape(body, "1664x928")
	if got := body["aspect_ratio"]; got != "52:29" {
		t.Fatalf("aspect_ratio = %v, want 52:29", got)
	}
	shape, ok := body["target_shape"].([]int)
	if !ok || len(shape) != 2 {
		t.Fatalf("target_shape = %v, want [928 1664]", body["target_shape"])
	}
	// **[height, width],不是 [width, height]**。写反不报错,只把横竖对调。
	if shape[0] != 928 || shape[1] != 1664 {
		t.Fatalf("target_shape = %v, want [928 1664](高在前)", shape)
	}
}

// 解析不出画幅的取值(空串、档位词、auto)不该留下半个字段:
// 写一个空的 aspect_ratio 比不写更糟 —— 引擎按空值查表的行为未验证。
func TestImageShapeIgnoresUnparsable(t *testing.T) {
	for _, size := range []string{"", "auto", "720P", "乱填"} {
		body := map[string]any{}
		applyImageShape(body, size)
		if len(body) != 0 {
			t.Fatalf("size=%q 不该写任何画幅字段,实际 %v", size, body)
		}
	}
}

// 只有生图两个玩法需要画幅整形。
//
// ⚠️ 视频**绝不能**进这条路:target_shape 是 wan 专属字段,H3 与 LTX-2.5 都在各自的
// apply*Request 里显式 delete 掉它(留着会让人误以为画幅可控),无差别写等于把刚删掉的
// 键又塞回去,而且是在它们各自整形之后 —— 那才是真正难查的。
func TestImageShapeTaskTypeScope(t *testing.T) {
	for _, tt := range []string{"t2i", "i2i"} {
		if !imageShapeTaskTypes[tt] {
			t.Fatalf("%s 是生图玩法,应当做画幅整形", tt)
		}
	}
	for _, tt := range []string{"t2v", "i2v", "flf2v", "l2va", "r2va", "sr", "v2a", "s2v", "tts", "t2m"} {
		if imageShapeTaskTypes[tt] {
			t.Fatalf("%s 不是生图玩法,不该做画幅整形(target_shape 会污染视频链路)", tt)
		}
	}
}

// 与同步链路(relay/channel/gpustackplus/adaptor.go 的 setImageShape)同语义。
//
// 这条守的是「两条链路对同一个档位给出同一个画幅」。它一旦破防的症状是:同一个模型
// 同一个档位,第三方渠道(回落同步)出对画幅、自建渠道(走异步)出 16:9,而两边都是 200。
func TestImageShapeMatchesSyncPathSemantics(t *testing.T) {
	// 同步链路的优先级:IsAspectRatio → AspectRatioFromSize 兜底 → 有像素再补 target_shape。
	// 这里逐档验证异步侧给出完全相同的结论。
	cases := []struct {
		size      string
		wantAR    string
		wantShape []int
		hasShape  bool
	}{
		{"1:1", "1:1", nil, false},
		{"16:9", "16:9", nil, false},
		{"1664x928", "52:29", []int{928, 1664}, true},
		{"1024x1024", "1:1", []int{1024, 1024}, true},
	}
	for _, tc := range cases {
		body := map[string]any{}
		applyImageShape(body, tc.size)
		if got := body["aspect_ratio"]; got != tc.wantAR {
			t.Fatalf("size=%q: aspect_ratio = %v, want %q", tc.size, got, tc.wantAR)
		}
		shape, ok := body["target_shape"].([]int)
		if ok != tc.hasShape {
			t.Fatalf("size=%q: target_shape 存在性 = %v, want %v", tc.size, ok, tc.hasShape)
		}
		if tc.hasShape && (shape[0] != tc.wantShape[0] || shape[1] != tc.wantShape[1]) {
			t.Fatalf("size=%q: target_shape = %v, want %v", tc.size, shape, tc.wantShape)
		}
	}
}

// ── 接线测试 ──────────────────────────────────────────────────────────────
//
// 上面那些只测函数本身:把 adaptor.go 里的调用点删掉,它们照样全绿 —— 那是假绿。
// 下面两条走真正的 BuildRequestBody,断言画幅与 ERNIE 生产档确实出现在**发往门面的
// 提交体**里。现网那两个缺陷正是「函数写对了但没接上/根本没写」,只有这一层测得出来。

func buildT2IBody(t *testing.T, model, size string, meta map[string]any) map[string]any {
	t.Helper()
	c := newTestGinContext()
	md := map[string]any{"task_type": "t2i"}
	for k, v := range meta {
		md[k] = v
	}
	req := relaycommon.TaskSubmitReq{Model: model, Prompt: "a cat", Size: size, Metadata: md}
	c.Set("task_request", req)

	info := &relaycommon.RelayInfo{
		UserId:        1,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_shape"},
		ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: model},
	}
	info.OriginModelName = model

	reader, err := (&TaskAdaptor{}).BuildRequestBody(c, info)
	if err != nil {
		t.Fatalf("BuildRequestBody: %s", err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read body: %s", err)
	}
	var body map[string]any
	if err := common.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body is not valid json: %s", err)
	}
	return body
}

// 现网故障的直接回归:qwen-image 选 1:1,提交体里必须真的带上 aspect_ratio。
func TestBuildRequestBodyCarriesRatioToken(t *testing.T) {
	body := buildT2IBody(t, "qwen-image", "1:1", nil)
	if got := body["aspect_ratio"]; got != "1:1" {
		t.Fatalf("aspect_ratio = %v, want 1:1 —— 不下发就落到引擎写死的 16:9(现网出横图那个 bug)", got)
	}
}

// 精确像素:target_shape 必须在提交体里,且是 [height, width]。
func TestBuildRequestBodyCarriesTargetShape(t *testing.T) {
	body := buildT2IBody(t, "qwen-image", "1664x928", nil)
	shape, ok := body["target_shape"].([]any)
	if !ok || len(shape) != 2 {
		t.Fatalf("target_shape = %v, want [928 1664]", body["target_shape"])
	}
	if shape[0] != float64(928) || shape[1] != float64(1664) {
		t.Fatalf("target_shape = %v, want [928 1664](高在前)", shape)
	}
}

// ERNIE 生产档必须真的出现在提交体里,而不只是函数写对了。
func TestBuildRequestBodyCarriesErnieDefaults(t *testing.T) {
	body := buildT2IBody(t, "ernie-image-turbo", "1:1", nil)
	if body["num_inference_steps"] != float64(8) {
		t.Fatalf("num_inference_steps = %v, want 8(不发则 50 步,慢 6.25 倍)", body["num_inference_steps"])
	}
	if body["guidance_scale"] != float64(1) {
		t.Fatalf("guidance_scale = %v, want 1.0", body["guidance_scale"])
	}
	extra, ok := body["extra_args"].(map[string]any)
	if !ok || extra["apply_pe"] != false {
		t.Fatalf("extra_args = %v, want {apply_pe:false}(不显式发则引擎缺省开启改写)", body["extra_args"])
	}
	// 用户开了智能优化时要跟着变 true
	on := buildT2IBody(t, "ernie-image-turbo", "1:1", map[string]any{"use_prompt_enhancer": true})
	if on["extra_args"].(map[string]any)["apply_pe"] != true {
		t.Fatalf("开启智能优化后 apply_pe 应为 true,实际 %v", on["extra_args"])
	}
}

// 视频链路绝不能被生图的画幅整形污染:target_shape 是 wan 专属字段,
// H3/LTX 都在各自的 apply*Request 里显式删掉它。
func TestBuildRequestBodyDoesNotShapeVideo(t *testing.T) {
	c := newTestGinContext()
	req := relaycommon.TaskSubmitReq{
		Model: "wan-t2v", Prompt: "a cat", Size: "1:1",
		Metadata: map[string]any{"task_type": "t2v"},
	}
	c.Set("task_request", req)
	info := &relaycommon.RelayInfo{
		UserId:        1,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_v"},
		ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: "wan-t2v"},
	}
	info.OriginModelName = "wan-t2v"
	reader, err := (&TaskAdaptor{}).BuildRequestBody(c, info)
	if err != nil {
		t.Fatalf("BuildRequestBody: %s", err)
	}
	raw, _ := io.ReadAll(reader)
	var body map[string]any
	common.Unmarshal(raw, &body)
	if _, bad := body["target_shape"]; bad {
		t.Fatalf("视频请求被写入了 target_shape: %s", string(raw))
	}
}
