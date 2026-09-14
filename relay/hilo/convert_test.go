package hilo

import (
	"testing"
)

func boolp(b bool) *bool { return &b }

// 玩法靠**哪个字段装图**区分，不是靠有没有图。
//
// 客户端对两种玩法只发一个接口（实测），而我们平台上它们是两个 checkpoint,
// 认错的后果是发给错的模型。
func TestDetectMode(t *testing.T) {
	cases := []struct {
		name string
		req  VideoRequest
		want ImageMode
	}{
		{"首帧", VideoRequest{FirstFrame: "https://a"}, ModeFirstLastFrame},
		{"仅尾帧", VideoRequest{LastFrame: "https://b"}, ModeFirstLastFrame},
		{"首尾帧", VideoRequest{FirstFrame: "https://a", LastFrame: "https://b"}, ModeFirstLastFrame},
		{"参考图", VideoRequest{RefImages: []string{"https://c"}}, ModeReference},
		{"纯文生", VideoRequest{}, ModeTextToVideo},
		// 两种都给时首尾帧优先：它直接决定画幅，语义更强。真实请求里不会
		// 同时出现，这里只是不让它变成一个"看起来随机"的选择。
		{"都给了", VideoRequest{FirstFrame: "https://a", RefImages: []string{"https://c"}}, ModeFirstLastFrame},
		// 空串不算给了图 —— 否则会走进首尾帧分支却没有素材。
		{"空串", VideoRequest{FirstFrame: "   "}, ModeTextToVideo},
	}
	for _, c := range cases {
		if got := c.req.DetectMode(); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

// 首尾帧的顺序必须是**首帧在前**。
//
// 下游按位置认，颠倒了视频会倒着长，而且不报错。
func TestImagesKeepFrameOrder(t *testing.T) {
	r := VideoRequest{FirstFrame: "https://first", LastFrame: "https://last"}
	got := r.Images()
	if len(got) != 2 || got[0] != "https://first" || got[1] != "https://last" {
		t.Fatalf("顺序不对：%v", got)
	}
}

// **task_type 必须显式下发**，而且要按"填了哪个槽"细分。
//
// 适配器的 taskTypeOfRequest 第一优先级读 metadata.task_type；不给就退回
// 形态推断，而 l2va（只给尾帧）和 i2v 的输入形态**完全相同**（都是 1 张图），
// 适配器的注释把 l2va 故意排除在形态推断之外，正是因为推不出来。
//
// 不给的后果：仅尾帧被当成首帧，视频从结尾往后长；给两张帧则落进二义分支 400。
func TestTaskTypeIsExplicitAndSlotAware(t *testing.T) {
	cases := []struct {
		name string
		req  VideoRequest
		want TaskType
	}{
		{"纯文生", VideoRequest{}, TaskT2V},
		{"只给首帧", VideoRequest{FirstFrame: "https://a"}, TaskI2V},
		{"只给尾帧", VideoRequest{LastFrame: "https://b"}, TaskL2VA},
		{"首尾帧", VideoRequest{FirstFrame: "https://a", LastFrame: "https://b"}, TaskFLF2V},
		{"参考图", VideoRequest{RefImages: []string{"https://c"}}, TaskR2VA},
	}
	for _, c := range cases {
		if got := c.req.TaskTypeOf(); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
		c.req.Prompt = "a cat"
		body, err := c.req.ToTaskSubmit("m")
		if err != nil {
			t.Fatal(err)
		}
		meta, ok := body["metadata"].(map[string]any)
		if !ok || meta["task_type"] != string(c.want) {
			t.Errorf("%s: metadata.task_type 不对：%v", c.name, body["metadata"])
		}
	}
}

// ⚠️ **参考图不能进顶层 images[]。**
//
// 帧约束走顶层 images（顺序即语义），参考素材走 metadata.src_ref_images。
// 放错的后果：下游按**张数**推断 role（1 张 = first_frame），单张参考图被
// 误判成首帧约束 —— 而 reference 是目录里的默认玩法，这是默认路径。
//
// relay/minimaxv2/convert.go 里对这条有加粗警告，我照样踩了。
func TestReferenceImagesGoToMetadataNotTopLevel(t *testing.T) {
	r := VideoRequest{Prompt: "a cat", RefImages: []string{"https://c1", "https://c2"}}
	body, err := r.ToTaskSubmit("m")
	if err != nil {
		t.Fatal(err)
	}
	if _, bad := body["images"]; bad {
		t.Error("参考图跑进了顶层 images[] —— 会被当成首帧约束")
	}
	meta := body["metadata"].(map[string]any)
	refs, ok := meta["src_ref_images"].([]string)
	if !ok || len(refs) != 2 {
		t.Errorf("src_ref_images 不对：%v", meta["src_ref_images"])
	}
}

// 反过来：帧约束必须在顶层 images，且顺序是 [首帧, 尾帧]。
func TestFrameImagesStayTopLevel(t *testing.T) {
	r := VideoRequest{Prompt: "a cat", FirstFrame: "https://first", LastFrame: "https://last"}
	body, _ := r.ToTaskSubmit("m")
	imgs, ok := body["images"].([]string)
	if !ok || len(imgs) != 2 || imgs[0] != "https://first" {
		t.Fatalf("帧约束的落点或顺序不对：%v", body["images"])
	}
	meta := body["metadata"].(map[string]any)
	if _, bad := meta["src_ref_images"]; bad {
		t.Error("帧约束跑进了 src_ref_images")
	}
}

// 档位词的字段名是 **size**，不是 resolution。
//
// 统一契约和 H3 读的都是 body["size"]。这个坑在聚合 overrides 上踩过两轮。
func TestResolutionBecomesSizeNotResolution(t *testing.T) {
	r := VideoRequest{Prompt: "a cat", Resolution: "768P"}
	body, err := r.ToTaskSubmit("m")
	if err != nil {
		t.Fatal(err)
	}
	if body["size"] != "768P" {
		t.Errorf("size 不对：%v", body["size"])
	}
	if _, bad := body["resolution"]; bad {
		t.Error("转过来之后不该还有 resolution —— 这条路上没人读它")
	}
}

// **比例不能进 size。**
//
// size 里放 WxH 形状的值会让 H3 的 adaptor 用 AspectRatioFromSize 反推并
// 覆盖掉用户选的比例；档位词匹配不到那个正则才安全。所以比例走 metadata。
func TestRatioGoesToMetadataNotSize(t *testing.T) {
	r := VideoRequest{Prompt: "a cat", Ratio: "16:9", Resolution: "768P"}
	body, _ := r.ToTaskSubmit("m")
	if body["size"] != "768P" {
		t.Errorf("size 被比例污染了：%v", body["size"])
	}
	meta := body["metadata"].(map[string]any)
	if meta["aspect_ratio"] != "16:9" {
		t.Errorf("比例没进 metadata：%v", meta["aspect_ratio"])
	}
}

// adaptive 是"跟随输入素材"，不是一个比例值 —— 不传即自适应。
func TestAdaptiveRatioIsNotForwarded(t *testing.T) {
	r := VideoRequest{Prompt: "a cat", Ratio: "adaptive"}
	body, _ := r.ToTaskSubmit("m")
	meta := body["metadata"].(map[string]any)
	if _, ok := meta["aspect_ratio"]; ok {
		t.Error("adaptive 不该原样传下去，会被当成未知比例")
	}
}

// **有声视频漏传按 true**，和官方目录的 default 一致。
//
// 我们之前完全没有这个参数，于是 H3 的原生音轨一直没生成 ——
// 表现是"视频没有声音"。
func TestGenerateAudioDefaultsToTrue(t *testing.T) {
	body, _ := (&VideoRequest{Prompt: "a cat"}).ToTaskSubmit("m")
	if body["metadata"].(map[string]any)["generate_audio"] != true {
		t.Error("漏传时应当按 true")
	}
	body, _ = (&VideoRequest{Prompt: "a cat", GenerateAudio: boolp(false)}).ToTaskSubmit("m")
	if body["metadata"].(map[string]any)["generate_audio"] != false {
		t.Error("显式 false 要被尊重")
	}
}

// 空 prompt 就地拒绝 —— 平台侧 ValidateBasicTaskRequest 也会拒，
// 但在更远的地方失败，错误信息说不清是哪一步。
func TestEmptyPromptRejected(t *testing.T) {
	if _, err := (&VideoRequest{Prompt: "  "}).ToTaskSubmit("m"); err == nil {
		t.Error("空 prompt 应当被拒")
	}
	if _, err := (&VideoRequest{Prompt: "a cat"}).ToTaskSubmit(""); err == nil {
		t.Error("空模型名应当被拒")
	}
}
