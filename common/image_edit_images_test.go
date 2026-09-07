package common

import "testing"

func setImageOpt(img string) {
	OptionMapRWMutex.Lock()
	if OptionMap == nil {
		OptionMap = map[string]string{}
	}
	OptionMap["ImageModelSizeConfig"] = img
	OptionMapRWMutex.Unlock()
}

// 未配 = 不设限。改造前直连 /v1/images/edits 一直允许到门面上限,拿体验区 UI 的
// 内置默认 3 去收紧它会打断存量调用方,故这里必须放行任意张数。
func TestImageEditCountUnconfiguredPasses(t *testing.T) {
	setImageOpt(`{"models":{"qwen-image-edit":{"sizes":["1024x1024"]}}}`)

	if _, configured := ImageMaxEditImagesForModel("qwen-image-edit"); configured {
		t.Fatal("只配了 sizes 不该算配了张数上限")
	}
	if err := ValidateImageEditCountForModel(5, "qwen-image-edit"); err != nil {
		t.Fatalf("未配模型应放行,得到 %v", err)
	}
	if err := ValidateImageEditCountForModel(3, "从没配过的模型"); err != nil {
		t.Fatalf("配置里没有的模型应放行,得到 %v", err)
	}
	setImageOpt("")
	if err := ValidateImageEditCountForModel(9, "qwen-image-edit"); err != nil {
		t.Fatalf("整份配置为空时应放行,得到 %v", err)
	}
}

// 配了就按配置拦。这是本次改动的主诉求:接口受体验区管理里的张数限制。
func TestImageEditCountRespectsConfig(t *testing.T) {
	setImageOpt(`{"models":{"sensenova-u1.5":{"tabs":{"image2image":{"maxEditImages":1}}}}}`)

	max, configured := ImageMaxEditImagesForModel("sensenova-u1.5")
	if !configured || max != 1 {
		t.Fatalf("want (1,true), got (%d,%v)", max, configured)
	}
	if err := ValidateImageEditCountForModel(1, "sensenova-u1.5"); err != nil {
		t.Fatalf("刚好到上限应放行,得到 %v", err)
	}
	if err := ValidateImageEditCountForModel(2, "sensenova-u1.5"); err == nil {
		t.Fatal("超过上限应被拒")
	}
}

// 只读 tabs[image2image]:模型级/default 都不回落。与前端 getMaxEditImagesForModel
// 和 recomputeModelLevel 的 MODEL_LEVEL_SKIP 同一决定,三处口径必须一致。
func TestImageEditCountIsTabScopedOnly(t *testing.T) {
	setImageOpt(`{"models":{"m":{"maxEditImages":1}}}`)
	if _, configured := ImageMaxEditImagesForModel("m"); configured {
		t.Fatal("模型级 maxEditImages 不该被读到(parse 侧也保不住它)")
	}

	// 配在别的 tab 下同样不算数:底图张数只在图生图有意义。
	setImageOpt(`{"models":{"m":{"tabs":{"text2image":{"maxEditImages":1}}}}}`)
	if _, configured := ImageMaxEditImagesForModel("m"); configured {
		t.Fatal("text2image 格里的值不该被图生图护栏读到")
	}
}

// 配置只能收窄:填得比门面 i2i 准入闸大无效,否则等于开出一批必被门面拒的槽位。
// 配置只能收窄:填得比门面 i2i 准入闸大无效,否则等于开出一批必被门面拒的槽位。
//
// 用例里的 12 必须**大于**天花板才谈得上钳制。天花板跟着门面调过一次(5→9),那次
// 如果这里恰好写的是新天花板的值,用例会静默退化成「不测钳制」也照样绿 —— 所以先
// 显式断言前提成立,天花板将来再涨时是这条断言先响,而不是悄悄失去检测能力。
func TestImageEditCountClampedToCeiling(t *testing.T) {
	const configured12 = 12
	if ImageEditImagesCeiling >= configured12 {
		t.Fatalf("天花板已涨到 %d,本用例的 %d 不再高于它、测不出钳制,请同步调大",
			ImageEditImagesCeiling, configured12)
	}
	setImageOpt(`{"models":{"m":{"tabs":{"image2image":{"maxEditImages":12}}}}}`)

	max, configured := ImageMaxEditImagesForModel("m")
	if !configured || max != ImageEditImagesCeiling {
		t.Fatalf("want (%d,true), got (%d,%v)", ImageEditImagesCeiling, max, configured)
	}
	if err := ValidateImageEditCountForModel(ImageEditImagesCeiling+1, "m"); err == nil {
		t.Fatalf("超过天花板应被拒,而不是按运营填的 %d 放行", configured12)
	}
}

// 0 在别的 int 字段里是「不限」,这里不成立:底图是图生图的唯一视觉输入,
// 放行 0 张只会让请求带着空输入打到引擎。前端同样把 0 抬回 1。
func TestImageEditCountZeroMeansOne(t *testing.T) {
	setImageOpt(`{"models":{"m":{"tabs":{"image2image":{"maxEditImages":0}}}}}`)

	max, configured := ImageMaxEditImagesForModel("m")
	if !configured || max != 1 {
		t.Fatalf("want (1,true), got (%d,%v)", max, configured)
	}
	if err := ValidateImageEditCountForModel(2, "m"); err == nil {
		t.Fatal("配 0 时 2 张应被拒(上限按 1 算)")
	}
}

// candidates 逐个试:对外模型名没配时能落到渠道重定向后的上游名。
func TestImageEditCountFallsThroughCandidates(t *testing.T) {
	setImageOpt(`{"models":{"upstream-name":{"tabs":{"image2image":{"maxEditImages":2}}}}}`)

	max, configured := ImageMaxEditImagesForModel("对外名", "upstream-name")
	if !configured || max != 2 {
		t.Fatalf("want (2,true), got (%d,%v)", max, configured)
	}
}
