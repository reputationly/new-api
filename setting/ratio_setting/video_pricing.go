package ratio_setting

// 视频模型计费矩阵。设计与背景见 docs/video-billing-matrix-design.md。
//
// 供应商对 Seedance 这类模型按 token 计费,单价由 (分辨率, 输入是否含视频) 二维决定;
// kling / vidu 那类不返回 token 的则按 (分辨率, 秒数) 定单次价。两种形态收在同一个
// 配置项里,由 Mode 区分。
//
// 价格单位一律**美元**,与 ModelPrice 同口径——货币换算只发生在管理端编辑器里
// (¥ ÷ 汇率),后端全程不碰汇率。理由见 docs/model-pricing-rmb-input-design.md §3
// 与 docs/currency-fx-architecture.md §二。

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/types"
)

const (
	VideoPriceModeToken     = "token"
	VideoPriceModePerCall   = "per_call"
	VideoPriceModePerSecond = "per_second"

	VideoPriceKeyWithVideo    = "with_video"
	VideoPriceKeyWithoutVideo = "without_video"

	// VideoPriceRowFallback 是分辨率维度的兜底行,语义同 GroupModelRatio 的 "*":
	// 精确行优先,未列出的分辨率落到它。
	//
	// 为什么必须有:分辨率这一维**没有后端校验**(理由见 common/media_model_config.go
	// 文件头——运营填档位词、客户端发精确像素,字符串比较永远对不上,当白名单会把
	// 合法请求拒成 400),所以 API 直连可以传任意 size。而两种形态是两套坐标系:
	// LTX 的 1080p 档真实像素 1920×1088,VideoResolutionTier 按短边归档得到 "4k",
	// 矩阵里没这一行就静默回退固定价——最贵的档反而收得最少。
	VideoPriceRowFallback = "*"
)

// VideoPriceEntry 单个模型的计费矩阵。
//
//	Token:     [分辨率][with_video|without_video] → $/百万 tokens
//	PerCall:   [分辨率][秒数]                      → $/次
//	PerSecond: [分辨率]                            → $/秒
//
// PerSecond 为什么不并进 PerCall 的通配列:PerCall 的列名校验要求正整数秒
// (拼错的列名会静默失配,见 validate),开一个 "*" 口子就得放宽这道校验;而且同一
// 张表里混着「总价」与「单价」两种语义,运营看不出哪格是哪种。三种模式各自语义
// 单一,编辑器也好画。
//
// 适用:时长连续的模型。minimax-h3 是 4~15 秒、LTX-2.5 的 1080p 到 17.7 秒,
// 穷举成 PerCall 要 92 格,按秒只要 6 格。
type VideoPriceEntry struct {
	Mode      string                        `json:"mode"`
	Token     map[string]map[string]float64 `json:"token,omitempty"`
	PerCall   map[string]map[string]float64 `json:"per_call,omitempty"`
	PerSecond map[string]float64            `json:"per_second,omitempty"`
}

var videoPricingMap = types.NewRWMap[string, VideoPriceEntry]()

func VideoPricing2JSONString() string {
	return videoPricingMap.MarshalJSONString()
}

// UpdateVideoPricingByJSONString 先整体校验再写入——半张校验不过的表生效会造成错账,
// 比整个保存失败糟糕得多。
func UpdateVideoPricingByJSONString(jsonStr string) error {
	if strings.TrimSpace(jsonStr) != "" {
		parsed := map[string]VideoPriceEntry{}
		if err := common.UnmarshalJsonStr(jsonStr, &parsed); err != nil {
			return fmt.Errorf("视频计费配置不是合法 JSON: %w", err)
		}
		for name, entry := range parsed {
			if err := entry.validate(name); err != nil {
				return err
			}
		}
	}
	return types.LoadFromJsonString(videoPricingMap, jsonStr)
}

func GetVideoPricing(model string) (VideoPriceEntry, bool) {
	entry, ok := videoPricingMap.Get(model)
	return entry, ok
}

func GetVideoPricingCopy() map[string]VideoPriceEntry {
	return videoPricingMap.ReadAll()
}

// LookupToken 查 $/百万 tokens。仅 token 模式有效。
func (e VideoPriceEntry) LookupToken(resolution string, hasVideo bool) (float64, bool) {
	if e.Mode != VideoPriceModeToken {
		return 0, false
	}
	col := VideoPriceKeyWithoutVideo
	if hasVideo {
		col = VideoPriceKeyWithVideo
	}
	return lookupCell(e.Token, resolution, col)
}

// LookupPerCall 查 $/次。仅 per_call 模式有效。
func (e VideoPriceEntry) LookupPerCall(resolution string, seconds int) (float64, bool) {
	if e.Mode != VideoPriceModePerCall || seconds <= 0 {
		return 0, false
	}
	return lookupCell(e.PerCall, resolution, strconv.Itoa(seconds))
}

