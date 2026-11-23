package billing_setting

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/QuantumNous/new-api/setting/config"
	"github.com/samber/lo"
)

const (
	BillingModeRatio      = "ratio"
	BillingModeTieredExpr = "tiered_expr"
	BillingModeField      = "billing_mode"
	BillingExprField      = "billing_expr"
)

// BillingSetting is managed by config.GlobalConfig.Register.
// DB keys: billing_setting.billing_mode, billing_setting.billing_expr
type BillingSetting struct {
	BillingMode map[string]string `json:"billing_mode"`
	BillingExpr map[string]string `json:"billing_expr"`
}

var billingSetting = BillingSetting{
	BillingMode: make(map[string]string),
	BillingExpr: make(map[string]string),
}

func init() {
	config.GlobalConfig.Register("billing_setting", &billingSetting)
}

// ---------------------------------------------------------------------------
// Read accessors (hot path, must be fast)
// ---------------------------------------------------------------------------

func GetBillingMode(model string) string {
	if mode, ok := billingSetting.BillingMode[model]; ok {
		return mode
	}
	return BillingModeRatio
}

// ---------------------------------------------------------------------------
// ModelSourceRate — 每个模型的源货币汇率（上游美元目录价 → 人民币的换算率）
// ---------------------------------------------------------------------------
//
// 为什么单独存：表达式里的系数是人民币，而供应商按美元报价、汇率由它定且会变。
// 把汇率做成按模型的参数后，表达式可以写成 `p * 1.5 * fx`（美元价 × fx），
// 改汇率只需改这一个数，不必重写表达式。
//
// 存在这里而不是 billing_setting 的配置结构里：前端用的顶层 key 是 ModelSourceRate，
// 与 ModelRatio/GroupModelRatio 一样走 model/option.go 的显式 case，避免改 key、
// 也避免给已存数据做迁移。
var (
	modelSourceRateMu sync.RWMutex
	modelSourceRate   = make(map[string]float64)
)

func UpdateModelSourceRateByJSONString(jsonStr string) error {
	parsed := make(map[string]float64)
	if strings.TrimSpace(jsonStr) != "" {
		if err := common.UnmarshalJsonStr(jsonStr, &parsed); err != nil {
			return err
		}
	}
	modelSourceRateMu.Lock()
	modelSourceRate = parsed
	modelSourceRateMu.Unlock()
	return nil
}

func ModelSourceRate2JSONString() string {
	modelSourceRateMu.RLock()
	defer modelSourceRateMu.RUnlock()
	// 用 common.Marshal 保证与其它 option 的序列化口径一致。
	data, err := common.Marshal(modelSourceRate)
	if err != nil {
		return "{}"
	}
	return string(data)
}

// GetModelSourceRateCopy 返回汇率表的副本（批量保存时用来算「最终状态」）。
func GetModelSourceRateCopy() map[string]float64 {
	modelSourceRateMu.RLock()
	defer modelSourceRateMu.RUnlock()
	out := make(map[string]float64, len(modelSourceRate))
	for k, v := range modelSourceRate {
		out[k] = v
	}
	return out
}

