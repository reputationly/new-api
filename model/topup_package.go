package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
)

// 充值套餐。设计见 docs/user-tier-pricing-and-topup-package-design.md §6。
//
// **只送积分**：1:1 到账，不额外送额度，不附带档位折扣。
//
// 曾设计成三重让利（送额度 + 送积分 + 永久档位折扣），被否决——第三重取决于用户
// 未来消费多少，运营在配置界面上根本算不出来。实算一遍：到账 ¥1250 按 7 折能买
// ¥1786 的目录价服务，成本率高于 56% 即净亏，而运营看到的是「送 20% + 5000 积分」，
// 感觉让利 25%，实际 44%。
//
// 只送积分之后让利可精确计算且封顶：积分只能用在自建模型（points_setting 白名单），
// 送出去的是电费而非供应商账单。5000 积分面值 ¥50、自建成本约 ¥10，实际让利 1%，
// 而用户感知是「充 1000 送 5000 分」——积分数字大、成本封在电费里，营销杠杆率高。
//
// ⚠️ 这个性质的唯一保障是「积分白名单里不能出现外采模型」。一旦某个白名单模型挂了
// 外采渠道，送积分就从「送电费」变成「送供应商账单」，成本放大 5 倍。
type TopupPackage struct {
	Id       int    `json:"id"`
	Title    string `json:"title" gorm:"type:varchar(128);not null"`
	Subtitle string `json:"subtitle" gorm:"type:varchar(255);default:''"`

	// PriceAmount 售价；到账额度与它 1:1，不单独存字段
	// 用 precision/scale 而不是 type:decimal(10,6)：SQLite 驱动（glebarez/sqlite）的 DDL
	// 解析器抓列类型的正则字符集不含逗号，会把 decimal(10,6) 读成 decimal(10，进而每次
	// AutoMigrate 都误判该列需要变更、走 recreateTable，并在参数替换时把类型写坏成 ?,6)，
	// 导致「第一次启动正常、第二次启动 FATAL」。改用 precision/scale 后由各方言自己生成：
	// MySQL decimal(10, 6)、PostgreSQL numeric(10, 6)、SQLite real —— 语义不变且无逗号。
	PriceAmount float64 `json:"price_amount" gorm:"precision:10;scale:6;not null;default:0"`
	Currency    string  `json:"currency" gorm:"type:varchar(8);not null;default:'CNY'"`

	// GrantPoints 赠送积分数（不是 quota unit）
	GrantPoints int `json:"grant_points" gorm:"type:int;not null;default:0"`

	Enabled            bool `json:"enabled" gorm:"default:true"`
	SortOrder          int  `json:"sort_order" gorm:"type:int;default:0"`
	MaxPurchasePerUser int  `json:"max_purchase_per_user" gorm:"type:int;default:0"` // 0 = 不限

	CreatedAt int64 `json:"created_at" gorm:"bigint"`
	UpdatedAt int64 `json:"updated_at" gorm:"bigint"`
}

var ErrTopupPackageNotFound = errors.New("充值套餐不存在")

func GetTopupPackageById(id int) (*TopupPackage, error) {
	if id <= 0 {
		return nil, ErrTopupPackageNotFound
	}
	var pkg TopupPackage
	if err := DB.First(&pkg, id).Error; err != nil {
		return nil, ErrTopupPackageNotFound
	}
	return &pkg, nil
}

// GetTopupPackages 返回套餐列表。enabledOnly 供用户侧充值页使用。
func GetTopupPackages(enabledOnly bool) ([]*TopupPackage, error) {
	var list []*TopupPackage
	db := DB.Order("sort_order asc, id asc")
	if enabledOnly {
		db = db.Where("enabled = ?", true)
	}
	err := db.Find(&list).Error
	return list, err
}

func (p *TopupPackage) Insert() error {
	now := common.GetTimestamp()
	p.CreatedAt = now
	p.UpdatedAt = now

	// GORM 对带 `default:true` 标签的字段会把零值当成「未设置」并套用默认值：
	// 新建一个「先不上架」的套餐（Enabled=false）会被直接改成上架开卖。
	// 与 Model.Insert 同一处理——先存原值，Create 之后强制写回。
	enabled := p.Enabled
	if err := DB.Create(p).Error; err != nil {
		return err
	}
	if !enabled {
		if err := DB.Model(&TopupPackage{}).Where("id = ?", p.Id).
			Update("enabled", false).Error; err != nil {
			return err
		}
		// 补回结构体：GORM 的 Create 回调在 INSERT **之前**就把零值 bool 改成了
		// default 值，上面那次 Update 只修了库里的行。而调用方
		// （controller.CreateTopupPackage）会用 ApiSuccess 把这个结构体直接序列化
		// 回客户端，不同步就会出现「接口说已上架、库里是下架」。
		p.Enabled = enabled
	}
	return nil
}

