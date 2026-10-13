package common

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bytedance/gopkg/util/gopool"
)

// shuttingDown 标记进程已进入优雅退出流程。
//
// 为什么需要这个标志位，而不是直接调 http.Server.Shutdown：
//
// Shutdown 保证的是「不再接新连接 + 等在途请求结束」，它**不负责让负载均衡把本实例摘掉**。
// ELB/Service 摘除后端有秒级延迟，这段窗口里新请求仍会被转发进来，而那时监听器已经关了，
// 用户看到的是连接被拒。所以顺序必须是：先置位这个标志让 readiness 探针失败 → 等一会儿
// 让 LB 摘掉本实例 → 再 Shutdown。
//
// 置位后不可撤销：探针要一直返回失败，直到进程退出。若中途变回成功，LB 可能又把流量送进来。
var shuttingDown atomic.Bool

// MarkShuttingDown 置位关闭标志。幂等，可在多个收尾路径重复调用。
func MarkShuttingDown() {
	shuttingDown.Store(true)
}

// IsShuttingDown 报告进程是否已进入关闭流程，供 /healthz 之类的探针端点读取。
func IsShuttingDown() bool {
	return shuttingDown.Load()
}

// ---------- 退出前必须等完的后台任务 ----------
//
// 为什么需要它：请求处理函数里用 gopool.Go 起的那几个 goroutine，在函数返回后就被
// http.Server.Shutdown 当成「请求已结束」了——Shutdown 只等在正常 HTTP 生命周期里的请求，
// **不会等处理函数自己起的 goroutine**。干退钱或记账的是这几处：
//
//   - service.BillingSession.Refund（由 controller/relay.go 的失败路径调用）：
//     资金来源回补 + 订阅额度回补 + 令牌额度回补——**这是线上唯一会被触发的退款路径**；
//   - model.LogQuotaData：数据看板的增量累积；
//   - service.ReturnPreConsumedQuota：请求失败时退还预扣额度。**注意它目前全仓库没有调用方**
//     （含测试，是死代码），所以这一处改与不改都不影响线上；一并转成 GoCritical 只是为了让
//     它将来被接上时天然是对的。别把它当成一条活路径来推理。
//
// 它们要么直写数据库，要么把增量放进进程内的累积态（批量更新的 store、看板 map）。
// 两者都必须在收尾刷库**之前**发生：晚于刷库就进了再也没人刷的 store，晚于进程退出
// 就会写到一半被 SIGKILL——用户被白扣，且不会报任何错。
//
// 收敛到这里之后，收尾只要「等这个计数归零 → 再刷库」，就保证每一笔退款要么已经落库、
// 要么已经进了随后会被刷下去的 store。
var criticalTasks atomic.Int64

// GoCritical 起一个「退出前必须等它做完」的后台任务。适用范围见上。
//
// 用 defer 还账而不是在闭包末尾还：任务 panic 时也必须把计数还回去，否则退出会一直
// 白等到预算用尽（gopool 会接住 panic，不会因此打崩进程，所以这行一定会执行）。
func GoCritical(fn func()) {
	criticalTasks.Add(1)
	gopool.Go(func() {
		defer criticalTasks.Add(-1)
		fn()
	})
}

// CriticalTaskCount 当前尚未结束的关键后台任务数。
func CriticalTaskCount() int {
	return int(criticalTasks.Load())
}

// BeginCritical 手工登记一段「退出前必须做完」的工作，返回结束函数（调用方应当 defer 它）。
//
// 供**定时循环里的一轮**使用——它不像请求处理函数那样是"起一个 goroutine 就结束"，
// 而是长驻循环里的一轮，所以不能包成 GoCritical。与 GoCritical 共用同一个计数，
// 因此收尾那一步的等待天然覆盖它。
//
// 若进程已进入关闭流程，返回 ok=false 且不登记——调用方**应当放弃这一轮**，不要再开新的
// 工作（新开的工作可能晚于收尾的刷库，甚至晚于进程退出）。
//
// ⚠️ 内部顺序是「**先加计数、再读关闭标志**」，这不是随手写的：
// 调用方一旦看到 ok=true（也就是读到了"还没开始关闭"），它的登记就必须已经能被收尾
// 那一步的计数读取看到。反过来先读标志再加计数的话，两者之间存在窗口——收尾可能在计数
// 还是 0 时越过等待，随后这一轮才开始退款并晚于刷库落地。
// Go 的 sync/atomic 是顺序一致的，所以「Add(1) → Load(标志)=false」蕴含
// 「Add(1) 先于 Store(true) 先于收尾的 Load(计数)」，收尾必定看得到这个 1。
func BeginCritical() (done func(), ok bool) {
	criticalTasks.Add(1)
	if IsShuttingDown() {
		criticalTasks.Add(-1)
		return func() {}, false
	}
	var once sync.Once
	return func() { once.Do(func() { criticalTasks.Add(-1) }) }, true
}

// WaitCriticalTasks 等待已登记的关键后台任务全部结束，直到 ctx 到期；返回是否已全部结束。
//
// 用「atomic 计数 + 轮询」而不是 sync.WaitGroup：WaitGroup 要求「计数归零后新增的 Add
// 必须发生在 Wait 之前」，而这里恰好相反——收尾开始等待时仍可能有任务被登记
// （主节点的定时任务在收尾期间也能触发退款）。轮询不受这个约束，代价只是 20ms 粒度；
// 而且与 WaitWebSockets / DrainModerationLogQueue 的写法一致。
func WaitCriticalTasks(ctx context.Context) bool {
	if CriticalTaskCount() == 0 {
		return true
	}
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return CriticalTaskCount() == 0
		case <-ticker.C:
			if CriticalTaskCount() == 0 {
				return true
			}
		}
	}
}
