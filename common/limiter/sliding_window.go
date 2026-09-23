package limiter

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/go-redis/redis/v8"
)

//go:embed lua/sliding_window.lua
var slidingWindowScriptSrc string

// slidingWindowScript 不走 New() 的单例：那个单例绑定第一次传入的客户端、只预载一次
// SHA。redis.Script 每次按传入的客户端执行，脚本不在时（Redis 重启、SCRIPT FLUSH）
// 自动退回 EVAL。
var slidingWindowScript = redis.NewScript(slidingWindowScriptSrc)

// AllowSlidingWindow 任意 windowSeconds 秒内最多放行 limit 次，被拒的请求不计数。
// limit <= 0 视为不限。
func AllowSlidingWindow(ctx context.Context, rdb *redis.Client, key string, limit int, windowSeconds int) (bool, error) {
	if limit <= 0 {
		return true, nil
	}
	res, err := slidingWindowScript.Run(ctx, rdb, []string{key}, limit, windowSeconds).Int()
	if err != nil {
		return false, fmt.Errorf("sliding window rate limit failed: %w", err)
	}
	return res == 1, nil
}
