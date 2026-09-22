package service

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

// LayeredFunding 的通用语义测试。
//
// 这些用例刻意不碰数据库：要守的是编排本身——扣减顺序、失败回滚、逆序退还、
// 按快照精确逆转。真实层怎么扣（积分 CAS、钱包授信条件更新）由
// funding_hybrid_test.go 覆盖，两边职责不重叠。
//
// 存在的意义是「加第三层时不会出资金事故」。两层结构下有些分支根本走不到——
// 比如所有层都扣不满的缺口路径，现有钱包层要么全扣要么报错——但加上信用/权益层
// 之后它就是真实路径了，而那时再补测试就晚了。

type fakeLayer struct {
	name     string
	capacity int // 最多能扣多少，-1 表示无限
	err      error
	consumed int
	// takeLog / backLog 记录调用顺序，用于断言编排而非结果
	takeLog *[]string
	backLog *[]string
}

func (f *fakeLayer) layer() fundingLayer {
	return fundingLayer{
		name:    f.name,
		counter: &f.consumed,
		tryTake: func(amount int, _ bool) (int, error) {
			if f.takeLog != nil {
				*f.takeLog = append(*f.takeLog, f.name)
			}
			if f.err != nil {
				return 0, f.err
			}
			if f.capacity < 0 {
				return amount, nil
			}
			take := min(amount, f.capacity)
			f.capacity -= take
			return take, nil
		},
		giveBack: func(amount int) error {
			if f.backLog != nil {
				*f.backLog = append(*f.backLog, f.name)
			}
			f.capacity += amount
			return nil
		},
	}
}

func buildLayered(fakes ...*fakeLayer) *LayeredFunding {
	layers := make([]fundingLayer, 0, len(fakes))
	for _, f := range fakes {
		layers = append(layers, f.layer())
	}
	return &LayeredFunding{layers: layers}
}

var errShortfall = errors.New("不够")

// 按层序扣减：前面的层扣满了才轮到后面的。
func TestLayered_DeductInOrder(t *testing.T) {
	var takeLog []string
	a := &fakeLayer{name: "a", capacity: 30, takeLog: &takeLog}
	b := &fakeLayer{name: "b", capacity: 100, takeLog: &takeLog}
	l := buildLayered(a, b)

	taken, err := l.deduct(50, true, errShortfall)
	require.NoError(t, err)
	require.Equal(t, []int{30, 20}, taken)
	require.Equal(t, 30, a.consumed)
	require.Equal(t, 20, b.consumed)
	require.Equal(t, []string{"a", "b"}, takeLog)
}

// 某层扣光后不再被问：前层已清零时直接跳到下一层。
func TestLayered_SkipsExhaustedLayer(t *testing.T) {
	a := &fakeLayer{name: "a", capacity: 0}
	b := &fakeLayer{name: "b", capacity: -1}
	l := buildLayered(a, b)

	taken, err := l.deduct(50, true, errShortfall)
	require.NoError(t, err)
	require.Equal(t, []int{0, 50}, taken)
	require.Equal(t, 0, a.consumed)
	require.Equal(t, 50, b.consumed)
}

// 这条是 M3 的锁：全部层加起来也扣不满时必须整笔失败。
//
// 两层结构下走不到（钱包层要么全扣要么报错），但加第三层后它就是真实路径。
// 若漏掉这个检查，计数器会记下「扣到了一部分」而账上其实没扣够，
// 后续退款按计数器退，退的比实际扣的多——凭空多给用户钱。
func TestLayered_ShortfallFailsAtomically(t *testing.T) {
	var backLog []string
	a := &fakeLayer{name: "a", capacity: 10, backLog: &backLog}
	b := &fakeLayer{name: "b", capacity: 20, backLog: &backLog}
	l := buildLayered(a, b)

	taken, err := l.deduct(100, true, errShortfall)
	require.ErrorIs(t, err, errShortfall)
	require.Nil(t, taken)
	require.Equal(t, 0, a.consumed, "计数器不得记下未完成的扣减")
	require.Equal(t, 0, b.consumed)
	require.Equal(t, 10, a.capacity, "已扣部分必须退回")
	require.Equal(t, 20, b.capacity)
	require.Equal(t, []string{"b", "a"}, backLog, "回滚按逆序")
}