// LookupPerSecond 查 $/秒。仅 per_second 模式有效。
//
// 价格 <= 0 一律视为**未配置**而非"免费",与 lookupCell 同口径:调用方无从区分
// 「这一档没填」与「这一档就是 0」,而未配置时的正确行为是回退旧计费路径。
func (e VideoPriceEntry) LookupPerSecond(resolution string) (float64, bool) {
	if e.Mode != VideoPriceModePerSecond || len(e.PerSecond) == 0 {
		return 0, false
	}
	want := normalizeResolutionKey(resolution)
	if want == "" {
		return 0, false
	}
	// 形状必须与 lookupCell 逐行对应:外层按「精确行 → 兜底行」依次试,单行查不到
	// (含价格 <= 0 这种"未配置"形态)只结束本行,不结束整次查找。写成在行内直接
	// return false 的话,配了 {"1080p": 0, "*": 0.02} 时兜底行一次都试不到——而
	// 0 既然等于未配置,它就该和「这一行不存在」表现一致。
	for _, key := range []string{want, VideoPriceRowFallback} {
		if p, ok := lookupPerSecondRow(e.PerSecond, key); ok {
			return p, true
		}
	}
	return 0, false
}

func lookupPerSecondRow(table map[string]float64, wantRow string) (float64, bool) {
	for r, price := range table {
		if normalizeResolutionKey(r) != wantRow {
			continue
		}
		if price <= 0 {
			return 0, false
		}
		return price, true
	}
	return 0, false
}

// lookupCell 行列都按归一化后的键匹配(小写、去空格;秒数取前导整数),
// 容忍运营把 "720P" / "5s" 这类形态填进来。
//
// 价格为 0 一律视为**未配置**而非"免费":否则调用方无法区分"这一格没填"与
// "这一格就是 0",而未配置时的正确行为是回退旧计费路径。
func lookupCell(table map[string]map[string]float64, row, col string) (float64, bool) {
	if len(table) == 0 {
		return 0, false
	}
	wantRow := normalizeResolutionKey(row)
	if wantRow == "" {
		return 0, false
	}
	// 精确行优先,未列出的分辨率落兜底行。兜底只作用在**分辨率**这一维:
	// 秒数维度是离散且可穷举的,给它兜底等于抹掉时长差异,那正是 per_second 该做的事。
	for _, key := range []string{wantRow, VideoPriceRowFallback} {
		if p, ok := lookupRow(table, key, col); ok {
			return p, true
		}
	}
	return 0, false
}

func lookupRow(table map[string]map[string]float64, wantRow, col string) (float64, bool) {
	for r, cols := range table {
		if normalizeResolutionKey(r) != wantRow {
			continue
		}
		wantCol := normalizeSecondsKey(col)
		for c, price := range cols {
			if normalizeSecondsKey(c) != wantCol {
				continue
			}
			if price <= 0 {
				return 0, false
			}
			return price, true
		}
	}
	return 0, false
}

func normalizeResolutionKey(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// normalizeSecondsKey 数字形态取前导整数("5s" / "5秒" → "5"),
// 非数字形态(with_video / without_video)按小写去空格比较。
func normalizeSecondsKey(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	if n := leadingDigits(v); n != "" {
		if i, err := strconv.Atoi(n); err == nil {
			return strconv.Itoa(i)
		}
	}
	return v
}

func leadingDigits(s string) string {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return s[:i]
}

func (e VideoPriceEntry) validate(model string) error {
	switch e.Mode {
	case VideoPriceModeToken:
		return validateTable(model, e.Token, func(col string) error {
			switch normalizeResolutionKey(col) {
			case VideoPriceKeyWithVideo, VideoPriceKeyWithoutVideo:
				return nil
			}
			// 拼错的列名(如 "withvideo")会永远匹配不上、静默按旧路径计费,
			// 这类错误必须在保存时就拦住。
			return fmt.Errorf("模型 %s: token 模式的列名只能是 %s / %s,收到 %q",
				model, VideoPriceKeyWithVideo, VideoPriceKeyWithoutVideo, col)
		})
	case VideoPriceModePerCall:
		return validateTable(model, e.PerCall, func(col string) error {
			n, err := strconv.Atoi(leadingDigits(strings.TrimSpace(col)))
			if err != nil || n <= 0 {
				return fmt.Errorf("模型 %s: per_call 模式的列名必须是正整数秒,收到 %q", model, col)
			}
			return nil
		})
	case VideoPriceModePerSecond:
		// 一维表,复用 validateTable 要先包一层——包出来的假列名不参与校验,
		// 单价的合法性判据(非负、非 NaN/Inf)与二维表逐字一致。
		for row, price := range e.PerSecond {
			if normalizeResolutionKey(row) == "" {
				return fmt.Errorf("模型 %s: 分辨率不能为空", model)
			}
			if math.IsNaN(price) || math.IsInf(price, 0) || price < 0 {
				return fmt.Errorf("模型 %s: %s 的每秒单价非法(%v)", model, row, price)
			}
		}
		return nil
	default:
		return fmt.Errorf("模型 %s: 计费模式只能是 %s / %s / %s,收到 %q",
			model, VideoPriceModeToken, VideoPriceModePerCall, VideoPriceModePerSecond, e.Mode)
	}
}

func validateTable(model string, table map[string]map[string]float64, checkCol func(string) error) error {
	for row, cols := range table {
		if normalizeResolutionKey(row) == "" {
			return fmt.Errorf("模型 %s: 分辨率不能为空", model)
		}
		for col, price := range cols {
			if err := checkCol(col); err != nil {
				return err
			}
			if math.IsNaN(price) || math.IsInf(price, 0) || price < 0 {
				return fmt.Errorf("模型 %s: %s/%s 的价格非法(%v)", model, row, col, price)
			}
		}
	}
	return nil
}
