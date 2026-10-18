package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/go-redis/redis/v8"
)

// GPUStack 实例亲和的在飞负载记账。
//
// 存在的原因：亲和键只决定「应该去哪」，不看「那台现在有多忙」。长上下文
// 负载下（单请求 prompt 可达 MB 级、生成 20 分钟量级），合法同键的并发就能
// 把一个实例的 KV cache 顶到 100%，而其余实例闲着——2026-10-18 事故的放大器。
// 溢出判断需要跨副本一致的负载视图，所以首选 Redis 做共享记账。
//
// 数据结构：每实例一个 ZSET，member="<reqID>:<promptBytes>"，score 是兜底过期
// 时间戳。ZREMRANGEBYSCORE 负责清过期成员——副本中途死掉泄漏的条目最多存活
// 一个 TTL 就自动消失，泄漏方向是「多记」而不是「少记」，只会更早溢出，不会
// 假装空闲。正常完成走 ZREM 即时释放。
//
// 全部是 Redis ≤3.0 的命令（ZADD/ZREM/ZREMRANGEBYSCORE/ZRANGE），符合
// AGENTS.md Rule 8 的 DCS 约束。Redis 不可用时退回进程内 map——单副本等价，
// 多副本下只是各看各的负载（溢出更迟钝，不影响正确性）。

const (
	// gpustackLoadRedisNamespace Redis 键前缀。v1 是 member 编码格式的版本号，
	// 改格式时 bump，避免旧 member 解析串味。
	gpustackLoadRedisNamespace = "new-api:gpustack_affinity_load:v1"
	// gpustackLoadEntryTTL 成员兜底寿命。取 1 小时：长于任何正常生成请求的
	// 实际耗时（含长推理），又短到崩溃泄漏不会污染决策超过一小时。
	gpustackLoadEntryTTL = time.Hour
	// gpustackLoadRedisTimeout 每次 Redis 读写的上限。亲和是请求热路径上的
	// 尽力优化，Redis 抖一下不能把用户请求拖死——超时后按「这次没有数据」走。
	gpustackLoadRedisTimeout = time.Second
)

type gpustackLoadBackend interface {
	snapshot(instKey string) (count int64, bytes int64)
	acquire(instKey, member string)
	release(instKey, member string)
}

// gpustackLoadMember 组装 ZSET member：reqID 保证唯一，bytes 嵌进 member 里，
// snapshot 时一次 ZRANGE 同时拿到条数和字节总量，不用再开第二份存储。
func gpustackLoadMember(reqID string, promptBytes int64) string {
	return fmt.Sprintf("%s:%d", reqID, promptBytes)
}

// gpustackLoadMemberBytes 从 member 尾段解析字节数；解析失败按 0 计
// （宁可少算，不让脏 member 把溢出逻辑锁死）。
func gpustackLoadMemberBytes(member string) int64 {
	i := strings.LastIndex(member, ":")
	if i < 0 || i == len(member)-1 {
		return 0
	}
	n, err := strconv.ParseInt(member[i+1:], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// --- Redis 后端 ---

type gpustackRedisLoad struct{}

func (gpustackRedisLoad) key(instKey string) string {
	return gpustackLoadRedisNamespace + ":" + instKey
}

func (b gpustackRedisLoad) snapshot(instKey string) (int64, int64) {
	ctx, cancel := context.WithTimeout(context.Background(), gpustackLoadRedisTimeout)
	defer cancel()
	key := b.key(instKey)
	now := strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := common.RDB.ZRemRangeByScore(ctx, key, "-inf", now).Err(); err != nil {
		return 0, 0
	}
	members, err := common.RDB.ZRange(ctx, key, 0, -1).Result()
	if err != nil {
		return 0, 0
	}
	var count, bytes int64
	for _, m := range members {
		count++
		bytes += gpustackLoadMemberBytes(m)
	}
	return count, bytes
}

func (b gpustackRedisLoad) acquire(instKey, member string) {
	ctx, cancel := context.WithTimeout(context.Background(), gpustackLoadRedisTimeout)
	defer cancel()
	_ = common.RDB.ZAdd(ctx, b.key(instKey), &redis.Z{
		Score:  float64(time.Now().Add(gpustackLoadEntryTTL).UnixNano()),
		Member: member,
	}).Err()
}

func (b gpustackRedisLoad) release(instKey, member string) {
	ctx, cancel := context.WithTimeout(context.Background(), gpustackLoadRedisTimeout)
	defer cancel()
	_ = common.RDB.ZRem(ctx, b.key(instKey), member).Err()
}

// --- 进程内后端 ---

type gpustackMemLoadEntry struct {
	expireAt time.Time
}

type gpustackMemLoad struct {
	mu      sync.Mutex
	entries map[string]map[string]gpustackMemLoadEntry // instKey -> member -> entry
}

var gpustackMem = &gpustackMemLoad{entries: map[string]map[string]gpustackMemLoadEntry{}}

func (b *gpustackMemLoad) snapshot(instKey string) (int64, int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	members := b.entries[instKey]
	now := time.Now()
	var count, bytes int64
	for m, e := range members {
		if now.After(e.expireAt) {
			delete(members, m)
			continue
		}
		count++
		bytes += gpustackLoadMemberBytes(m)
	}
	if len(members) == 0 {
		delete(b.entries, instKey)
	}
	return count, bytes
}

func (b *gpustackMemLoad) acquire(instKey, member string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	members := b.entries[instKey]
	if members == nil {
		members = map[string]gpustackMemLoadEntry{}
		b.entries[instKey] = members
	}
	members[member] = gpustackMemLoadEntry{expireAt: time.Now().Add(gpustackLoadEntryTTL)}
}

func (b *gpustackMemLoad) release(instKey, member string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if members := b.entries[instKey]; members != nil {
		delete(members, member)
		if len(members) == 0 {
			delete(b.entries, instKey)
		}
	}
}

// gpustackLoadBackendNow 每次调用现挑后端：Redis 在就共享（多副本语义），
// 不在就用本进程。backend 之间不互相迁移数据——切换瞬间的少量漂移靠 TTL
// 自愈，不值得为它加复杂度。
func gpustackLoadBackendNow() gpustackLoadBackend {
	if common.RedisEnabled && common.RDB != nil {
		return gpustackRedisLoad{}
	}
	return gpustackMem
}

// gpustackLoadSnapshot 读某实例当前在飞（count, promptBytes）。读失败返回 0：
// 宁可漏溢出（退回纯亲和）也不让记账故障放大成错误路由。
func gpustackLoadSnapshot(instKey string) (int64, int64) {
	return gpustackLoadBackendNow().snapshot(instKey)
}

func gpustackLoadAcquire(instKey, member string) {
	gpustackLoadBackendNow().acquire(instKey, member)
}

func gpustackLoadRelease(instKey, member string) {
	gpustackLoadBackendNow().release(instKey, member)
}
