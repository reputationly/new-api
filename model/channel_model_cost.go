package model

import (
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// 渠道 × 模型 的上游成本比。设计见
// docs/user-tier-pricing-and-topup-package-design.md §5。
//
// 语义：相对 ModelRatio 目录价的成本比，即供应商给的那个折扣。0.62 表示这个渠道
// 上这个模型的上游成本是目录价的 62%。
//
// 它有两个消费方，这也是它必须是一张表、而不是塞进 channel.Setting JSON 的原因：
//
//	对账  service/reconcile_helpers.go 按 (channel, model) 聚合与供应商账单比对
//	路由  同一优先级层内按成本升序选渠道（P2）
//
// **只支持精确模型名，不支持通配。** 成本是从供应商账单抄来的确定数字，不是运营
// 策略；通配会让「未列出的模型成本是多少」变成一个需要推断的问题，而成本必填
// （§5.5）的前提恰恰是每个 (渠道, 模型) 组合都有明确答案。
type ChannelModelCost struct {
	Id        int    `json:"id"`
	ChannelId int    `json:"channel_id" gorm:"not null;index:idx_cmc_channel_model,priority:1"`
	ModelName string `json:"model_name" gorm:"type:varchar(128);not null;index:idx_cmc_channel_model,priority:2"`
	// 用 precision/scale 而不是 type:decimal(10,6)：SQLite 驱动（glebarez/sqlite）的 DDL
	// 解析器抓列类型的正则字符集不含逗号，会把 decimal(10,6) 读成 decimal(10，进而每次
	// AutoMigrate 都误判该列需要变更、走 recreateTable，并在参数替换时把类型写坏成 ?,6)，
	// 导致「第一次启动正常、第二次启动 FATAL」。改用 precision/scale 后由各方言自己生成：
	// MySQL decimal(10, 6)、PostgreSQL numeric(10, 6)、SQLite real —— 语义不变且无逗号。
	CostRatio float64 `json:"cost_ratio" gorm:"precision:10;scale:6;not null;default:1"`
	Remark    string  `json:"remark" gorm:"type:varchar(255);default:''"`

	CreatedAt int64 `json:"created_at" gorm:"bigint"`
	UpdatedAt int64 `json:"updated_at" gorm:"bigint"`
}

// channelModelCostCache: channelId -> modelName -> costRatio。
// 成本要参与记账与选路（P2），热路径不能查库。
var (
	channelModelCostCache = map[int]map[string]float64{}
	channelModelCostLock  sync.RWMutex
)

// InitChannelModelCostCache 全量重建成本缓存。
//
// **不挂在 InitChannelCache 上**：那个函数在 `!common.MemoryCacheEnabled` 时直接
// return（main.go:112 整块也只在开内存缓存时才跑），而成本要参与记账，关掉内存
// 缓存的部署一样得有。因此与 SyncOptions 同构——无条件初始化 + 无条件轮询。
func InitChannelModelCostCache() {
	var rows []*ChannelModelCost
	if err := DB.Find(&rows).Error; err != nil {
		common.SysError("failed to load channel model costs: " + err.Error())
		return
	}
	fresh := make(map[int]map[string]float64, len(rows))
	for _, r := range rows {
		if fresh[r.ChannelId] == nil {
			fresh[r.ChannelId] = make(map[string]float64)
		}
		fresh[r.ChannelId][r.ModelName] = r.CostRatio
	}
	channelModelCostLock.Lock()
	channelModelCostCache = fresh
	channelModelCostLock.Unlock()
}

// SyncChannelModelCostCache 周期性重建成本缓存，供多节点部署感知其他节点的改动。
// 与 model.SyncOptions 同一机制：编辑方主动刷新本节点，其余节点最多滞后一个周期。
func SyncChannelModelCostCache(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		InitChannelModelCostCache()
	}
}

// GetChannelModelCostRatio 返回某渠道某模型的成本比。
//
// 第二个返回值区分「配了 1.0」与「没配」——两者在记账上等价，但在管理端与对账页
// 必须区分：没配的渠道要标红提醒补录（§5.5），把它显示成「零差异」会让运营以为
// 账已经对上了。
func GetChannelModelCostRatio(channelId int, modelName string) (float64, bool) {
	channelModelCostLock.RLock()
	defer channelModelCostLock.RUnlock()

	models, ok := channelModelCostCache[channelId]
	if !ok {
		return 0, false
	}
	ratio, ok := models[modelName]
	if !ok {
		return 0, false
	}
	return ratio, true
}

// GetChannelModelCosts 返回某渠道已配置的全部成本，供管理端编辑页读取。
func GetChannelModelCosts(channelId int) ([]*ChannelModelCost, error) {
	var rows []*ChannelModelCost
	err := DB.Where("channel_id = ?", channelId).Order("model_name asc").Find(&rows).Error
	return rows, err
}

// ReplaceChannelModelCosts 用给定集合整体替换某渠道的成本配置。
//
// 整体替换而非增量合并：管理端编辑的是「这个渠道的完整报价单」，删掉一行就该是
// 删掉。增量语义下删除需要额外的接口，且「页面上没有的行」与「没提交的行」无法
// 区分——供应商换一版报价单时最容易出错的地方。
func ReplaceChannelModelCosts(channelId int, costs []*ChannelModelCost) error {
	now := common.GetTimestamp()
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("channel_id = ?", channelId).Delete(&ChannelModelCost{}).Error; err != nil {
			return err
		}
		if len(costs) == 0 {
			return nil
		}
		for _, c := range costs {
			c.Id = 0
			c.ChannelId = channelId
			c.CreatedAt = now
			c.UpdatedAt = now
		}
		return tx.CreateInBatches(costs, 100).Error
	})
	if err != nil {
		return err
	}
	InitChannelModelCostCache()
	return nil
}

// DeleteChannelModelCosts 清掉某渠道的全部成本配置，渠道删除时调用——否则成本表
// 会累积指向已不存在渠道的孤儿行，对账按 channel 聚合时凭空多出几列。
func DeleteChannelModelCosts(channelId int) error {
	if err := DB.Where("channel_id = ?", channelId).Delete(&ChannelModelCost{}).Error; err != nil {
		return err
	}
	InitChannelModelCostCache()
	return nil
}
