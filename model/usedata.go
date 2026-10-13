package model

import (
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// QuotaData 柱状图数据
type QuotaData struct {
	Id        int    `json:"id"`
	UserID    int    `json:"user_id" gorm:"index"`
	Username  string `json:"username" gorm:"index:idx_qdt_model_user_name,priority:2;size:64;default:''"`
	ModelName string `json:"model_name" gorm:"index:idx_qdt_model_user_name,priority:1;size:64;default:''"`
	CreatedAt int64  `json:"created_at" gorm:"bigint;index:idx_qdt_created_at,priority:2"`
	TokenUsed int    `json:"token_used" gorm:"default:0"`
	Count     int    `json:"count" gorm:"default:0"`
	Quota     int    `json:"quota" gorm:"default:0"`
}

// UpdateQuotaData 周期性把本进程累积的看板增量落库。
//
// ⚠️ **刻意不做 IsMasterNode 门控**（多副本部署下这是必需的，不是漏了）。
//
// 原因：CacheQuotaData 是**进程内** map（没有任何 Redis 或跨进程共享），每个副本只在
// 自己服务过的请求上累积，也只有自己能刷自己那份——SaveQuotaDataCache 遍历的是调用者
// 本进程的 map。如果只在主节点跑这个循环：
//   - 从节点累积的增量永远没人写库，看板只剩主节点那一份（≈ 1/N）；
//   - 而且 SaveQuotaDataCache 正是清空 map 的那一步，不跑它，从节点的 map 永不释放，
//     内存无界增长。
//
// 代价是 N 个副本会并发写 quota_data，于是 SaveQuotaDataCache 的「First 未命中 → Create」
// 在并发下可能产生重复行（两张 Fact 行），而 increaseQuotaData 是按 4 个字段匹配的批量
// UPDATE（无主键、无 LIMIT），之后每笔增量都会同时加到两行上——看板求和偏大。
// 那个竞态的修法是给 quota_data 加唯一索引 + upsert，**不能靠把写入者收敛成一个**：
// 写入者数量是由上面这条进程内约束决定的，收不了。
func UpdateQuotaData() {
	for {
		if common.DataExportEnabled {
			common.SysLog("正在更新数据看板数据...")
			SaveQuotaDataCache()
		}
		time.Sleep(time.Duration(common.DataExportInterval) * time.Minute)
	}
}

// FlushQuotaDataCache 立即把本进程累积的看板增量落库，供优雅退出调用。
//
// 与 UpdateQuotaData 的定时刷库是同一套内存态：不刷的话，退出时最多
// DataExportInterval（默认 5 分钟）内累积的看板增量会随进程一起消失。
// 单容器时 docker stop 也一样丢，但上了 CCE 之后滚动升级、节点排水、缩容都会触发，
// 会持续累积成看板偏少——而且方向与 ⑧.1 的「偏多」相反，两个叠在一起，看板既不能
// 当对账依据，也很难解释差在哪。
//
// 注意不要在这里先取 CacheQuotaDataLock 再调 SaveQuotaDataCache：sync.Mutex 不可重入，
// 那样会自锁。空集合由 SaveQuotaDataCache 自己处理（遍历 0 次）。
func FlushQuotaDataCache() {
	// 关掉数据导出时没有任何东西被累积（LogQuotaData 的调用点也判这个开关），直接放行。
	if !common.DataExportEnabled {
		return
	}
	SaveQuotaDataCache()
}

var CacheQuotaData = make(map[string]*QuotaData)
var CacheQuotaDataLock = sync.Mutex{}

func logQuotaDataCache(userId int, username string, modelName string, quota int, createdAt int64, tokenUsed int) {
	key := fmt.Sprintf("%d-%s-%s-%d", userId, username, modelName, createdAt)
	quotaData, ok := CacheQuotaData[key]
	if ok {
		quotaData.Count += 1
		quotaData.Quota += quota
		quotaData.TokenUsed += tokenUsed
	} else {
		quotaData = &QuotaData{
			UserID:    userId,
			Username:  username,
			ModelName: modelName,
			CreatedAt: createdAt,
			Count:     1,
			Quota:     quota,
			TokenUsed: tokenUsed,
		}
	}
	CacheQuotaData[key] = quotaData
}

func LogQuotaData(userId int, username string, modelName string, quota int, createdAt int64, tokenUsed int) {
	// 只精确到小时
	createdAt = createdAt - (createdAt % 3600)

	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	logQuotaDataCache(userId, username, modelName, quota, createdAt, tokenUsed)
}

func SaveQuotaDataCache() {
	CacheQuotaDataLock.Lock()
	defer CacheQuotaDataLock.Unlock()
	size := len(CacheQuotaData)
	// 如果缓存中有数据，就保存到数据库中
	// 1. 先查询数据库中是否有数据
	// 2. 如果有数据，就更新数据
	// 3. 如果没有数据，就插入数据
	for _, quotaData := range CacheQuotaData {
		quotaDataDB := &QuotaData{}
		DB.Table("quota_data").Where("user_id = ? and username = ? and model_name = ? and created_at = ?",
			quotaData.UserID, quotaData.Username, quotaData.ModelName, quotaData.CreatedAt).First(quotaDataDB)
		if quotaDataDB.Id > 0 {
			//quotaDataDB.Count += quotaData.Count
			//quotaDataDB.Quota += quotaData.Quota
			//DB.Table("quota_data").Save(quotaDataDB)
			increaseQuotaData(quotaData.UserID, quotaData.Username, quotaData.ModelName, quotaData.Count, quotaData.Quota, quotaData.CreatedAt, quotaData.TokenUsed)
		} else {
			DB.Table("quota_data").Create(quotaData)
		}
	}
	CacheQuotaData = make(map[string]*QuotaData)
	common.SysLog(fmt.Sprintf("保存数据看板数据成功，共保存%d条数据", size))
}

func increaseQuotaData(userId int, username string, modelName string, count int, quota int, createdAt int64, tokenUsed int) {
	err := DB.Table("quota_data").Where("user_id = ? and username = ? and model_name = ? and created_at = ?",
		userId, username, modelName, createdAt).Updates(map[string]interface{}{
		"count":      gorm.Expr("count + ?", count),
		"quota":      gorm.Expr("quota + ?", quota),
		"token_used": gorm.Expr("token_used + ?", tokenUsed),
	}).Error
	if err != nil {
		common.SysLog(fmt.Sprintf("increaseQuotaData error: %s", err))
	}
}

func GetQuotaDataByUsername(username string, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	err = DB.Table("quota_data").Where("username = ? and created_at >= ? and created_at <= ?", username, startTime, endTime).Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetQuotaDataByUserId(userId int, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	err = DB.Table("quota_data").Where("user_id = ? and created_at >= ? and created_at <= ?", userId, startTime, endTime).Find(&quotaDatas).Error
	return quotaDatas, err
}

// GetQuotaDataFromLogsByTokenIds 为企业子账户的数据看板从 logs 实时聚合（设计 §4.5，决策点 D10 同款理由）。
//
// 不动 quota_data 聚合表（其无 token 维度，加列会让行数随 key 数线性膨胀并触碰上游热路径）。
// 子账户是低频查看场景，从 logs 聚合（有 user_id/token_id 索引 + 时间窗限制）足够。返回结构与
// GetQuotaDataByUserId 对齐，前端零改动。tokenIds 必须非空（空集合应由 controller 短路为空结果，
// 否则等价于不过滤会泄漏企业全量数据）。
//
// 小时分桶用 created_at - created_at % 3600，% 在 MySQL/PostgreSQL/SQLite 通用，
// 与 LogQuotaData 落库时的分桶口径一致。
func GetQuotaDataFromLogsByTokenIds(userId int, tokenIds []int, startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	if len(tokenIds) == 0 {
		return []*QuotaData{}, nil
	}
	hourExpr := "(logs.created_at - logs.created_at % 3600)"
	var quotaDatas []*QuotaData
	err = LOG_DB.Table("logs").
		Select("logs.model_name as model_name, "+hourExpr+" as created_at, "+
			"count(*) as count, sum(logs.quota) as quota, "+
			"sum(logs.prompt_tokens + logs.completion_tokens) as token_used").
		Where("logs.user_id = ? AND logs.token_id IN ? AND logs.type = ? AND logs.created_at >= ? AND logs.created_at <= ?",
			userId, tokenIds, LogTypeConsume, startTime, endTime).
		Group("logs.model_name, " + hourExpr).
		Find(&quotaDatas).Error
	if err != nil {
		return nil, err
	}
	// 补回 user_id（聚合未选该列），保持返回结构与 quota_data 行一致。
	for _, qd := range quotaDatas {
		qd.UserID = userId
	}
	return quotaDatas, nil
}

func GetQuotaDataGroupByUser(startTime int64, endTime int64) (quotaData []*QuotaData, err error) {
	var quotaDatas []*QuotaData
	err = DB.Table("quota_data").
		Select("username, created_at, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used").
		Where("created_at >= ? and created_at <= ?", startTime, endTime).
		Group("username, created_at").
		Find(&quotaDatas).Error
	return quotaDatas, err
}

func GetAllQuotaDates(startTime int64, endTime int64, username string) (quotaData []*QuotaData, err error) {
	if username != "" {
		return GetQuotaDataByUsername(username, startTime, endTime)
	}
	var quotaDatas []*QuotaData
	// 从quota_data表中查询数据
	// only select model_name, sum(count) as count, sum(quota) as quota, model_name, created_at from quota_data group by model_name, created_at;
	//err = DB.Table("quota_data").Where("created_at >= ? and created_at <= ?", startTime, endTime).Find(&quotaDatas).Error
	err = DB.Table("quota_data").Select("model_name, sum(count) as count, sum(quota) as quota, sum(token_used) as token_used, created_at").Where("created_at >= ? and created_at <= ?", startTime, endTime).Group("model_name, created_at").Find(&quotaDatas).Error
	return quotaDatas, err
}
