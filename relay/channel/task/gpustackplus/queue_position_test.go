package gpustackplus

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
)

// 排队回显（queue_ahead / estimated_start_seconds）从门面到 TaskInfo 的透传。
//
// 为什么必须由测试守住：这两个字段全程是「有就显示、没有就退回笼统文案」的可选值，
// 漏传、被值类型折成 0、或者在终态没清干净，都不会报错，只会在页面上显示一个看着
// 很合理的错数字——「已完成」旁边挂「前面还有 2 个」，或者「说不准」被渲染成
// 「马上开始」。这类回显错误没有任何自动告警会响。

// shown 让失败信息打出数值而不是指针地址——排查这类回显问题时，"ahead=0x3ae62c33"
// 除了浪费一次重跑之外没有任何用处。
func shown(p *int) any {
	if p == nil {
		return "nil"
	}
	return *p
}

func TestQueueFieldsPassThroughWhileWaiting(t *testing.T) {
	// assigned = 已派到某台实例、坐在它的引擎队列里，这是排队的常态。
	body := []byte(`{"task_id":"t1","status":"assigned","progress":0,
		"queue_ahead":2,"estimated_start_seconds":530}`)

	ti, err := (&TaskAdaptor{}).ParseTaskResult(body)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if ti.Status != model.TaskStatusQueued {
		t.Fatalf("status = %v, want queued", ti.Status)
	}
	if ti.QueueAhead == nil || *ti.QueueAhead != 2 {
		t.Fatalf("QueueAhead = %v, want 2", shown(ti.QueueAhead))
	}
	if ti.EstimatedStartSeconds == nil || *ti.EstimatedStartSeconds != 530 {
		t.Fatalf("EstimatedStartSeconds = %v, want 530", shown(ti.EstimatedStartSeconds))
	}
}

// 0 与「没有」必须可区分：0 是「下一个就轮到我」，nil 是「门面说不准」。
// 用值类型 int 会把两者都变成 0，等于对用户承诺马上开始。
func TestZeroAheadIsNotTheSameAsUnknown(t *testing.T) {
	next, err := (&TaskAdaptor{}).ParseTaskResult(
		[]byte(`{"task_id":"t1","status":"assigned","queue_ahead":0,"estimated_start_seconds":0}`))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if next.QueueAhead == nil {
		t.Fatal("queue_ahead:0 被当成了「没有」——前端会退回笼统文案，丢掉「马上开始」这个信息")
	}
	if *next.QueueAhead != 0 {
		t.Fatalf("QueueAhead = %d, want 0", *next.QueueAhead)
	}

	unknown, err := (&TaskAdaptor{}).ParseTaskResult(
		[]byte(`{"task_id":"t1","status":"assigned","queue_ahead":null}`))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if unknown.QueueAhead != nil {
		t.Fatalf("queue_ahead:null 被当成了 %d——「说不准」会被渲染成「马上开始」", *unknown.QueueAhead)
	}
}

// 老版本门面根本不返回这两个字段，必须安静地退回 nil，而不是 0。
func TestOlderFacadeWithoutTheFieldsIsUnknownNotZero(t *testing.T) {
	ti, err := (&TaskAdaptor{}).ParseTaskResult(
		[]byte(`{"task_id":"t1","status":"queued","progress":0}`))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if ti.QueueAhead != nil || ti.EstimatedStartSeconds != nil {
		t.Fatalf("字段缺失时应为 nil，实际 ahead=%v eta=%v",
			shown(ti.QueueAhead), shown(ti.EstimatedStartSeconds))
	}
}

// 终态不能带排队回显。门面本身对终态回 null，这里守的是「上游哪天回了脏数据」时
// 网关不跟着一起错——「已完成 · 前面还有 2 个」是自相矛盾的。
func TestTerminalStatesCarryNoQueuePosition(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"done", `{"task_id":"t1","status":"done","nfs_path":"/nfs/a.mp4","queue_ahead":2,"estimated_start_seconds":530}`},
		{"failed", `{"task_id":"t1","status":"failed","error":"boom","queue_ahead":2,"estimated_start_seconds":530}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ti, err := (&TaskAdaptor{}).ParseTaskResult([]byte(tc.body))
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}
			if ti.QueueAhead != nil || ti.EstimatedStartSeconds != nil {
				t.Fatalf("终态仍带排队回显：ahead=%v eta=%v",
					shown(ti.QueueAhead), shown(ti.EstimatedStartSeconds))
			}
		})
	}
}

// 运行中也要带：queue_ahead=0 是「已经轮到我了」，前端据此把「排队中」切成「生成中」
// 而不需要等下一轮状态跳变。
func TestRunningTaskKeepsZeroAhead(t *testing.T) {
	ti, err := (&TaskAdaptor{}).ParseTaskResult(
		[]byte(`{"task_id":"t1","status":"running","progress":40,"queue_ahead":0,"estimated_start_seconds":0}`))
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	if ti.Status != model.TaskStatusInProgress {
		t.Fatalf("status = %v, want in_progress", ti.Status)
	}
	if ti.QueueAhead == nil || *ti.QueueAhead != 0 {
		t.Fatalf("QueueAhead = %v, want 0", shown(ti.QueueAhead))
	}
}
