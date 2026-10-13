package common

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// BeginCritical / GoCritical 的契约。两者共用同一个计数，所以收尾那一步的等待天然覆盖
// 定时循环里的一轮。
//
// 两半放在同一个函数里：MarkShuttingDown() 不可撤销、会持续影响整个 common 测试二进制，
// 所以"未关闭时能登记"和"已关闭时拒绝"必须在同一个用例里按顺序验，不能拆成两个函数
// （拆开的话执行顺序没有保证，前一半会随机失败）。
func TestBeginCriticalContract(t *testing.T) {
	// ---- 1) 未进入关闭流程：登记成功、计数 +1，done 幂等 ----
	before := CriticalTaskCount()
	done, ok := BeginCritical()
	require.True(t, ok, "未处于关闭流程时应登记成功")
	require.Equal(t, before+1, CriticalTaskCount(), "登记后计数应 +1")

	done()
	require.Equal(t, before, CriticalTaskCount(), "done 后计数应还回去")
	done()
	require.Equal(t, before, CriticalTaskCount(), "done 应幂等（重复调用不能把计数减成负的）")

	// GoCritical 与它共用计数：收尾只需要等这一个数
	baseline := CriticalTaskCount()
	GoCritical(func() {})
	require.Eventually(t, func() bool { return CriticalTaskCount() == baseline }, 2*time.Second, 10*time.Millisecond,
		"GoCritical 与 BeginCritical 必须共用同一个计数")

	// ---- 2) 进入关闭流程后：拒绝登记，且不改计数 ----
	MarkShuttingDown()

	before = CriticalTaskCount()
	done, ok = BeginCritical()
	require.False(t, ok, "已进入关闭流程时应拒绝登记——调用方据此放弃这一轮")
	require.Equal(t, before, CriticalTaskCount(), "被拒绝时不应留下计数")
	done()
	require.Equal(t, before, CriticalTaskCount(), "被拒绝时返回的 done 应是空操作")
}