// 任一层报错即整笔失败，已扣部分逐层退回，计数器不变。
func TestLayered_LayerErrorRollsBackEverything(t *testing.T) {
	boom := errors.New("boom")
	a := &fakeLayer{name: "a", capacity: 40}
	b := &fakeLayer{name: "b", err: boom}
	l := buildLayered(a, b)

	taken, err := l.deduct(100, true, errShortfall)
	require.ErrorIs(t, err, boom)
	require.Nil(t, taken)
	require.Equal(t, 0, a.consumed)
	require.Equal(t, 40, a.capacity, "已扣部分必须退回")
}

// 退还按扣减顺序的逆序：先退最后扣的那层。
func TestLayered_RefundReverseOrder(t *testing.T) {
	var backLog []string
	a := &fakeLayer{name: "a", capacity: 100, backLog: &backLog}
	b := &fakeLayer{name: "b", capacity: 100, backLog: &backLog}
	c := &fakeLayer{name: "c", capacity: 100, backLog: &backLog}
	l := buildLayered(a, b, c)

	_, err := l.deduct(250, true, errShortfall)
	require.NoError(t, err)
	require.Equal(t, []int{100, 100, 50}, []int{a.consumed, b.consumed, c.consumed})

	require.NoError(t, l.refundReverse(120))
	require.Equal(t, 0, c.consumed, "最后扣的先退光")
	require.Equal(t, 30, b.consumed, "退不完的才轮到上一层")
	require.Equal(t, 100, a.consumed, "还没轮到最前面这层")
	require.Equal(t, []string{"c", "b"}, backLog)
}

// 每层最多退到它自己的累计扣减量，不得把某层退成负数。
func TestLayered_RefundNeverExceedsConsumed(t *testing.T) {
	a := &fakeLayer{name: "a", capacity: 100}
	b := &fakeLayer{name: "b", capacity: 100}
	l := buildLayered(a, b)

	_, err := l.deduct(30, true, errShortfall)
	require.NoError(t, err)

	require.NoError(t, l.refundReverse(999))
	require.Equal(t, 0, a.consumed)
	require.Equal(t, 0, b.consumed)
	require.GreaterOrEqual(t, a.capacity, 0)
}

// 按快照精确逆转「刚扣的那一刀」，而不是套用全局退还策略。
//
// 场景：第一刀全走了 a，第二刀因为 a 被扣光而走了 b。逆转第二刀必须退 b，
// 若按「先退最后一层」以外的全局策略就会退错桶，余额与计数器双双错位。
func TestLayered_UnreserveReversesExactSnapshot(t *testing.T) {
	a := &fakeLayer{name: "a", capacity: 100}
	b := &fakeLayer{name: "b", capacity: 100}
	l := buildLayered(a, b)

	first, err := l.deduct(100, true, errShortfall)
	require.NoError(t, err)
	require.Equal(t, []int{100, 0}, first)

	second, err := l.deduct(40, true, errShortfall)
	require.NoError(t, err)
	require.Equal(t, []int{0, 40}, second)

	l.unreserve(second)
	require.Equal(t, 100, a.consumed, "第一刀不受影响")
	require.Equal(t, 0, b.consumed, "第二刀原路退回")
}

// 全额退款清零所有层。
func TestLayered_RefundAll(t *testing.T) {
	a := &fakeLayer{name: "a", capacity: 100}
	b := &fakeLayer{name: "b", capacity: 100}
	l := buildLayered(a, b)

	_, err := l.deduct(150, true, errShortfall)
	require.NoError(t, err)

	require.NoError(t, l.refundAll())
	require.Equal(t, 0, a.consumed)
	require.Equal(t, 0, b.consumed)
	require.Equal(t, 100, a.capacity)
	require.Equal(t, 100, b.capacity)
}

func TestLayered_DeductNonPositiveIsNoOp(t *testing.T) {
	a := &fakeLayer{name: "a", capacity: 100}
	l := buildLayered(a)

	taken, err := l.deduct(0, true, errShortfall)
	require.NoError(t, err)
	require.Equal(t, []int{0}, taken)
	require.Equal(t, 0, a.consumed)
}

// 三层级联：验证泛化后加层不需要改编排代码。这是整个重构的目的本身。
func TestLayered_ThreeLayersCascade(t *testing.T) {
	a := &fakeLayer{name: "points", capacity: 10}
	b := &fakeLayer{name: "cash", capacity: 20}
	c := &fakeLayer{name: "credit", capacity: -1}
	l := buildLayered(a, b, c)

	taken, err := l.deduct(100, true, errShortfall)
	require.NoError(t, err)
	require.Equal(t, []int{10, 20, 70}, taken)
	require.Equal(t, 70, c.consumed, "兜底层承担剩余")
}
