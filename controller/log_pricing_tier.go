package controller

import (
	"strings"

	"github.com/QuantumNous/new-api/pkg/billingexpr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
	"github.com/shopspring/decimal"
)

var tierPriceVars = []struct{ name, label string }{
	{"p", "输入价格"}, {"c", "输出价格"}, {"cr", "缓存命中价格"},
	{"cc", "缓存创建价格"}, {"cc1h", "缓存创建价格（长 TTL：1h/12h）"},
	{"img", "图片输入价格"}, {"img_o", "图片输出价格"},
	{"ai", "音频输入价格"}, {"ao", "音频输出价格"},
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

func fxDegree(node ast.Node) (int, bool) {
	switch n := node.(type) {
	case *ast.IntegerNode, *ast.FloatNode:
		return 0, true
	case *ast.IdentifierNode:
		if n.Value == "fx" {
			return 1, true
		}
		return 0, true
	case *ast.BinaryNode:
		left, leftOK := fxDegree(n.Left)
		right, rightOK := fxDegree(n.Right)
		if leftOK && rightOK {
			switch n.Operator {
			case "*":
				return left + right, true
			case "/":
				return left - right, true
			case "+", "-":
				return left, left == right
			}
		}
	}
	return 0, false
}

func tierLogCatalog(m pricingLogFields, expression string, rate decimal.Decimal) ([]string, decimal.Decimal) {
	base, _, _ := strings.Cut(expression, "|||")
	version, body := billingexpr.ParseExprVersion(base)
	fx := m.num("expr_fx")
	usesFX := billingexpr.UsedVars(base)["fx"]
	if usesFX {
		rate = fx
	}
	tree, err := parser.Parse(body)
	if err != nil {
		return nil, rate
	}
	finder := tierPriceFinder{name: m.str("matched_tier")}
	ast.Walk(&tree.Node, &finder)
	node := tree.Node
	if finder.matches == 1 {
		node = finder.node
	} else if finder.matches > 1 || finder.name != "" {
		return nil, rate
	}
	sourceUSD := version == billingexpr.ExprVersionV1
	if !rate.IsPositive() && !sourceUSD {
		degree, ok := fxDegree(node)
		if !ok || degree != 1 {
			return nil, rate
		}
		sourceUSD = true
		fx = decimal.NewFromInt(1)
	}

	coefficients, ok := linearTierPrice(node, fx)
	if !ok {
		return []string{"单价不可拆分"}, rate
	}
	var prices []string
	hasCache := m.num("cache_tokens").IsPositive() || m.num("cache_creation_tokens").IsPositive() ||
		m.num("cache_write_tokens").IsPositive() || m.num("cache_creation_tokens_5m").IsPositive() ||
		m.num("cache_creation_tokens_1h").IsPositive()
	for _, variable := range tierPriceVars {
		if strings.HasPrefix(variable.name, "c") && variable.name != "c" && !hasCache {
			continue
		}
		if price := coefficients[variable.name]; price.IsPositive() {
			prices = append(prices, catalogPrice(variable.label, "1M tokens", price, rate, sourceUSD))
		}
	}
	// 表达式里的常数项是**人民币**（求值结果本来就是人民币），而上面每个 token 系数在美元
	// 口径下已经折进了 fx。所以固定费必须按人民币来源渲染，不能跟着 sourceUSD 走，否则会把
	// 一笔 ¥100 的固定费显示成 $100。
	if fixed := coefficients[""]; !fixed.IsZero() {
		prices = append(prices, catalogPrice("固定费", "次", fixed, rate, false))
	}
	return prices, rate
}
