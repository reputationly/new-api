package model

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// 复用本包 TestMain（内存 SQLite、RedisEnabled=false、已 AutoMigrate User）。

func seedPointsUser(t *testing.T, id, points int) {
	t.Helper()
	err := DB.Create(&User{
		Id:            id,
		Username:      fmt.Sprintf("pu_%d", id),
		Role:          1,
		Status:        1,
		PointsBalance: points,
	}).Error
	require.NoError(t, err)
}

func readPoints(t *testing.T, id int) int {
	t.Helper()
	p, err := GetUserPoints(id, true)
	require.NoError(t, err)
	return p
}

func readPointsUsed(t *testing.T, id int) int {
	t.Helper()
	var used int
	require.NoError(t, DB.Model(&User{}).Where("id = ?", id).Select("points_used").Find(&used).Error)
	return used
}

func TestTryDecreaseUserPoints_Sufficient(t *testing.T) {
	truncateTables(t)
	seedPointsUser(t, 101, 1000)

	ok, err := TryDecreaseUserPoints(101, 500)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 500, readPoints(t, 101))
}

func TestTryDecreaseUserPoints_Insufficient(t *testing.T) {
	truncateTables(t)
	seedPointsUser(t, 102, 300)

	ok, err := TryDecreaseUserPoints(102, 500)
	require.NoError(t, err)
	require.False(t, ok)
	// 扣不到时余额不变
	require.Equal(t, 300, readPoints(t, 102))
}

func TestTryDecreaseUserPoints_Exact(t *testing.T) {
	truncateTables(t)
	seedPointsUser(t, 103, 500)

	ok, err := TryDecreaseUserPoints(103, 500)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 0, readPoints(t, 103))
}

// 并发两笔各扣全额，只能成功一笔，积分不为负（§6.4 条件更新）。
func TestTryDecreaseUserPoints_Concurrent(t *testing.T) {
	truncateTables(t)
	seedPointsUser(t, 104, 1000)

	var successes int32
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := TryDecreaseUserPoints(104, 1000)
			if err == nil && ok {
				atomic.AddInt32(&successes, 1)
			}
		}()
	}
	wg.Wait()

	require.Equal(t, int32(1), successes, "并发扣全额应只成功一笔")
	final := readPoints(t, 104)
	require.Equal(t, 0, final)
	require.GreaterOrEqual(t, final, 0, "积分永不为负")
}

// DecreaseUserPoints 扣超余额时钳到 0，不产生负数。
func TestDecreaseUserPoints_ClampToZero(t *testing.T) {
	truncateTables(t)
	seedPointsUser(t, 105, 300)

	require.NoError(t, DecreaseUserPoints(105, 500, false))
	require.Equal(t, 0, readPoints(t, 105))
}

func TestIncreaseUserPoints_RejectsNegative(t *testing.T) {
	truncateTables(t)
	seedPointsUser(t, 106, 100)

	require.Error(t, IncreaseUserPoints(106, -5, true))
	require.Equal(t, 100, readPoints(t, 106))

	// 0 是 no-op，不报错
	require.NoError(t, IncreaseUserPoints(106, 0, true))
	require.Equal(t, 100, readPoints(t, 106))
}

// TryMarkKycPointsGranted 原子占位：仅首次返回 true，防 KYC reset 后重复发放（§8.2）。
func TestTryMarkKycPointsGranted_Idempotent(t *testing.T) {
	truncateTables(t)
	seedPointsUser(t, 107, 0)

	first, err := TryMarkKycPointsGranted(107)
	require.NoError(t, err)
	require.True(t, first)

	second, err := TryMarkKycPointsGranted(107)
	require.NoError(t, err)
	require.False(t, second, "第二次占位应失败（已发放）")
}

func TestTryMarkKycPointsGranted_Concurrent(t *testing.T) {
	truncateTables(t)
	seedPointsUser(t, 108, 0)

	var wins int32
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := TryMarkKycPointsGranted(108)
			if err == nil && ok {
				atomic.AddInt32(&wins, 1)
			}
		}()
	}
	wg.Wait()
	require.Equal(t, int32(1), wins, "并发占位只应一人成功")
}

func TestAddUserPointsUsed(t *testing.T) {
	truncateTables(t)
	seedPointsUser(t, 109, 0)

	require.NoError(t, AddUserPointsUsed(109, 100))
	require.Equal(t, 100, readPointsUsed(t, 109))

	// 累加
	require.NoError(t, AddUserPointsUsed(109, 50))
	require.Equal(t, 150, readPointsUsed(t, 109))

	// <=0 为 no-op
	require.NoError(t, AddUserPointsUsed(109, 0))
	require.Equal(t, 150, readPointsUsed(t, 109))
}
