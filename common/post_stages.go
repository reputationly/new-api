package common

import (
	"fmt"
	"strings"
)

// 后置段（metadata.post_stages）。
//
// **声明，不是命令。** 客户端只说"要什么"（超分、配音、用什么模型、放大到哪一档），
// 不说"由谁提交、什么时候提交、用哪个父任务 id"。父任务 id、渠道、计费全由服务端
// 在推进时决定 —— 这是"关掉页面也能跑完"的前提：一旦声明落在任务上，推进权就归服务端。
//
// 为什么不叫 pipeline：VideoModelConfig 里的 pipeline 已经是**引擎族开关**
// （「自建引擎（可编排超分/配音/插帧）」），含义是"这个模型跑在自建引擎上"。
// 同一个词在"模型能力"与"本次请求的后置段"两个层面指两件事，配混了没人看得出来。
const (
	PostStageKindSR  = "sr"  // 画质超分（生成段产物 → 放大一档）
	PostStageKindV2A = "v2a" // 视频配音（对最终画质做口型/环境声）

	// PostStagesKey 声明在请求 metadata 里的键名。服务端接单时**必须剥掉**再发给引擎。
	PostStagesKey = "post_stages"
)

// PostStage 一个后置段的声明。字段与体验区多年来发给第二段的那份 metadata
// 一一对应：服务端推进时原样搬进子任务，少一层翻译就少一层漂移。
type PostStage struct {
	Kind  string `json:"kind"`
	Model string `json:"model,omitempty"`
	// SRRatio 超分倍率。nil = 交给引擎缺省（SeedVR2 那类"倍率只是上限"的引擎靠它
	// 够不着；固定倍率引擎必须显式给，否则整段超分失败并静默降级）。
	SRRatio         *float64 `json:"sr_ratio,omitempty"`
	ResizeMode      string   `json:"resize_mode,omitempty"`
	TargetShortEdge int      `json:"target_short_edge,omitempty"`
}

// ParsePostStages 解析并校验 metadata.post_stages。
//
// 校验放在**提交阶段**，不是为了好看：这些声明一旦进了任务行，推进是后台跑的，
// 那时再发现"模型名不存在"或"这个分组调不了它"，就只能静默降级 —— 用户付了 2K 的钱
// 拿到 768P 的片子，而且没有任何报错。宁可现在 400。
//
// 这里只做**结构**层面的校验（种类、必填、取值）；"模型是否存在、分组是否可用"
// 需要查库，由调用方（middleware）补。本函数不依赖 model 包，避免 common → model 的环。
func ParsePostStages(raw any) ([]PostStage, error) {
	if raw == nil {
		return nil, nil
	}
	data, err := Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("post_stages 无法解析: %w", err)
	}
	var stages []PostStage
	if err := Unmarshal(data, &stages); err != nil {
		// 文案要能区分"不是数组"与"数组里某一项的字段类型不对"（比如 sr_ratio 写成
		// "abc"），否则运营看到"必须是一个数组"会去改结构，而问题在字段类型上。
		return nil, fmt.Errorf("post_stages 解析失败(需要数组，且各项字段类型正确): %w", err)
	}
	if len(stages) == 0 {
		return nil, nil
	}
	if len(stages) > 2 {
		return nil, fmt.Errorf("post_stages 最多两段（sr、v2a）")
	}
	seen := map[string]bool{}
	for i, st := range stages {
		kind := strings.ToLower(strings.TrimSpace(st.Kind))
		switch kind {
		case PostStageKindSR, PostStageKindV2A:
		default:
			return nil, fmt.Errorf("post_stages[%d].kind 只能是 %q 或 %q，收到 %q",
				i, PostStageKindSR, PostStageKindV2A, st.Kind)
		}
		if seen[kind] {
			return nil, fmt.Errorf("post_stages 里 %q 出现了两次", kind)
		}
		seen[kind] = true
		st.Kind = kind
		st.Model = strings.TrimSpace(st.Model)
		if st.Model == "" {
			return nil, fmt.Errorf("post_stages[%d]（%s）必须给出 model", i, kind)
		}
		if st.SRRatio != nil && *st.SRRatio <= 0 {
			return nil, fmt.Errorf("post_stages[%d].sr_ratio 必须大于 0", i)
		}
		if st.TargetShortEdge < 0 {
			return nil, fmt.Errorf("post_stages[%d].target_short_edge 不能为负", i)
		}
		stages[i] = st
	}
	// 顺序固定：先超分再配音。配音要对着**最终画质**做，反了就是给一张马上要被
	// 放大的画面配音，音频与成片对不上，而且不会报错。
	if len(stages) == 2 && stages[0].Kind != PostStageKindSR {
		return nil, fmt.Errorf("post_stages 的顺序必须是 sr → v2a")
	}
	return stages, nil
}

// PostStageModels 返回声明里出现的模型名，供"分组可用性"那类需要查库的校验使用。
func PostStageModels(stages []PostStage) []string {
	out := make([]string, 0, len(stages))
	for _, st := range stages {
		if st.Model != "" {
			out = append(out, st.Model)
		}
	}
	return out
}