// Update 用 Select 白名单强制更新零值字段。
//
// 与 Model.Update 同一模式，理由也一样：把赠送积分改回 0、把套餐下架（enabled=false）
// 都是正常操作，非零字段更新做不到。新增字段漏进白名单的表现是**保存成功但没存上**
// ——接口 200、页面显示已保存、刷新后配置消失，没有任何错误可查。
func (p *TopupPackage) Update() error {
	p.UpdatedAt = common.GetTimestamp()
	return DB.Model(&TopupPackage{}).Where("id = ?", p.Id).
		Select(topupPackageUpdateSelectFields()).
		Updates(p).Error
}

// TopupPackageUpdateSelectFieldsForTest 暴露白名单供跨包测试断言。
// 不直接导出 topupPackageUpdateSelectFields：它是 Update 的实现细节，
// 只有「新增字段别漏进白名单」这一条需要被外部钉住。
func TopupPackageUpdateSelectFieldsForTest() []string {
	return topupPackageUpdateSelectFields()
}

func topupPackageUpdateSelectFields() []string {
	return []string{
		"title", "subtitle", "price_amount", "currency", "grant_points",
		"enabled", "sort_order", "max_purchase_per_user", "updated_at",
	}
}

func DeleteTopupPackage(id int) error {
	return DB.Delete(&TopupPackage{}, id).Error
}

// CountUserPackagePurchases 统计某用户已成功购买某套餐的次数，用于 MaxPurchasePerUser。
func CountUserPackagePurchases(userId, packageId int) (int64, error) {
	var count int64
	err := DB.Model(&TopUp{}).
		Where("user_id = ? AND package_id = ? AND status = ?",
			userId, packageId, common.TopUpStatusSuccess).
		Count(&count).Error
	return count, err
}

// GrantTopupPackageBonus 充值到账后发放套餐赠品（当前只有积分）。
//
// **必须在事务提交之后调用**，不能塞进各条充值路径自己的事务里：
// IncreaseUserPoints 会异步更新 Redis 缓存，把它包进别人的事务，事务回滚时缓存已经
// 加过了，积分凭空多出来且无法回收。
//
// 幂等性由调用点保证：各条 Recharge* 路径的事务里都判了
// `status != pending 就返回`，所以同一笔订单只会走到这里一次。进程若在事务提交后、
// 本函数执行前崩溃，会漏发一次——日志里有充值成功记录而无赠送记录，可人工补。
// 这个窗口是刻意接受的：为它引入两阶段提交，复杂度远超一笔积分的价值。
//
// 非套餐充值（PackageId = 0）直接返回，这是绝大多数请求的路径。
//
// **已接入的到账路径**（现网只上了自研支付宝 / 微信）：
//
//	RechargeAlipay        支付宝直连
//	RechargeWxpay         微信直连
//	ManualCompleteTopUp   管理员手动补单（回调失败时的兜底，必接）
//
// **未接入**：RechargeCreem / RechargeWaffo / RechargeWaffoPancake / Stripe。
// 这几个支付方式现网未启用，接了也是死代码。若将来启用其中任一，**必须在那条路径
// 的事务提交后补调本函数**——漏了的症状是用户付了钱、额度到账、积分没发，而且不报
// 任何错，只能靠用户投诉发现。
func GrantTopupPackageBonus(topUp *TopUp) {
	if topUp == nil || topUp.PackageId <= 0 {
		return
	}
	pkg, err := GetTopupPackageById(topUp.PackageId)
	if err != nil {
		common.SysError(fmt.Sprintf(
			"topup package %d not found for topup %s, bonus not granted",
			topUp.PackageId, topUp.TradeNo))
		return
	}
	if pkg.GrantPoints <= 0 {
		return
	}
	if err := IncreaseUserPoints(topUp.UserId, pkg.GrantPoints, true); err != nil {
		common.SysError(fmt.Sprintf(
			"failed to grant %d points for topup %s: %s",
			pkg.GrantPoints, topUp.TradeNo, err.Error()))
		return
	}
	RecordLog(topUp.UserId, LogTypeTopup, fmt.Sprintf(
		"购买套餐「%s」赠送积分 %d（订单 %s）",
		pkg.Title, pkg.GrantPoints, topUp.TradeNo))
	logger.LogInfo(nil, fmt.Sprintf("granted %d points to user %d for package %d",
		pkg.GrantPoints, topUp.UserId, pkg.Id))
}
