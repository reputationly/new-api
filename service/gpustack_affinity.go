package service

import (
	"fmt"
	"hash/fnv"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

// GPUStack 实例亲和：把同一段对话的后续轮次钉回同一个模型实例。
//
// 背景：GPUStack 的网关把每个 RUNNING 实例注册成独立 service，按百分比随机分流，
// 而 vLLM 的前缀缓存是实例本地的——同一会话的第二轮落到别的实例就要重算整段前缀。
// 计费产能约等于实算产能 ÷ (1 − 命中率)，所以命中率被摊薄的代价是成倍的。
// 4 副本上受控回放（8 会话 × 5 轮）实测命中率 34.1%，逐轮命中占比
// 0/0/38/62/75%——随机分流的特征曲线。
//
// 键取「对话前缀」而不是某个会话 id：多轮累积时首条消息恒定不变，正好等价于前缀
// 缓存的命中条件，而且不需要客户端配合送任何 header。不同会话共享同一份文档时也会
// 落到同一实例，比按会话 id 更贴合缓存的实际形状。
//
// 做法是**直连实例**（改写目标 URL 为 worker_ip:port），而不是给网关下发路由头。
//
// 为什么不能用 X-GPUStack-Model-Instance 那个头（2026-09-15 实测否掉）：
// 那个头是「网关 → worker」的输出，不是「客户端 → 网关」的输入。
//   - 所有 worker 都是 proxy_mode=worker，于是 gpustack 的
//     model_instance_registry 把每个实例的 service 名解析到同一个
//     worker_ip:10150；网关选完 cluster 后 worker 无法从地址分辨该转给哪个
//     实例，只能靠网关把选择结果写进这个头告诉它。
//   - 网关的 weighted_clusters 选择**不读**任何客户端头（实测：发
//     model-57-1426 的请求落到了别的 worker，worker 查不到就 404）。
//   - 做这个写入的 set-model-pre-route 是预编译 wasm（pip 包
//     gpustack_higress_plugins），改不了。
// 曾短暂放开网关对该头的剥离，结果是 3/6 的请求 404 —— 把一个静默的性能问题
// 换成了显性的可用性故障。所以本实现**完全不发那个头**。
//
// 直连的代价与兜底：绕过网关的计量与 ingress 重试策略，所以任何一步失败都必须
// 退回渠道原本的 Base URL（网关），可用性不低于不做亲和。

const (
	// gpustackAffinityPrefixChars 参与哈希的前缀字符数上限。取 2048 是因为只需要
	// 一个稳定的标识，不需要读完文档——线上首条消息可达 80 万字符，整段哈希既慢
	// 又没有额外收益。
	gpustackAffinityPrefixChars = 2048
	// gpustackAffinityPrefixMessages 参与哈希的消息条数。第一轮是 [user]，第二轮是
	// [user, assistant, user]，取前 2 条即可在各轮之间保持不变。
	gpustackAffinityPrefixMessages = 2
	gpustackInstanceCacheTTL       = 30 * time.Second
	gpustackInstanceFetchTimeout   = 5 * time.Second
	// gpustackInstanceFailureBackoff 拉取失败后的最短重试间隔。
	//
	// 没有它的话：失败不写时间戳 → 每个后续请求都判定「该刷新了」→ 单飞标记一清
	// 就又起一个 goroutine。运营把 key 填错（秒回 403）或 gpustack 宕了的时候，
	// 就退化成「每个用户请求打一次管理 API + 打一行错误日志」，正是 TTL 本来要
	// 避免的事，还会刷爆日志。取 5 秒而不是复用 30 秒的 TTL：足够止住洪水，
	// 又能在抖动恢复后较快跟上。
	gpustackInstanceFailureBackoff = 5 * time.Second
	// gpustackInstancePerPage 每页条数，与 gpustack 的 ListParams 默认值一致。
	gpustackInstancePerPage = 100
	// gpustackInstanceMaxPages 翻页上限，防止分页信息异常时打成死循环。
	// 100 页 × 100 条 = 一万个实例，远超任何现实机队。
	gpustackInstanceMaxPages = 100
	// gpustackInvalidateMinInterval 两次「失败触发的失效」之间的最短间隔，见
	// InvalidateGPUStackInstances。取 2 秒：一个死实例最多多收 2 秒的请求
	// （相对 30 秒的 TTL 已是 15 倍改善），同时把全量翻页扫的频率封在 0.5 次/秒。
	gpustackInvalidateMinInterval = 2 * time.Second
)

// GPUStackInstance 是 /v2/model-instances 返回项里我们用到的字段。
type GPUStackInstance struct {
	ID      int    `json:"id"`
	ModelID int    `json:"model_id"`
	State   string `json:"state"`
	// ModelName 用来筛出本渠道这个模型的实例。GPUStack 上同时跑着图像、音频等
	// 其它模型，若不筛就可能把文本请求钉到一个图像模型的实例上——那不是「优化
	// 没生效」，是把请求打坏。
	ModelName string `json:"model_name"`
	// WorkerIP / Port 是实例推理端口的直连地址，用来改写目标 URL。
	//
	// 取 worker_ip 而不是 worker_advertise_address：后者是给集群外访问用的，
	// 在我们的部署里为空，而 gpustack 自己的 model_instance_registry 也是
	// “advertise_address or worker_ip” 的顺序。两个都空的实例在
	// fetchGPUStackInstances 里就被丢掉了。
	WorkerIP string `json:"worker_ip"`
	Port     int    `json:"port"`
}

// DirectBaseURL 返回该实例推理端口的直连 base URL。
//
// vLLM 侧无鉴权（实测 4/4 个实例在不带凭据时对 /v1/models 与一次真实
// completion 都返回 200），所以直连不需要额外的头；渠道自己的 Authorization
// 照常带上，vLLM 会忽略它。
func (i GPUStackInstance) DirectBaseURL() string {
	return fmt.Sprintf("http://%s:%d", i.WorkerIP, i.Port)
}

// hashKey 是 HRW 用的实例标识。
//
// 用身份（model_id + 实例 id）而不是地址：实例重启换端口、或 worker 换 IP 时，
// 若用地址做盐，它原本承接的那批会话会被重映射到别的实例，缓存白丢。身份在实例
// 生命周期内不变，只有实例真正被替换时才该重映射——那时缓存本来也没了。
func (i GPUStackInstance) hashKey() string {
	return fmt.Sprintf("model-%d-%d", i.ModelID, i.ID)
}

var (
	gpustackInstanceCache = map[string][]GPUStackInstance{}
	// gpustackInstanceFetched 最近一次**成功**的时间；零值表示从未成功过。
	gpustackInstanceFetched = map[string]time.Time{}
	// gpustackInstanceFailedAt 最近一次**失败**的时间，用于退避。
	gpustackInstanceFailedAt = map[string]time.Time{}
	gpustackInstanceLoading  = map[string]bool{}
	gpustackInstanceMu       sync.Mutex

	gpustackLogAt   = map[string]time.Time{}
	gpustackLogAtMu sync.Mutex
)

// GPUStackAffinityLogThrottleWindow 同一条诊断日志的最小间隔。
//
// 取 30 秒与实例列表的 TTL 对齐：这几条日志描述的都是「缓存里这份列表有问题」，
// 一份列表最多值一条日志。
const GPUStackAffinityLogThrottleWindow = 30 * time.Second

// GPUStackAffinityLogAllowed 判断这条诊断日志是否该打。
//
// 必须节流：gpustackRunningInstances 是 stale-while-revalidate，缓存里有什么就
// 立刻返回什么，所以「拉到 0 条」「筛完 0 条」这两种状态在整个 TTL 窗口内每个
// 请求都会命中。不节流就是按流量速率刷日志——正是
// gpustackInstanceFailureBackoff 当初为拉取失败引入的那个问题。
func GPUStackAffinityLogAllowed(key string) bool {
	now := time.Now()
	gpustackLogAtMu.Lock()
	defer gpustackLogAtMu.Unlock()
	if last, ok := gpustackLogAt[key]; ok && now.Sub(last) < GPUStackAffinityLogThrottleWindow {
		return false
	}
	gpustackLogAt[key] = now
	return true
}

// GPUStackAffinityBaseURL 为本次请求算出要直连的实例 base URL。
//
// 返回空串表示不做亲和——开关没开、没有 key、消息为空、或者实例列表拉不到。
// 调用方据此照常走渠道原本的 Base URL（网关），行为退回 GPUStack 自己的分流，
// 不影响可用性。
//
// 除「开关没开」之外的每条失败路径都留日志：这个功能的失败形态全是静默的
// （拉到 0 条实例是 HTTP 200 + total=0，不是报错），2026-09-15 那次定位花了
// 两小时，全靠读 gpustack 源码倒推。日志的措辞要直接把原因写出来。
func GPUStackAffinityBaseURL(
	setting dto.ChannelSettings,
	channelID int,
	baseURL string,
	upstreamModelName string,
	messages []dto.Message,
) string {
	if !setting.GPUStackAffinity {
		return ""
	}
	key := strings.TrimSpace(setting.GPUStackAffinityKey)
	baseURL = strings.TrimSpace(baseURL)
	modelName := strings.TrimSpace(upstreamModelName)
	if key == "" || baseURL == "" || modelName == "" {
		if GPUStackAffinityLogAllowed(fmt.Sprintf("incomplete|%d", channelID)) {
			common.SysLog(fmt.Sprintf(
				"gpustack affinity: 渠道 %d 亲和已开但配置不全（管理key=%v Base URL=%v 上游模型名=%v），本次不做亲和",
				channelID, key != "", baseURL != "", modelName != ""))
		}
		return ""
	}
	prefix := gpustackAffinityPrefix(messages)
	if prefix == "" {
		return ""
	}
	// 缓存按「渠道」而不是「渠道+模型」：管理 API 只能按 model_id 过滤而我们只有
	// 模型名，所以无论如何都要全量拉再客户端筛。若把模型名也放进缓存键，一个服务
	// M 个模型的渠道每 30 秒就会做 M 次完全相同的全量翻页扫。
	all := gpustackRunningInstances(channelID, baseURL, key)
	if len(all) == 0 {
		// 最难查的一种失败：管理 API 返回 HTTP 200，pagination.total = 0。
		// 不是 401 也不是 403，就是空。因为 /v2/model-instances 按
		// owner_principal_id == current_principal_id 过滤，而 API key 认证时
		// 这个值恒等于密钥自己的 owner（X-Organization-Id 被忽略），
		// 模型/实例归组织所有，个人作用域的密钥一条都匹配不上。
		if GPUStackAffinityLogAllowed(fmt.Sprintf("empty|%d", channelID)) {
			common.SysError(fmt.Sprintf(
				"gpustack affinity: 渠道 %d 的 RUNNING 实例列表为空。"+
					"若管理 API 返回的是 HTTP 200 + total=0，说明管理密钥的作用域不对——"+
					"它需要由平台管理员在「无组织上下文」下创建（owner_principal_id 为空），"+
					"或至少属于模型所在的组织。密钥列表页的「作用域」列可以直接看出来。",
				channelID))
		}
		return ""
	}
	instances := gpustackInstancesForModel(all, modelName)
	if len(instances) == 0 {
		// seen 只在真要打日志时才构建：这条分支在整个 TTL 窗口内每个请求都会走到，
		// 无条件遍历全量实例只为拼一行日志是纯浪费。
		if GPUStackAffinityLogAllowed(fmt.Sprintf("nomatch|%d|%s", channelID, modelName)) {
			seen := make([]string, 0, len(all))
			for _, inst := range all {
				seen = append(seen, inst.ModelName)
			}
			common.SysError(fmt.Sprintf(
				"gpustack affinity: 渠道 %d 拉到 %d 个实例，但没有 model_name == %q 的。"+
					"渠道配了模型重定向时上游名与 gpustack 侧的模型名会不一致。实际见到：%v",
				channelID, len(all), modelName, dedupeStrings(seen)))
		}
		return ""
	}
	picked := gpustackPickInstance(instances, prefix)
	target := picked.DirectBaseURL()
	if common.DebugEnabled {
		common.SysLog(fmt.Sprintf(
			"gpustack affinity: 渠道 %d 模型 %s → 实例 %d（%s），候选 %d 个",
			channelID, modelName, picked.ID, target, len(instances)))
	}
	return target
}

// dedupeStrings 去重并保序，仅用于日志里列出实际见到的模型名。
func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// gpustackInstancesForModel 从渠道级列表里筛出该模型的实例。
//
// 这一筛是必须的，且挡的是「把请求打坏」而不是「优化失效」：GPUStack 上同时跑着
// 图像、音频等其它模型，选中它们的实例会让文本请求被路由到一个根本不提供该模型的
// 进程上。
func gpustackInstancesForModel(
	instances []GPUStackInstance, modelName string,
) []GPUStackInstance {
	out := make([]GPUStackInstance, 0, len(instances))
	for _, inst := range instances {
		if strings.EqualFold(strings.TrimSpace(inst.ModelName), modelName) {
			out = append(out, inst)
		}
	}
	return out
}

// gpustackAffinityPrefix 取对话的稳定前缀。角色一起参与，避免「同样的文本但角色
// 不同」被当成同一段对话。
//
// 必须有真正的内容才返回非空：只有角色没有内容时若照样成键，所有空内容请求会哈希
// 到同一个实例——那比不做亲和更糟。
func gpustackAffinityPrefix(messages []dto.Message) string {
	var b strings.Builder
	hasContent := false
	for idx, msg := range messages {
		if idx >= gpustackAffinityPrefixMessages || b.Len() >= gpustackAffinityPrefixChars {
			break
		}
		content := strings.TrimSpace(msg.StringContent())
		if content == "" {
			continue
		}
		// 预算必须在写入角色**之前**算：先写角色再算的话，第二条消息上
		// gpustackAffinityPrefixChars - b.Len() 会变成负数（首条内容
		// 2033~2040 字节时可复现），content[:负数] 直接 panic。
		// 这正好落在本功能的目标流量里（模板化的 2 KB 首条消息）。
		remain := gpustackAffinityPrefixChars - b.Len() - len(msg.Role) - 2
		if remain <= 0 {
			break
		}
		hasContent = true
		b.WriteString(msg.Role)
		b.WriteByte('\x1f')
		if len(content) > remain {
			// 按字节截断即可：这是哈希输入，不需要是合法的 UTF-8，只要稳定。
			content = content[:remain]
		}
		b.WriteString(content)
		b.WriteByte('\x1e')
	}
	if !hasContent {
		return ""
	}
	return b.String()
}

// gpustackPickInstance 用 HRW(最高随机权重)选实例。
//
// 不用取模：实例数一变，取模会把所有会话重新打乱、全部缓存作废；HRW 在实例增减时
// 只重映射 1/N 的会话。
func gpustackPickInstance(instances []GPUStackInstance, prefix string) GPUStackInstance {
	best := instances[0]
	var bestScore uint64
	for idx, inst := range instances {
		h := fnv.New64a()
		_, _ = h.Write([]byte(prefix))
		_, _ = h.Write([]byte{0})
		_, _ = h.Write([]byte(inst.hashKey()))
		score := mix64(h.Sum64())
		if idx == 0 || score > bestScore {
			best, bestScore = inst, score
		}
	}
	return best
}

// mix64 是 splitmix64 的终末混淆。
//
// 必须有：FNV-1a 是乘-异或链，雪崩很弱，而实例标识只在末尾几个字节上有差异
// （model-7-23 / model-7-24），于是相邻实例的分数高度相关，HRW 退化——扩容一个
// 实例时重映射比例实测到 24.7%，而理论值是 1/(N+1)≈11%（见
// TestPickInstanceRemapsOnlyAFractionWhenScalingOut）。之前盐带 ".static" 后缀
// 多了 7 个字节的混淆轮数才勉强达标，那是运气不是设计。
func mix64(x uint64) uint64 {
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return x
}

// gpustackRunningInstances 返回该渠道可见的全部 RUNNING 实例（不分模型），
// **从不阻塞请求路径**。按模型筛在 gpustackInstancesForModel 里做。
//
// 语义是 stale-while-revalidate：命中就直接返回；过期或没有时，后台刷新并**立即**
// 返回手上已有的（可能为空）。理由是亲和只是尽力而为的优化——若同步去打管理 API，
// 缓存冷的那一刻每个并发请求都要各等一次超时（惊群），给用户请求平白加延迟，
// 而它换来的只是一次缓存命中。代价是某渠道的头几个请求没有亲和，可以接受。
//
// 单飞用 gpustackInstanceLoading 标记：同一 key 同时只允许一个后台刷新在跑。
func gpustackRunningInstances(
	channelID int, baseURL string, key string,
) []GPUStackInstance {
	cacheKey := fmt.Sprintf("%d|%s", channelID, strings.TrimRight(baseURL, "/"))

	gpustackInstanceMu.Lock()
	cached := gpustackInstanceCache[cacheKey]
	fetchedAt := gpustackInstanceFetched[cacheKey]
	everFetched := !fetchedAt.IsZero()
	// 需要刷新，且没有别人在刷（单飞），且不在失败退避窗口内。单飞标记必须在持锁
	// 期间置起，否则并发请求会同时判定「没人在刷」而一起发起拉取。
	shouldRefresh := time.Since(fetchedAt) >= gpustackInstanceCacheTTL &&
		!gpustackInstanceLoading[cacheKey] &&
		time.Since(gpustackInstanceFailedAt[cacheKey]) >= gpustackInstanceFailureBackoff
	if shouldRefresh {
		gpustackInstanceLoading[cacheKey] = true
	}
	gpustackInstanceMu.Unlock()

	if shouldRefresh {
		go refreshGPUStackInstances(
			cacheKey, channelID, baseURL, key, everFetched, len(cached))
	}
	// 无论是否在刷新，都立刻返回手上已有的——请求路径不等 I/O。
	return cached
}

// InvalidateGPUStackInstances 在上游失败后把该渠道的实例列表标记为过期。
//
// 为什么需要：TTL 是 30 秒，实例挂掉后这 30 秒里的请求仍会被钉到它上面。下发路由头
// 意味着我们放弃了网关的分流兜底，所以必须自己感知实例已不可用。
//
// 为什么是「标记过期」而不是「删掉」：删掉之后若重新拉取也失败（比如 gpustack 自己
// 在抖），亲和就整体丢了，整个机队的前缀缓存同时失效；标记过期则下一个请求触发后台
// 刷新、同时继续用旧列表，几百毫秒后刷新落地，死实例自然从列表里消失。
//
// 只对本渠道生效（缓存本就是渠道级的，一个渠道一个键）。
//
// 失效必须有下限：实例抖动时（gpustack 仍报它 running，网关却对它 502），每个失败
// 请求都会重新武装一次刷新，管理 API 就会看到「每个拉取时延一次全量翻页扫」而不是
// 每 30 秒一次——偏偏是在机队不健康的时候。而且上游错误里 statusCode=0 那一类还
// 包含纯本地失败（GetRequestURL / SetupRequestHeader / header override 出错），
// 与实例存活毫无关系。
func InvalidateGPUStackInstances(channelID int) {
	prefix := fmt.Sprintf("%d|", channelID)
	now := time.Now()
	// 往前挪一个 TTL 即判定为过期，但保留非零值——否则 everFetched 变成 false，
	// 日志会误报「从未成功拉取过」。
	stale := now.Add(-gpustackInstanceCacheTTL)

	gpustackInstanceMu.Lock()
	defer gpustackInstanceMu.Unlock()
	for k, fetchedAt := range gpustackInstanceFetched {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		// 刚成功拉过就先不动：这一跳的失败大概率发生在上一次刷新之前或之中，
		// 再扫一遍拿到的还是同一份列表。
		if now.Sub(fetchedAt) < gpustackInvalidateMinInterval {
			continue
		}
		gpustackInstanceFetched[k] = stale
	}
}

// GPUStackAffinityShouldInvalidate 判断上游的这个状态码是否说明实例已不可用。
//
// 只认路由/可用性类的失败。4xx 里除 404 之外（400 参数错、401 鉴权、429 限流）都是
// 请求本身的问题，与实例存活无关——拿它们去失效缓存会让正常的用户错误不断打掉亲和。
func GPUStackAffinityShouldInvalidate(statusCode int) bool {
	switch statusCode {
	case http.StatusNotFound,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func refreshGPUStackInstances(
	cacheKey string, channelID int, baseURL string, key string,
	hasCached bool, cachedLen int,
) {
	defer func() {
		gpustackInstanceMu.Lock()
		delete(gpustackInstanceLoading, cacheKey)
		gpustackInstanceMu.Unlock()
	}()

	instances, err := fetchGPUStackInstances(baseURL, key)
	if err != nil {
		// 记下失败时间：否则每个后续请求都会再起一个刷新 goroutine，
		// 变成「每请求一次管理 API + 一行日志」。
		gpustackInstanceMu.Lock()
		gpustackInstanceFailedAt[cacheKey] = time.Now()
		gpustackInstanceMu.Unlock()

		// 两种失败要分清：有旧列表可用时只是降级，没有则完全不做亲和。
		// 这条日志是运营判断「key 填错了没」的唯一信号，措辞不能含糊。
		outcome := "暂不做亲和（从未成功拉取过）"
		if hasCached {
			outcome = fmt.Sprintf("沿用上一次的 %d 个实例", cachedLen)
		}
		common.SysError(fmt.Sprintf(
			"gpustack affinity: 拉取渠道 %d 的实例列表失败，%s: %s",
			channelID, outcome, err.Error()))
		return
	}

	gpustackInstanceMu.Lock()
	gpustackInstanceCache[cacheKey] = instances
	gpustackInstanceFetched[cacheKey] = time.Now()
	delete(gpustackInstanceFailedAt, cacheKey)
	gpustackInstanceMu.Unlock()
}

// fetchGPUStackInstances 拉全该渠道可见的 RUNNING 实例（不按模型筛，筛在取用时做）。
//
// 必须翻页：/v2/model-instances 返回的是 PaginatedList（默认 perPage=100），而模型名
// 只能在客户端筛（服务端只认 model_id，我们只有模型名）。机队大或一个 org 下模型多时，
// 目标模型的实例可能根本不在第一页——那样筛完就是空，亲和会静默失效。
func fetchGPUStackInstances(
	baseURL string, key string,
) ([]GPUStackInstance, error) {
	client := GetHttpClient()
	if client == nil {
		return nil, fmt.Errorf("http client 未初始化")
	}
	// 整体拷贝再只覆盖超时，**不要**只拷 Transport 重新 new 一个 Client：那样会丢掉
	// CheckRedirect，而 checkRedirect 是本包里唯一对每一跳做
	// common.ValidateURLWithFetchSetting 的地方（SSRF 开关、内网 IP/端口/域名过滤）。
	// 丢了它，配置的 Base URL 一旦返回 3xx 就会被无校验地跟随到任意目标。
	// 单独收紧超时：亲和是尽力而为的优化，不能让它拖住用户请求。
	scoped := *client
	scoped.Timeout = gpustackInstanceFetchTimeout

	base := strings.TrimRight(baseURL, "/")
	running := make([]GPUStackInstance, 0, gpustackInstancePerPage)
	for page := 1; page <= gpustackInstanceMaxPages; page++ {
		url := fmt.Sprintf("%s/v2/model-instances?state=running&page=%d&perPage=%d",
			base, page, gpustackInstancePerPage)
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+key)

		resp, err := scoped.Do(req)
		if err != nil {
			return nil, err
		}
		items, totalPage, err := decodeGPUStackPage(resp)
		if err != nil {
			return nil, err
		}

		// 三道筛选（模型名那道在 gpustackInstancesForModel 里做）：
		//   - state：服务端已按 running 过滤，这里再挡一次，防过滤参数失效
		//   - id/model_id 合法：用于日志与 HRW 的稳定键
		//   - worker_ip + port 齐全：直连的地址来源。刚调度上、还没拿到端口的
		//     实例（state=starting 时常见）会缺这两项；若不丢掉，拼出的
		//     "http://:0" 会让本该命中网关兜底的请求直接连接失败。
		for _, inst := range items {
			if !strings.EqualFold(inst.State, "running") || inst.ID <= 0 || inst.ModelID <= 0 {
				continue
			}
			if strings.TrimSpace(inst.WorkerIP) == "" || inst.Port <= 0 {
				continue
			}
			running = append(running, inst)
		}

		if len(items) < gpustackInstancePerPage || page >= totalPage {
			break
		}
	}
	return running, nil
}

// decodeGPUStackPage 读一页并返回总页数。响应体形状见 gpustack 的 PaginatedList。
func decodeGPUStackPage(resp *http.Response) ([]GPUStackInstance, int, error) {
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, 0, fmt.Errorf("HTTP %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var page struct {
		Items      []GPUStackInstance `json:"items"`
		Pagination struct {
			TotalPage int `json:"totalPage"`
		} `json:"pagination"`
	}
	// AGENTS.md Rule 1：JSON 一律走 common 的包装，不直接用 encoding/json。
	if err := common.DecodeJson(resp.Body, &page); err != nil {
		return nil, 0, err
	}
	return page.Items, page.Pagination.TotalPage, nil
}
