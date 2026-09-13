package common

import (
	"encoding/json"
	"testing"
)

// 出厂的 H3 聚合流水线必须把生成段**钉在 768P**。
//
// 没有这条 override 的话，客户传的 size=2K 会原样到达生成段 ——
// 而自建部署的 H3 上限就是 768P，请求原地失败。也就是说这个聚合模型名
// 承诺的 2K 反而是唯一调不通的档位。
//
// # 键名必须是 size
//
// 这里踩过：一开始写的是 `resolution`。applyAggregateExpansion 改写的是
// `Distribute()` 里那份**统一任务契约**的 body（prompt/model/images/size/…），
// H3 也从 `body["size"]` 取档位词（h3ApplyCanvas → h3ShortEdgeFromSizeToken）。
// `resolution` 在这条路上没人读 —— 覆盖静默落空，而且不报错。
//
// `resolution` 是官方形状那条 /v2/video_generation 上的字段名，但那条路的
// MiniMaxV2CreateConvert 跑在 Distribute 之前，自己就把它转成 size 了。
func TestDefaultAggregatePinsGenerateStageWithSizeKey(t *testing.T) {
	var models []AggregateModel
	if err := json.Unmarshal([]byte(DefaultAggregateModelConfig), &models); err != nil {
		t.Fatalf("出厂配置不是合法 JSON: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("出厂配置是空的")
	}
	for _, m := range models {
		if m.Upscale == nil || !m.Upscale.IsEnabled() {
			continue // 没有超分段就不需要钉中间尺寸
		}
		ov := m.Generate.Overrides
		if ov == nil {
			t.Errorf("%s 有超分段却没有 generate.overrides —— 客户传的最终尺寸会直接打到生成段", m.Name)
			continue
		}
		if _, ok := ov["size"]; !ok {
			keys := make([]string, 0, len(ov))
			for k := range ov {
				keys = append(keys, k)
			}
			t.Errorf("%s 的 overrides 里没有 size（有的是 %v）—— 生成段读的是 body[\"size\"]，别的键没人读",
				m.Name, keys)
		}
		if _, wrong := ov["resolution"]; wrong {
			t.Errorf("%s 的 overrides 用了 resolution —— 这条路上没人读它，覆盖会静默落空", m.Name)
		}
	}
}
