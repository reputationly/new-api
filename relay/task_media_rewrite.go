package relay

// 任务请求里媒体字段的统一改写骨架(见 docs/inbound-media-offload-design.md)。
//
// 两类改写共用这一套遍历:
//   - task:<id> 产物引用 → data-url / OBS 签名 URL(task_ref_expand.go)
//   - 客户端上传的 data-url → OBS 签名 URL(task_media_offload.go)
//
// 合并成一次遍历而非两道串联:两者字段集与递归器完全相同,串联会让 task: 引用走一趟
// 「下载 → base64 → 立刻解码 → 上传」的无谓往返,且第二道拿到 data-url 时来源信息已丢失,
// obs:// 直签捷径无从谈起。
//
// 三相执行(收集 → 并发解析 → 回填)而非内联 resolve:一次请求可能带 3 张参考图 + 1 个视频,
// 串行上传会把延迟直接叠加到用户感知上——而这段跑在预扣费之前。
//
// 重试语义:RestoreOriginalTaskBody 在每次尝试开头把 body 复位成客户端原始字节。
// 这一步是必需的而非保险——common.ReplaceRequestBody 是持久替换(关旧 storage、清
// KeyRequestBody、写新 storage),没有复位的话,第三方渠道那轮改写过的 body 会污染后续
// 所有重试:重试若落到 gemini/vertex,它们拿到 http URL 而 ParseImageInput 对 http 返回
// nil,参考图被静默丢弃、图生视频降级成文生视频,且没有任何报错。

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"golang.org/x/sync/errgroup"
)

const (
	// ctxKeyMediaRewriteMemo 跨重试的解析结果缓存,生命周期覆盖 RelayTask 整个重试循环。
	ctxKeyMediaRewriteMemo = "media_rewrite_memo"
	// ctxKeyOriginalTaskBody 客户端原始 JSON body 快照,只在首次改写前存一次。
	ctxKeyOriginalTaskBody = "media_rewrite_original_body"

	// mediaResolveConcurrency 单次请求内并发解析的上限。体验区典型批量是 1~4 项
	// (3 张参考图 + 1 个视频是最坏情况),4 能让常见场景一波跑完;再高会让单个请求
	// 独占 S3 客户端连接池。
	mediaResolveConcurrency = 4
	// mediaResolveBudget 整批解析的时间预算。这段跑在 service.PreConsumeBilling 之前,
	// 是纯粹叠加在用户感知延迟上的;而 obsStore.PutObject 走 SDK 默认超时(obs_store.go
	// 那个 5 分钟 http.Client 只用于 downloadToTemp)。没有这道闸,OBS 一挂每次提交都挂死。
	mediaResolveBudget = 20 * time.Second
)

// mediaResolver 认领并改写字符串叶子。
//
// Resolve 返回的 error 一律按硬错误处理(400 skip-retry)。软失败(如落盘失败应回退原值
// 透传)必须由 resolver 自己在内部吞掉并返回原值 + nil ——把"这个失败要不要中断请求"
// 的判断留在懂它的那一层,骨架不猜。
type mediaResolver interface {
	// Name 用于 memo 分区。同一个值在不同 resolver 下可能有不同结果
	// (如 task: 在白名单渠道下直签成 URL、在其他第三方渠道下展开成 data-url),
	// 不分区会让重试换渠道时错误复用上一轮的结果。
	Name() string
	Want(value string) bool
	Resolve(ctx context.Context, value string) (string, error)
}

// mediaResolverReporter 可选:改写结束时汇总本轮结果(日志、响应头)。
// 做成可选接口而非主接口的方法,是为了让"只做改写"的 resolver 不必写空实现。
type mediaResolverReporter interface {
	Report(c *gin.Context)
}

// mediaRewriteMemo 按 (resolver, 内容摘要) 索引的解析结果缓存。
// 用摘要而非原字符串做 key:原值可能是 1 MB 的 base64,存多份副本没有必要。
type mediaRewriteMemo struct {
	mu  sync.Mutex
	val map[string]string
}

func (m *mediaRewriteMemo) get(key string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.val[key]
	return v, ok
}

func (m *mediaRewriteMemo) set(key, value string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.val[key] = value
}

func getRewriteMemo(c *gin.Context) *mediaRewriteMemo {
	if v, ok := c.Get(ctxKeyMediaRewriteMemo); ok {
		if memo, ok := v.(*mediaRewriteMemo); ok {
			return memo
		}
	}
	memo := &mediaRewriteMemo{val: map[string]string{}}
	c.Set(ctxKeyMediaRewriteMemo, memo)
	return memo
}

