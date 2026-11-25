package billing_setting

import (
	"errors"
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
var (
	priceCurrencyMu sync.RWMutex
	priceCurrencies map[string]string
	modelSourceRate float64
)

const (
	CurrencyCNY = "CNY"
	CurrencyUSD = "USD"
)

func UpdateModelSourceRateByJSONString(value string) error {
	rate, err := ParseGlobalModelSourceRate(value)
	if err != nil {
		return err
	}
	priceCurrencyMu.Lock()
	modelSourceRate = rate
	priceCurrencyMu.Unlock()
	return nil
}

func ModelSourceRate2JSONString() string {
	rate, ok := GetModelSourceRate()
	if !ok {
		return ""
	}
	raw, _ := common.Marshal(rate)
	return string(raw)
}

func GetModelSourceRate() (float64, bool) {
	priceCurrencyMu.RLock()
	defer priceCurrencyMu.RUnlock()
	return modelSourceRate, modelSourceRate > 0
}

func QuotaPerUSD() float64 {
	rate, _ := GetModelSourceRate()
	return rate * common.QuotaPerYuan
}

func ParseModelPriceCurrencies(raw string) (map[string]string, error) {
	var currencies map[string]string
	if strings.TrimSpace(raw) != "" {
		if err := common.UnmarshalJsonStr(raw, &currencies); err != nil {
			return nil, fmt.Errorf("ModelPriceCurrency 不是合法的币种表: %w", err)
		}
	}
	for name, currency := range currencies {
		if strings.TrimSpace(name) == "" || strings.Contains(name, "*") || (currency != CurrencyCNY && currency != CurrencyUSD) {
			return nil, fmt.Errorf("模型 %q 的定价币种无效: %q", name, currency)
		}
	}
	return currencies, nil
}

func UpdateModelPriceCurrencies(raw string) error {
	currencies, err := ParseModelPriceCurrencies(raw)
	if err != nil {
		return err
	}
	priceCurrencyMu.Lock()
	priceCurrencies = currencies
	priceCurrencyMu.Unlock()
	return nil
}

func GetModelPriceCurrency(name string) string {
	priceCurrencyMu.RLock()
	defer priceCurrencyMu.RUnlock()
	if priceCurrencies[name] == CurrencyUSD {
		return CurrencyUSD
	}
	return CurrencyCNY
}

var ErrModelSourceRateUnavailable = errors.New("全局源汇率未配置")

func ResolveModelPriceConversion(name string) (currency string, factor float64, err error) {
	priceCurrencyMu.RLock()
	defer priceCurrencyMu.RUnlock()
	if priceCurrencies[name] != CurrencyUSD {
		return CurrencyCNY, 1, nil
	}
	if modelSourceRate <= 0 {
		return CurrencyUSD, 0, fmt.Errorf("模型 %s 使用美元定价: %w", name, ErrModelSourceRateUnavailable)
	}
	return CurrencyUSD, modelSourceRate, nil
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
	rate, _ := GetModelSourceRate()
	return ValidateBillingExprJSONWithRate(jsonStr, rate)
}

func ValidateBillingExprJSONWithRate(jsonStr string, rate float64) error {
	if strings.TrimSpace(jsonStr) == "" {
		return nil
	}
	var exprs map[string]string
	if err := common.UnmarshalJsonStr(jsonStr, &exprs); err != nil {
		// 结构本身有问题交给既有的反序列化路径报错，这里只负责表达式语义。
		return nil
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
		// 表达式引用了 fx（本模型的源货币汇率）却拿不到汇率时，用 1 跑冒烟没有意义，
		// 而且存进去会在每次请求预扣费时失败（fail closed）——保存阶段就拒绝。
		if billingexpr.UsedVars(expr)["fx"] && !(rate > 0) {
			return fmt.Errorf("模型 %s 的计费表达式引用了 fx，但没有设置全局源汇率；请先填汇率再保存", model)
		}
		// 用真实汇率跑冒烟：引用 fx 的表达式在 fx=1 下恒正、到真实汇率可能变负，
		// 而负额度在结算时会变成「给用户加额度」。
		if err := SmokeTestExprWithRate(expr, rate); err != nil {
			return fmt.Errorf("模型 %s 的计费表达式无法编译或算出了非法金额：%w", model, err)
		}
	}
	return nil
}