// ParseModelSourceRateJSON 解析一张「模型 → 汇率」JSON；空串视为空表。
func ParseModelSourceRateJSON(jsonStr string) (map[string]float64, error) {
	parsed := make(map[string]float64)
	trimmed := strings.TrimSpace(jsonStr)
	// "" 与 "null" 都表示**空表**（显式清空）。绝不能返回 nil：
	// 调用方把 nil 当成"没提交、回退去读旧值"，于是"清空汇率"会被静默当成"不改"，
	// 校验也就放行了（本地复现过：提交 null 后库里汇率被清空，表达式仍引用 fx）。
	if trimmed == "" || trimmed == "null" {
		return parsed, nil
	}
	if err := common.UnmarshalJsonStr(jsonStr, &parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

// GetModelSourceRate 返回某模型的源货币汇率；未配置时 ok=false。
func GetModelSourceRate(model string) (float64, bool) {
	modelSourceRateMu.RLock()
	rate, ok := modelSourceRate[model]
	modelSourceRateMu.RUnlock()
	if !ok || rate <= 0 {
		return 0, false
	}
	return rate, true
}

func GetBillingExpr(model string) (string, bool) {
	expr, ok := billingSetting.BillingExpr[model]
	return expr, ok
}

func GetBillingModeCopy() map[string]string {
	return lo.Assign(billingSetting.BillingMode)
}

func GetBillingExprCopy() map[string]string {
	return lo.Assign(billingSetting.BillingExpr)
}

func GetPricingSyncData(base map[string]any) map[string]any {
	extra := make(map[string]any, 2)
	if modes := GetBillingModeCopy(); len(modes) > 0 {
		extra[BillingModeField] = modes
	}
	if exprs := GetBillingExprCopy(); len(exprs) > 0 {
		extra[BillingExprField] = exprs
	}
	return lo.Assign(base, extra)
}

// ---------------------------------------------------------------------------
// Smoke test (called externally for validation before save)
// ---------------------------------------------------------------------------

func SmokeTestExpr(exprStr string) error {
	return SmokeTestExprWithRate(exprStr, 0)
}

// SmokeTestExprWithRate 与 SmokeTestExpr 相同，但用**真实汇率**跑冒烟向量。
//
// 为什么必须带汇率：引用 fx 的表达式在 fx=1 下可能恒为正（如 `p * (2 - fx)`），
// 到真实汇率 6.8 就变成负数——而负额度在结算时会走「增加用户余额」的分支
// （service/quota.go）。用真实汇率校验能把这种表达式拦在保存阶段。
func SmokeTestExprWithRate(exprStr string, fx float64) error {
	return smokeTestExpr(exprStr, fx)
}

func smokeTestExpr(exprStr string, fx float64) error {
	// 引用 fx 的表达式必须按真实汇率跑：fx=0 会算出 0（等于没验），
	// fx=1 会漏掉「真实汇率下为负」的表达式。没引用 fx 时取值无关紧要，用 1。
	smokeFX := fx
	if smokeFX <= 0 {
		smokeFX = 1
	}
	vectors := []billingexpr.TokenParams{
		{P: 0, C: 0, Len: 0, FX: smokeFX},
		{P: 1000, C: 1000, Len: 1000, FX: smokeFX},
		{P: 100000, C: 100000, Len: 100000, FX: smokeFX},
		{P: 1000000, C: 1000000, Len: 1000000, FX: smokeFX},
	}
	requests := []billingexpr.RequestInput{
		{},
		{
			Headers: map[string]string{
				"anthropic-beta": "fast-mode-2026-02-01",
			},
			Body: []byte(`{"service_tier":"fast","stream_options":{"include_usage":true},"messages":[1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20,21]}`),
		},
	}

	for _, v := range vectors {
		for _, request := range requests {
			result, _, err := billingexpr.RunExprWithRequest(exprStr, v, request)
			if err != nil {
				return fmt.Errorf("vector {p=%g, c=%g}: run failed: %w", v.P, v.C, err)
			}
			if result < 0 {
				return fmt.Errorf("vector {p=%g, c=%g}: result %f < 0", v.P, v.C, result)
			}
			// 与运行期同一套范围校验（这里分组倍率按 1 算）：系数写大了（少 /1e6、
			// 把美元价当人民币价）会在这里被拦下，而不是等到用户请求时溢出。
			quota, convErr := billingexpr.QuotaFromExprOutput(result, billingexpr.ExprVersionV2)
			if convErr != nil {
				return fmt.Errorf("vector {p=%g, c=%g}: %w", v.P, v.C, convErr)
			}
			if err := billingexpr.ValidateFinalQuota(quota); err != nil {
				return fmt.Errorf("vector {p=%g, c=%g}: %w", v.P, v.C, err)
			}
		}
	}
	return nil
}

// ValidateBillingExprJSON 校验一整张「模型 → 表达式」映射，返回第一个失败的模型与原因。
//
// 为什么需要：保存路径此前只校验「表达式 vs 时段价」互斥，**没有任何编译校验**
// （SmokeTestExpr 写好了却没人调用）。于是写坏的表达式能静默存库，直到下一次请求
// 预扣费时才在 relay/helper/price.go 里炸 —— 那时已经影响了线上调用。
//
// 只校验带 v2: 前缀的表达式：存量 v1（美元口径）的配置继续放行，避免一次升级把
// 老配置全拦住；按模型名字排序遍历，保证报错信息稳定（map 迭代顺序是随机的）。
func ValidateBillingExprJSON(jsonStr string) error {
	return ValidateBillingExprJSONWithRates(jsonStr, nil)
}

// ValidateBillingExprJSONWithRates 用**给定的汇率表**校验表达式（rates 为 nil 时用当前内存值）。
//
// 批量保存必须传 nil 以外的值：那一刻库里/内存里的汇率还是旧的，只有把"这次要写的
// 汇率"一起拿来校验，才能保证落库后的状态是自洽的（否则会出现"表达式引用了 fx，
// 但同一次保存把汇率删了"这种组合）。
func ValidateBillingExprJSONWithRates(jsonStr string, rates map[string]float64) error {
	if strings.TrimSpace(jsonStr) == "" {
		return nil
	}
	var exprs map[string]string
	if err := common.UnmarshalJsonStr(jsonStr, &exprs); err != nil {
		// 结构本身有问题交给既有的反序列化路径报错，这里只负责表达式语义。
		return nil
	}
	if rates == nil {
		rates = GetModelSourceRateCopy()
	}
	models := make([]string, 0, len(exprs))
	for model := range exprs {
		models = append(models, model)
	}
	sort.Strings(models)
	for _, model := range models {
		expr := strings.TrimSpace(exprs[model])
		if !strings.HasPrefix(expr, "v2:") {
			continue
		}
		rate := rates[model]
		// 表达式引用了 fx（本模型的源货币汇率）却拿不到汇率时，用 1 跑冒烟没有意义，
		// 而且存进去会在每次请求预扣费时失败（fail closed）——保存阶段就拒绝。
		if billingexpr.UsedVars(expr)["fx"] && !(rate > 0) {
			return fmt.Errorf("模型 %s 的计费表达式引用了 fx，但该模型没有设置「源货币汇率」；请先填汇率再保存", model)
		}
		// 用真实汇率跑冒烟：引用 fx 的表达式在 fx=1 下恒正、到真实汇率可能变负，
		// 而负额度在结算时会变成「给用户加额度」。
		if err := SmokeTestExprWithRate(expr, rate); err != nil {
			return fmt.Errorf("模型 %s 的计费表达式无法编译或算出了非法金额：%w", model, err)
		}
	}
	return nil
}
