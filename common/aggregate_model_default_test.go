package common

import (
	"strings"
	"testing"
)

// 出厂的聚合配置必须用**线上真正在跑的**超分模型。
// `swiftvr` 已下线（渠道没了，只是 `/v1/models` 还挂着旧能力），出厂值留着它的话，
// 所有没在后台保存过聚合配置的部署，超分段都会失败。
func TestDefaultAggregateConfigUsesLiveUpscaler(t *testing.T) {
	if strings.Contains(DefaultAggregateModelConfig, "swiftvr") {
		t.Fatalf("出厂聚合配置里还有 swiftvr：%s", DefaultAggregateModelConfig)
	}
	models := parseAggregateModels(DefaultAggregateModelConfig)
	if models == nil {
		t.Fatalf("出厂聚合配置解析失败: %s", DefaultAggregateModelConfig)
	}
	for _, name := range []string{"minimax-h3-2k", "minimax-h3-ref-2k"} {
		agg := models[name]
		if agg == nil {
			t.Fatalf("出厂配置缺少 %s", name)
		}
		if agg.Upscale == nil {
			t.Fatalf("%s 没有超分段", name)
		}
		if agg.Upscale.Model != "seedvr2" {
			t.Errorf("%s 的超分模型是 %q，期望 seedvr2", name, agg.Upscale.Model)
		}
		// SeedVR2 忽略请求尺寸、按部署配置出片，所以交付档位必须显式写出来，
		// 否则计费会按客户要的 2k 记，与实际交付不符。
		if agg.Upscale.DeliveredSize != "1080P" {
			t.Errorf("%s 的 delivered_size 是 %q，期望 1080P", name, agg.Upscale.DeliveredSize)
		}
	}
}
