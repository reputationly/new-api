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
	VideoPriceModeToken   = "token"
	VideoPriceModePerCall = "per_call"

	VideoPriceKeyWithVideo    = "with_video"
	VideoPriceKeyWithoutVideo = "without_video"
)

// VideoPriceEntry 单个模型的计费矩阵。
//
//	Token:   [分辨率][with_video|without_video] → $/百万 tokens
//	PerCall: [分辨率][秒数]                      → $/次
type VideoPriceEntry struct {
	Mode    string                        `json:"mode"`
	Token   map[string]map[string]float64 `json:"token,omitempty"`
	PerCall map[string]map[string]float64 `json:"per_call,omitempty"`
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
	default:
		return fmt.Errorf("模型 %s: 计费模式只能是 %s / %s,收到 %q",
			model, VideoPriceModeToken, VideoPriceModePerCall, e.Mode)
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