func memoKey(resolverName, value string) string {
	sum := sha256.Sum256([]byte(value))
	return resolverName + ":" + hex.EncodeToString(sum[:])
}

// RestoreOriginalTaskBody 把缓存 body 复位成客户端原始字节。
// 必须在 adaptor.ValidateRequestAndSetAction 之前调用——task_request 是从 body 重建的。
// 无快照(本次请求还没发生过改写)时是 no-op。
func RestoreOriginalTaskBody(c *gin.Context) {
	v, ok := c.Get(ctxKeyOriginalTaskBody)
	if !ok {
		return
	}
	raw, ok := v.([]byte)
	if !ok || len(raw) == 0 {
		return
	}
	if err := common.ReplaceRequestBody(c, raw); err != nil {
		common.SysError("media rewrite: 复位原始请求体失败: " + err.Error())
	}
}

// snapshotOriginalBody 首次改写前存一次客户端原始 body。已存过则不覆盖——
// 覆盖会把"原始"变成"上一轮改写后的",复位就失去意义。
func snapshotOriginalBody(c *gin.Context, raw []byte) {
	if _, exists := c.Get(ctxKeyOriginalTaskBody); exists {
		return
	}
	dup := make([]byte, len(raw))
	copy(dup, raw)
	c.Set(ctxKeyOriginalTaskBody, dup)
}

// rewriteTaskMedia 按当前渠道选出生效的 resolver,改写 task_request 与缓存 body。
// 是 relay_task.go 的唯一入口。
func rewriteTaskMedia(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	resolvers := activeMediaResolvers(c, info)
	if len(resolvers) == 0 {
		return nil
	}
	defer func() {
		for _, r := range resolvers {
			if reporter, ok := r.(mediaResolverReporter); ok {
				reporter.Report(c)
			}
		}
	}()
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil
	}
	memo := getRewriteMemo(c)

	// 相 1:收集待解析的值。只从 task_request 收集即可——body 白名单键
	// {image, images, input_reference, metadata} 的值全部会出现在 TaskSubmitReq 里
	// (ValidateBasicTaskRequest 只做归一化补充,不丢弃)。
	type pendingItem struct {
		resolver mediaResolver
		value    string
	}
	pending := map[string]pendingItem{}
	collect := func(value string) (string, error) {
		r := pickResolver(resolvers, value)
		if r == nil {
			return value, nil
		}
		key := memoKey(r.Name(), value)
		if _, cached := memo.get(key); cached {
			return value, nil
		}
		pending[key] = pendingItem{resolver: r, value: value}
		return value, nil
	}
	// collect 不产生错误,忽略返回值。
	_, _ = walkTaskRequestMedia(&req, collect)

	// 相 2:并发解析。任一硬错误即整体失败(400 skip-retry)。
	if len(pending) > 0 {
		ctx, cancel := context.WithTimeout(c.Request.Context(), mediaResolveBudget)
		defer cancel()
		g, gCtx := errgroup.WithContext(ctx)
		g.SetLimit(mediaResolveConcurrency)
		for key, item := range pending {
			key, item := key, item
			g.Go(func() error {
				next, rErr := item.resolver.Resolve(gCtx, item.value)
				if rErr != nil {
					return rErr
				}
				memo.set(key, next)
				return nil
			})
		}
		if gErr := g.Wait(); gErr != nil {
			return taskRefExpandError(gErr)
		}
	}

	// 相 3:回填 task_request。纯查表,无 IO。
	changed := false
	apply := func(value string) (string, error) {
		r := pickResolver(resolvers, value)
		if r == nil {
			return value, nil
		}
		next, ok := memo.get(memoKey(r.Name(), value))
		if !ok || next == value {
			return value, nil
		}
		changed = true
		return next, nil
	}
	if _, aErr := walkTaskRequestMedia(&req, apply); aErr != nil {
		return taskRefExpandError(aErr)
	}
	c.Set("task_request", req)

	// 相 4:回填缓存 body(供从 common.GetBodyStorage 重建请求的适配器)。
	// 仅当相 3 确实改动过才解析 body——无媒体的纯文生视频请求因此零 JSON 往返。
	if !changed {
		return nil
	}
	return rewriteMediaInBody(c, apply)
}

