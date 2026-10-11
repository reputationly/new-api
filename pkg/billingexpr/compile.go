package billingexpr

import (
	"fmt"
	"math"
	"strings"
	"sync"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/vm"
)

const maxCacheSize = 256

const (
	// ExprVersionV1：系数是**美元**/百万 tokens（历史口径）。
	ExprVersionV1 = 1
	// ExprVersionV2：系数是**人民币**/百万 tokens（1 quota = ¥0.00000001 之后的口径）。
	ExprVersionV2 = 2
	// DefaultExprVersion 无前缀时按 v1 处理，以兼容库里已有的历史表达式。
	//
	// v1 的结算会被拒绝（见 QuotaFromExprOutput）：它的输出单位与当前余额不同单位，
	// 照原样结算会少收 100 倍，必须由管理员在编辑器里重录为 v2。
	DefaultExprVersion = ExprVersionV1
)

// ParseExprVersion extracts the version tag and body from an expression string.
// Format: "v2:tier(...)" → version=2, body="tier(...)".
// No prefix defaults to DefaultExprVersion (v1).
func ParseExprVersion(exprStr string) (version int, body string) {
	switch {
	case strings.HasPrefix(exprStr, "v2:"):
		return ExprVersionV2, exprStr[3:]
	case strings.HasPrefix(exprStr, "v1:"):
		return ExprVersionV1, exprStr[3:]
	default:
		return DefaultExprVersion, exprStr
	}
}

type cachedEntry struct {
	prog     *vm.Program
	usedVars map[string]bool
	version  int
}

var (
	cacheMu sync.RWMutex
	cache   = make(map[string]*cachedEntry, 64)
)

// compileEnvPrototypeV1 is the v1 type-checking prototype used at compile time.
var compileEnvPrototypeV1 = map[string]interface{}{
	"p":    float64(0),
	"c":    float64(0),
	"len":  float64(0),
	"cr":   float64(0),
	"cc":   float64(0),
	"cc1h": float64(0),
	"img":  float64(0),
	"img_o": float64(0),
	"ai":   float64(0),
	"ao":   float64(0),
	"tier":                   func(string, float64) float64 { return 0 },
	"header":                 func(string) string { return "" },
	"param":                  func(string) interface{} { return nil },
	"has":                    func(interface{}, string) bool { return false },
	"hour":                   func(string) int { return 0 },
	"minute":                 func(string) int { return 0 },
	"weekday":                func(string) int { return 0 },
	"month":                  func(string) int { return 0 },
	"day":                    func(string) int { return 0 },
	"max":                    math.Max,
	"min":                    math.Min,
	"abs":                    math.Abs,
	"ceil":                   math.Ceil,
	"floor":                  math.Floor,
}

func getCompileEnv(version int) map[string]interface{} {
	switch version {
	default:
		return compileEnvPrototypeV1
	}
}

// CompileFromCache compiles an expression string, using a cached program when
// available. The cache is keyed by the SHA-256 hex digest of the expression.
func CompileFromCache(exprStr string) (*vm.Program, error) {
	return compileFromCacheByHash(exprStr, ExprHashString(exprStr))
}

// CompileFromCacheByHash is like CompileFromCache but accepts a pre-computed
// hash, useful when the caller already has the BillingSnapshot.ExprHash.
func CompileFromCacheByHash(exprStr, hash string) (*vm.Program, error) {
	return compileFromCacheByHash(exprStr, hash)
}

func compileFromCacheByHash(exprStr, hash string) (*vm.Program, error) {
	cacheMu.RLock()
	if entry, ok := cache[hash]; ok {
		cacheMu.RUnlock()
		return entry.prog, nil
	}
	cacheMu.RUnlock()

	version, body := ParseExprVersion(exprStr)
	prog, err := expr.Compile(body, expr.Env(getCompileEnv(version)), expr.AsFloat64())
	if err != nil {
		return nil, fmt.Errorf("expr compile error: %w", err)
	}

	vars := extractUsedVars(prog)

	cacheMu.Lock()
	if len(cache) >= maxCacheSize {
		cache = make(map[string]*cachedEntry, 64)
	}
	cache[hash] = &cachedEntry{prog: prog, usedVars: vars, version: version}
	cacheMu.Unlock()

	return prog, nil
}

// ExprVersion returns the version of a cached expression. Returns DefaultExprVersion
// if the expression hasn't been compiled yet or is empty.
func ExprVersion(exprStr string) int {
	if exprStr == "" {
		return DefaultExprVersion
	}
	hash := ExprHashString(exprStr)
	cacheMu.RLock()
	if entry, ok := cache[hash]; ok {
		cacheMu.RUnlock()
		return entry.version
	}
	cacheMu.RUnlock()
	v, _ := ParseExprVersion(exprStr)
	return v
}

func extractUsedVars(prog *vm.Program) map[string]bool {
	vars := make(map[string]bool)
	node := prog.Node()
	ast.Find(node, func(n ast.Node) bool {
		if id, ok := n.(*ast.IdentifierNode); ok {
			vars[id.Value] = true
		}
		return false
	})
	return vars
}

// UsedVars returns the set of identifier names referenced by an expression.
// The result is cached alongside the compiled program. Returns nil for empty input.
func UsedVars(exprStr string) map[string]bool {
	if exprStr == "" {
		return nil
	}
	hash := ExprHashString(exprStr)
	cacheMu.RLock()
	if entry, ok := cache[hash]; ok {
		cacheMu.RUnlock()
		return entry.usedVars
	}
	cacheMu.RUnlock()

	// Compile (and cache) to populate usedVars
	if _, err := compileFromCacheByHash(exprStr, hash); err != nil {
		return nil
	}
	cacheMu.RLock()
	entry, ok := cache[hash]
	cacheMu.RUnlock()
	if ok {
		return entry.usedVars
	}
	return nil
}

// InvalidateCache clears the compiled-expression cache.
// Called when billing rules are updated.
func InvalidateCache() {
	cacheMu.Lock()
	cache = make(map[string]*cachedEntry, 64)
	cacheMu.Unlock()
}
