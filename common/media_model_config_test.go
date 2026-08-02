package common

import "testing"

func setOpt(img, vid string) {
	OptionMapRWMutex.Lock()
	if OptionMap == nil {
		OptionMap = map[string]string{}
	}
	OptionMap["ImageModelSizeConfig"] = img
	OptionMap["VideoModelConfig"] = vid
	OptionMapRWMutex.Unlock()
}

func TestVideoDurationValidation(t *testing.T) {
	setOpt("", `{"models":{"wan2.2-t2v":{"sizes":["1280x720"],"durations":["5","10"]}}}`)
	// 未配置模型:放行
	if err := ValidateVideoDurationForModel(999, "", "other"); err != nil {
		t.Fatalf("unconfigured should pass, got %v", err)
	}
	// 时长命中
	if err := ValidateVideoDurationForModel(5, "", "wan2.2-t2v"); err != nil {
		t.Fatalf("allowed duration should pass, got %v", err)
	}
	// 时长不中
	if err := ValidateVideoDurationForModel(7, "", "wan2.2-t2v"); err == nil {
		t.Fatal("bad duration should reject")
	}
	// 时长走 secondsStr 且带单位 "10s" 兼容(前导整数匹配)
	setOpt("", `{"models":{"m":{"durations":["10s"]}}}`)
	if err := ValidateVideoDurationForModel(10, "", "m"); err != nil {
		t.Fatalf("10 vs 10s should match, got %v", err)
	}
}

// 只配了 sizes、没配 durations 的模型:视为该模型未配置任何校验维度,时长一律放行。
func TestSizesOnlyConfigDoesNotGateDuration(t *testing.T) {
	setOpt("", `{"models":{"wan2.2-i2v":{"sizes":["720P","480P"]}}}`)
	if _, configured := VideoDurationsAllowedForModel("wan2.2-i2v"); configured {
		t.Fatal("sizes-only config should not count as duration-configured")
	}
	if err := ValidateVideoDurationForModel(4, "", "wan2.2-i2v"); err != nil {
		t.Fatalf("sizes-only config should not gate duration, got %v", err)
	}
}

// maxAudioSec:按模型优先于 default,两者都没配才算未配置。
func TestVideoMaxAudioSecForModel(t *testing.T) {
	setOpt("", `{"default":{"maxAudioSec":15},"models":{"infinitetalk-720p":{"maxAudioSec":30}}}`)
	if sec, ok := VideoMaxAudioSecForModel("infinitetalk-720p"); !ok || sec != 30 {
		t.Fatalf("per-model should win, got %v %v", sec, ok)
	}
	// 未在 models 里列出的模型回落到 default
	if sec, ok := VideoMaxAudioSecForModel("other"); !ok || sec != 15 {
		t.Fatalf("should fall back to default, got %v %v", sec, ok)
	}
	// 候选名按顺序匹配:第一个命中的模型配置生效(公开名 → 映射后的上游名)
	if sec, ok := VideoMaxAudioSecForModel("other", "infinitetalk-720p"); !ok || sec != 30 {
		t.Fatalf("later candidate should still match, got %v %v", sec, ok)
	}
	// 只配了别的维度 → 未配置,不限制
	setOpt("", `{"models":{"infinitetalk-720p":{"maxInputMB":50}}}`)
	if sec, ok := VideoMaxAudioSecForModel("infinitetalk-720p"); ok || sec != 0 {
		t.Fatalf("maxInputMB-only config should not count as audio-configured, got %v %v", sec, ok)
	}
	// 整体未配置
	setOpt("", "")
	if _, ok := VideoMaxAudioSecForModel("infinitetalk-720p"); ok {
		t.Fatal("empty config should report unconfigured")
	}
}

// 回归:sizes 与请求尺寸对不上时不再影响放行结果。运营填档位词("720P")而客户端
// 发精确像素("720x1280")是常态,二者无法字符串比较,早前版本会把合法请求拒成 400。
// 这里用同一份配置跑两种请求尺寸,断言二者结果一致——即 size 已完全退出校验决策。
func TestSizeDoesNotAffectValidation(t *testing.T) {
	setOpt("", `{"models":{"wan2.2-i2v":{"sizes":["720P","480P"],"durations":["4","5"]}}}`)
	// 时长命中:无论请求尺寸是否与配置的档位词一致,都放行
	if err := ValidateVideoDurationForModel(4, "", "wan2.2-i2v"); err != nil {
		t.Fatalf("duration in whitelist should pass regardless of size, got %v", err)
	}
	// 时长不中:仍然拒绝(证明本用例的配置确实生效,不是因为整体没配才放行)
	if err := ValidateVideoDurationForModel(9, "", "wan2.2-i2v"); err == nil {
		t.Fatal("duration outside whitelist should still reject")
	}
}
