-- 滑动窗口限流：任意 window 秒内最多 limit 次。
-- KEYS[1]: 限流 key
-- ARGV[1]: 上限次数
-- ARGV[2]: 窗口长度（秒）
--
-- 与令牌桶（rate_limit.lua）的区别：令牌桶初始是满的、又在持续补充，任意窗口内最多
-- 放行「容量 + 一个窗口的补充量」= 2 × limit。需要硬上限的地方用这个。
-- 判定与记录在同一个脚本里原子完成，并发请求不会同时读到「还剩 1 次」而都被放行。

local key = KEYS[1]
local limit = tonumber(ARGV[1])
local window_us = tonumber(ARGV[2]) * 1000000

-- 用 Redis 服务器时间：多实例各自的时钟偏差不影响判定
local t = redis.call('TIME')
local now = tonumber(t[1]) * 1000000 + tonumber(t[2])

-- 恰好一个窗口前的那次算作已过期，与进程内实现（now - oldest >= window 即放行）一致
redis.call('ZREMRANGEBYSCORE', key, '-inf', now - window_us)
local count = redis.call('ZCARD', key)
if count >= limit then
    return 0
end
-- 同一微秒内的两次请求靠 count 区分成员，避免互相覆盖而少计一次
redis.call('ZADD', key, now, now .. ':' .. count)
redis.call('PEXPIRE', key, math.ceil(window_us / 1000))
return 1
