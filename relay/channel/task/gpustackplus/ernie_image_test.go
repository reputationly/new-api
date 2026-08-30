package gpustackplus

import "testing"

// ERNIE-Image-Turbo 在**异步**生图链路上的生产采样参数。
//
// 这一组守的是「转换类参数不能靠 metadata 透传」这件事:seed / negative_prompt 那些
// 原样转发的键天然没问题,而这三项是我们锁死的生产档 —— 漏了不报错,只是慢 6.25 倍、
// CFG 被打开、提示词被悄悄改写。

func TestErnieAsyncAppliesProductionDefaults(t *testing.T) {
	body := map[string]any{}
	applyErnieImageTurboDefaults(body, "ernie-image-turbo")

	if got := body["num_inference_steps"]; got != 8 {
		t.Fatalf("num_inference_steps = %v, want 8(不发则引擎缺省 50 步,慢 6.25 倍)", got)
	}
	if got := body["guidance_scale"]; got != 1.0 {
		t.Fatalf("guidance_scale = %v, want 1.0(不发则引擎缺省 4.0,等于给蒸馏模型开 CFG)", got)
	}
	// 键名必须是 extra_args:异步端点按 hasattr 过滤,extra_params 会被静默丢弃
	extra, ok := body["extra_args"].(map[string]any)
	if !ok {
		t.Fatalf("extra_args 缺失或类型不对: %v(发 extra_params 会被异步端点静默丢弃)", body["extra_args"])
	}
	if _, bad := body["extra_params"]; bad {
		t.Fatal("发了 extra_params —— 异步端点不认这个键,会被静默丢弃")
	}
	// 必须**显式** false:引擎 _should_apply_pe 读不到时缺省 True
	if extra["apply_pe"] != false {
		t.Fatalf("apply_pe = %v, want false(不显式发则引擎缺省开启,提示词会被改写)", extra["apply_pe"])
	}
}

// use_prompt_enhancer 经 metadata 透传进来,可能是 bool 也可能是 JSON 数字/字符串。
func TestErnieAsyncReadsPromptEnhancer(t *testing.T) {
	for _, tc := range []struct {
		raw  any
		want bool
	}{
		{true, true}, {false, false},
		{float64(1), true}, {float64(0), false},
		{"true", true}, {"false", false},
		{nil, false}, {"乱填", false},
	} {
		body := map[string]any{}
		if tc.raw != nil {
			body["use_prompt_enhancer"] = tc.raw
		}
		applyErnieImageTurboDefaults(body, "ernie-image-turbo")
		extra := body["extra_args"].(map[string]any)
		if extra["apply_pe"] != tc.want {
			t.Fatalf("use_prompt_enhancer=%#v: apply_pe = %v, want %v", tc.raw, extra["apply_pe"], tc.want)
		}
	}
}

// 只对 ERNIE Turbo 生效:把 8 步 / guidance 1.0 强加给别的生图模型是硬伤害。
func TestErnieAsyncScopedToTurbo(t *testing.T) {
	for _, m := range []string{"qwen-image", "z-image", "hunyuan-image-3", "qwen-image-edit"} {
		body := map[string]any{}
		applyErnieImageTurboDefaults(body, m)
		if len(body) != 0 {
			t.Fatalf("%s 不该被写入 ERNIE 生产档: %v", m, body)
		}
	}
	// 名字匹配与同步侧同口径:substring + lower + trim(渠道重定向后的上游名带前后缀很常见)
	for _, m := range []string{"ERNIE-Image-Turbo", "  ernie-image-turbo  ", "baidu/ernie-image-turbo-v1"} {
		body := map[string]any{}
		applyErnieImageTurboDefaults(body, m)
		if body["num_inference_steps"] != 8 {
			t.Fatalf("%q 应被识别为 ERNIE Turbo", m)
		}
	}
}
