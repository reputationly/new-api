package controller

import (
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
	"github.com/shopspring/decimal"
)

type tierLogDetail struct {
	terms         []usagePriceTerm
	lines         []string
	quota         float64
	valid         bool
	hasQuantities bool
}

var tierPriceVars = []struct{ name, label string }{
	{"p", "输入单价"}, {"c", "输出单价"}, {"cr", "缓存命中单价"},
	{"cc", "缓存创建单价"}, {"cc1h", "缓存创建 (1h) 单价"},
	{"img", "图片输入单价"}, {"img_o", "图片输出单价"},
	{"ai", "音频输入单价"}, {"ao", "音频输出单价"},
}

type tierPriceFinder struct {
	name    string
	node    ast.Node
	matches int
}

func (f *tierPriceFinder) Visit(node *ast.Node) {
	call, ok := (*node).(*ast.CallNode)
	if !ok || len(call.Arguments) != 2 {
		return
	}
	fn, named := call.Callee.(*ast.IdentifierNode)
	label, literal := call.Arguments[0].(*ast.StringNode)
	if named && fn.Value == "tier" && literal && label.Value == f.name {
		f.node = call.Arguments[1]
		f.matches++
	}
}

// Only split expressions that are linear in the billed token variables. A
// dynamic/co-dependent price cannot be presented as a fixed catalog unit.
func linearTierPrice(node ast.Node, fx decimal.Decimal) (map[string]decimal.Decimal, bool) {
	switch n := node.(type) {
	case *ast.IntegerNode:
		return map[string]decimal.Decimal{"": decimal.NewFromInt(int64(n.Value))}, true
	case *ast.FloatNode:
		return map[string]decimal.Decimal{"": decimal.NewFromFloat(n.Value)}, true
	case *ast.IdentifierNode:
		if n.Value == "fx" {
			return map[string]decimal.Decimal{"": fx}, fx.IsPositive()
		}
		for _, variable := range tierPriceVars {
			if n.Value == variable.name {
				return map[string]decimal.Decimal{n.Value: decimal.NewFromInt(1)}, true
			}
		}
	case *ast.BinaryNode:
		left, ok := linearTierPrice(n.Left, fx)
		right, rightOK := linearTierPrice(n.Right, fx)
		if !ok || !rightOK {
			break
		}
		switch n.Operator {
		case "+", "-":
			for name, coefficient := range right {
				if n.Operator == "-" {
					coefficient = coefficient.Neg()
				}
				left[name] = left[name].Add(coefficient)
			}
			return left, true
		case "*", "/":
			if n.Operator == "*" && len(left) == 1 {
				if _, scalar := left[""]; scalar {
					left, right = right, left
				}
			}
			scalar, ok := right[""]
			if !ok || len(right) != 1 || (n.Operator == "/" && scalar.IsZero()) {
				break
			}
			for name, coefficient := range left {
				if n.Operator == "/" {
					left[name] = coefficient.DivRound(scalar, 28)
				} else {
					left[name] = coefficient.Mul(scalar)
				}
			}
			return left, true
		}
	}
	return nil, false
}

func tierIsResult(root, selected ast.Node) bool {
	if root == selected {
		return true
	}
	switch node := root.(type) {
	case *ast.CallNode:
		return len(node.Arguments) == 2 && node.Arguments[1] == selected
	case *ast.ConditionalNode:
		return tierIsResult(node.Exp1, selected) || tierIsResult(node.Exp2, selected)
	default:
		return false
	}
}

func tierLogPricing(m pricingLogFields, log *model.Log, expression string, fx, rate decimal.Decimal) tierLogDetail {
	result := tierLogDetail{}
	if settled, ok := m.number("expr_quota_before_group"); ok {
		result.quota, result.valid = settled.InexactFloat64(), true
	}
	version, body := billingexpr.ParseExprVersion(expression)
	if version != billingexpr.ExprVersionV2 {
		return result
	}
	tree, err := parser.Parse(body)
	if err != nil {
		return result
	}
	finder := tierPriceFinder{name: m.str("matched_tier")}
	ast.Walk(&tree.Node, &finder)
	node := tree.Node
	if finder.matches == 1 {
		node = finder.node
	} else if finder.matches > 1 || finder.name != "" {
		return result
	}
	coefficients, ok := linearTierPrice(node, fx)
	if !ok {
		result.lines = append(result.lines, "该表达式无法拆分为固定目录单价。")
		return result
	}
	frozen, hasFrozen := m["expr_tokens"].(map[string]any)
	quantities := pricingLogFields(frozen)
	for _, variable := range tierPriceVars {
		if price, ok := coefficients[variable.name]; ok {
			result.terms = append(result.terms, usagePriceTerm{
				label: variable.label, unit: "1M tokens", price: price, rate: rate,
				quantity: quantities.tokens(variable.name), unitPower: 6,
			})
		}
	}
	// Without settlement snapshots only replay simple text/input-output cases.
	// Token normalization, runtime conditions and request inputs are not recoverable.
	if !hasFrozen && (tree.Node == node || finder.matches == 1) &&
		m.tokens("cache_tokens") == 0 && m.tokens("cache_creation_tokens") == 0 &&
		m.tokens("cache_creation_tokens_5m") == 0 && m.tokens("cache_creation_tokens_1h") == 0 &&
		m.tokens("image_output") == 0 && m.tokens("audio_input_token_count") == 0 &&
		!m.flag("audio") && !m.flag("ws") {
		for i := range result.terms {
			if result.terms[i].label == tierPriceVars[0].label {
				result.terms[i].quantity = log.PromptTokens
			} else if result.terms[i].label == tierPriceVars[1].label {
				result.terms[i].quantity = log.CompletionTokens
			}
		}
		hasFrozen = true
	}
	if hasFrozen {
		cost := coefficients[""]
		for _, term := range result.terms {
			cost = cost.Add(term.price.Mul(decimal.NewFromInt(int64(term.quantity))))
		}
		replayed, err := billingexpr.QuotaFromExprOutput(cost.InexactFloat64(), version)
		if err == nil && !result.valid && tierIsResult(tree.Node, node) {
			result.quota, result.valid = replayed, true
		}
		if err == nil && result.valid && coefficients[""].IsZero() &&
			decimal.NewFromFloat(replayed).Sub(decimal.NewFromFloat(result.quota)).Abs().LessThan(decimal.NewFromFloat(0.000001)) {
			result.hasQuantities = true
		}
	}
	return result
}
