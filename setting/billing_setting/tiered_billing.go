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
	return smokeTestExpr(exprStr)
}

func smokeTestExpr(exprStr string) error {
	// fx 取 1：引用 fx 的表达式在 fx=0 时会算出 0，那样冒烟测试等于什么都没验。
	vectors := []billingexpr.TokenParams{
		{P: 0, C: 0, Len: 0, FX: 1},
		{P: 1000, C: 1000, Len: 1000, FX: 1},
		{P: 100000, C: 100000, Len: 100000, FX: 1},
		{P: 1000000, C: 1000000, Len: 1000000, FX: 1},
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
		if err := SmokeTestExpr(expr); err != nil {
			return fmt.Errorf("模型 %s 的计费表达式无法编译：%w", model, err)
		}
		// 表达式引用了 fx（本模型的源货币汇率），但该模型没配汇率 —— 存进去会在
		// 每次请求预扣费时失败（fail closed），不如保存时就拒绝，并说清是哪个模型。
		if billingexpr.UsedVars(expr)["fx"] {
			if _, ok := GetModelSourceRate(model); !ok {
				return fmt.Errorf("模型 %s 的计费表达式引用了 fx，但该模型没有设置「源货币汇率」；请先填汇率再保存", model)
			}
		}
	}
	return nil
}
