package model

import (
	"errors"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
)

const (
	BatchUpdateTypeUserQuota = iota
	BatchUpdateTypeTokenQuota
	BatchUpdateTypeUsedQuota
	BatchUpdateTypeChannelUsedQuota
	BatchUpdateTypeRequestCount
	BatchUpdateTypeUserPoints
	BatchUpdateTypeCount // if you add a new type, you need to add a new map and a new lock
)

var batchUpdateStores []map[int]int
var batchUpdateLocks []sync.Mutex

// batchUpdateMu 串行化**整个**刷库过程，包括逐条写库那一段，不只是"把 store 换成新 map"那一下。
//
// 为什么需要它：batchUpdate 先在锁内把 store 换成空 map，再逐条写库。如果不串行化整轮，
// 退出时的 FlushBatchUpdate 会看到 store 已经空了而立即返回，调用方（优雅退出）据此
// 认为"额度已刷完"，随后进程退出、数据库关闭——而上一轮还没写完的那些增量就丢了。
// 这正是优雅退出要防的场景，却恰好发生在退出路径上。
//
// 加锁不会死锁：batchUpdate 内部只调 increaseUserQuota / increaseTokenQuota /
// updateUserUsedQuota / updateUserRequestCount / updateChannelUsedQuota / increaseUserPoints，
// 这 6 个都只做数据库写，不会回过来调 addNewRecord 或 batchUpdate。
var batchUpdateMu sync.Mutex

func init() {
	for i := 0; i < BatchUpdateTypeCount; i++ {
		batchUpdateStores = append(batchUpdateStores, make(map[int]int))
		batchUpdateLocks = append(batchUpdateLocks, sync.Mutex{})
	}
}

func InitBatchUpdater() {
	gopool.Go(func() {
		for {
			time.Sleep(time.Duration(common.BatchUpdateInterval) * time.Second)
			batchUpdate()
		}
	})
}

// FlushBatchUpdate 立即把内存里累积的额度变动刷库，供优雅退出调用。
// 返回时保证：调用之前已入队的所有增量都已落库。
//
// 这个保证靠 batchUpdateMu：如果后台那轮正在逐条写库，这里会等它写完再刷自己那一批。
// 只靠 batchUpdateLocks（每个类型一把、只护住"换 map"）是不够的——那会漏掉正在写的那一批。
//
// 未开启批量更新时是空操作——那种情况下每笔变动本来就直接写库，没有东西要刷。
func FlushBatchUpdate() {
	if !common.BatchUpdateEnabled {
		return
	}
	batchUpdate()
}

func addNewRecord(type_ int, id int, value int) {
	batchUpdateLocks[type_].Lock()
	defer batchUpdateLocks[type_].Unlock()
	if _, ok := batchUpdateStores[type_][id]; !ok {
		batchUpdateStores[type_][id] = value
	} else {
		batchUpdateStores[type_][id] += value
	}
}

func batchUpdate() {
	// 整轮串行：从"判断有没有数据"到"逐条写库结束"。
	// 只在换 map 那一下加锁是不够的——见 batchUpdateMu 的注释。
	batchUpdateMu.Lock()
	defer batchUpdateMu.Unlock()

	// check if there's any data to update
	hasData := false
	for i := 0; i < BatchUpdateTypeCount; i++ {
		batchUpdateLocks[i].Lock()
		if len(batchUpdateStores[i]) > 0 {
			hasData = true
			batchUpdateLocks[i].Unlock()
			break
		}
		batchUpdateLocks[i].Unlock()
	}

	if !hasData {
		return
	}

	common.SysLog("batch update started")
	for i := 0; i < BatchUpdateTypeCount; i++ {
		batchUpdateLocks[i].Lock()
		store := batchUpdateStores[i]
		batchUpdateStores[i] = make(map[int]int)
		batchUpdateLocks[i].Unlock()
		// TODO: maybe we can combine updates with same key?
		for key, value := range store {
			switch i {
			case BatchUpdateTypeUserQuota:
				err := increaseUserQuota(key, value)
				if err != nil {
					common.SysLog("failed to batch update user quota: " + err.Error())
				}
			case BatchUpdateTypeTokenQuota:
				err := increaseTokenQuota(key, value)
				if err != nil {
					common.SysLog("failed to batch update token quota: " + err.Error())
				}
			case BatchUpdateTypeUsedQuota:
				updateUserUsedQuota(key, value)
			case BatchUpdateTypeRequestCount:
				updateUserRequestCount(key, value)
			case BatchUpdateTypeChannelUsedQuota:
				updateChannelUsedQuota(key, value)
			case BatchUpdateTypeUserPoints:
				err := increaseUserPoints(key, value)
				if err != nil {
					common.SysLog("failed to batch update user points: " + err.Error())
				}
			}
		}
	}
	common.SysLog("batch update finished")
}

func RecordExist(err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return false, err
}

func shouldUpdateRedis(fromDB bool, err error) bool {
	return common.RedisEnabled && fromDB && err == nil
}