// pickResolver 返回第一个认领该值的 resolver。一个值只归一个 resolver:
// task: 与 data: 前缀天然互斥,不存在竞争。
func pickResolver(resolvers []mediaResolver, value string) mediaResolver {
	if value == "" {
		return nil
	}
	for _, r := range resolvers {
		if r.Want(value) {
			return r
		}
	}
	return nil
}

// walkTaskRequestMedia 遍历 TaskSubmitReq 的全部媒体字段。
// 顶层 Image/InputReference/Images 之外,metadata 可含任意深度的嵌套结构
// (如 doubao 的 content[].video_url.url),递归到每个 string 叶子。
func walkTaskRequestMedia(req *relaycommon.TaskSubmitReq, resolve func(string) (string, error)) (bool, error) {
	for _, field := range []*string{&req.Image, &req.InputReference} {
		next, err := resolve(*field)
		if err != nil {
			return false, err
		}
		*field = next
	}
	for i, image := range req.Images {
		next, err := resolve(image)
		if err != nil {
			return false, err
		}
		req.Images[i] = next
	}
	if _, err := rewriteMediaInValue(req.Metadata, resolve); err != nil {
		return false, err
	}
	return true, nil
}

// rewriteMediaInBody 读取缓存的原始 JSON body,改写其中的媒体字段后写回 body 存储。
func rewriteMediaInBody(c *gin.Context, resolve func(string) (string, error)) *dto.TaskError {
	if !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
		return nil
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil // body 不可读(如已被消费),task_request 改写已兜底
	}
	raw, err := storage.Bytes()
	if err != nil || len(raw) == 0 {
		return nil
	}
	var bodyMap map[string]any
	// UseNumber:数字保留为 json.Number 原始文本,避免大整数(seed/provider id)经
	// float64 round-trip 丢精度;json.Number 是命名类型,不匹配 rewriteMediaInValue
	// 的 case string,只有真正的 string 叶子被改写。
	if err := common.UnmarshalWithNumber(raw, &bodyMap); err != nil {
		return nil // 非对象 JSON,task_request 路径已覆盖标量场景
	}
	// 只改写已知媒体/输入字段(与 task_request 路径 Image/Images/InputReference/Metadata
	// 一致),不扫 prompt/model 等自由文本——否则正常 prompt 以 "task:" 开头会被误判为
	// 引用而查库失败(400)。metadata 内可含嵌套媒体(doubao content[].video_url.url),深入递归。
	changed := false
	wrap := func(value string) (string, error) {
		next, err := resolve(value)
		if err == nil && next != value {
			changed = true
		}
		return next, err
	}
	for _, key := range []string{"image", "images", "input_reference", "metadata"} {
		v, ok := bodyMap[key]
		if !ok {
			continue
		}
		next, rErr := rewriteMediaInValue(v, wrap)
		if rErr != nil {
			return taskRefExpandError(rErr)
		}
		bodyMap[key] = next
	}
	if !changed {
		return nil // 白名单字段无可改写值(如 task: 只出现在 prompt 文本里)
	}
	newBody, err := common.Marshal(bodyMap)
	if err != nil {
		return taskRefExpandError(err)
	}
	// 改写前先留档:重试时 RestoreOriginalTaskBody 靠它把 body 复位。
	snapshotOriginalBody(c, raw)
	if err := common.ReplaceRequestBody(c, newBody); err != nil {
		return taskRefExpandError(err)
	}
	return nil
}

// rewriteMediaInValue 递归改写任意 JSON 值(string/map/slice)中的 string 叶子,原地
// 改写 map value / slice 元素。JSON 反序列化产生的是 map[string]any 与 []any,同时兼容
// 手工构造的 []string / map[string]string。返回值仅用于把 string 叶子替换回上层容器。
func rewriteMediaInValue(value any, resolve func(string) (string, error)) (any, error) {
	switch v := value.(type) {
	case string:
		return resolve(v)
	case map[string]any:
		for key, item := range v {
			next, err := rewriteMediaInValue(item, resolve)
			if err != nil {
				return nil, err
			}
			v[key] = next
		}
		return v, nil
	case []any:
		for i, item := range v {
			next, err := rewriteMediaInValue(item, resolve)
			if err != nil {
				return nil, err
			}
			v[i] = next
		}
		return v, nil
	case map[string]string:
		for key, item := range v {
			next, err := resolve(item)
			if err != nil {
				return nil, err
			}
			v[key] = next
		}
		return v, nil
	case []string:
		for i, item := range v {
			next, err := resolve(item)
			if err != nil {
				return nil, err
			}
			v[i] = next
		}
		return v, nil
	default:
		return value, nil
	}
}
