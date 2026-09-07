package common

import "strings"

// MetadataStringList 从 metadata 取一个字符串列表:支持数组([]any / []string 里的
// 字符串)、逗号分隔的单串、或单个字符串。
//
// **这份实现是平台统一的 metadata 约定,不是某个适配器的私有解析**。原先只存在于
// gpustackplus 适配器里,于是中间件另写了一份"字符串算一项"的计数 —— 同一个请求在
// 生成段被拆成两张参考图、在增强段却被当成一个非法 URL,产出与素材对不上且不报错。
// 放这里是为了让两边共用一份:改约定只有一处要改。
//
// ⚠️ data URL 自身带逗号(data:...;base64,XXXX),**绝不能按逗号拆**。只有纯 URL/路径
// 列表才是逗号分隔;多张 data URL 必须以 JSON 数组传(走下面的 []any / []string 分支)。
func MetadataStringList(md map[string]any, key string) []string {
	if md == nil {
		return nil
	}
	v, ok := md[key]
	if !ok {
		return nil
	}
	var out []string
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			break
		}
		if strings.HasPrefix(s, "data:") {
			out = append(out, s)
		} else {
			for _, part := range strings.Split(s, ",") {
				if p := strings.TrimSpace(part); p != "" {
					out = append(out, p)
				}
			}
		}
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
	case []string:
		for _, s := range t {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// MetadataStringListAny 依次尝试多个键名,返回第一个非空的列表。
//
// 用于**单复数别名**:参考素材的键在平台契约里是 `reference_videos` /
// `reference_video` 两种拼法都收(与 doubao/Ark 对齐,见 materializeR2VAInputs 的
// 字段注释),复数优先——它是多值语义的规范形态。
//
// 和 MetadataStringList 一样放在 common:生成段与增强段必须按同一套键名数素材,
// 否则调用方用单数键提交时,生成段收得到、增强段说"没有参考素材",
// 而增强模型据此写出与素材无关的提示词,全程不报错。
func MetadataStringListAny(md map[string]any, keys ...string) []string {
	for _, k := range keys {
		if v := MetadataStringList(md, k); len(v) > 0 {
			return v
		}
	}
	return nil
}
