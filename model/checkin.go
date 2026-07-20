package model

import (
	"errors"
	"math/rand"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

// 签到奖励类型
const (
	CheckinRewardQuota  = "quota"  // 发额度（默认，向后兼容）
	CheckinRewardPoints = "points" // 发积分
)

// Checkin 签到记录
type Checkin struct {
	Id           int    `json:"id" gorm:"primaryKey;autoIncrement"`
	UserId       int    `json:"user_id" gorm:"not null;uniqueIndex:idx_user_checkin_date"`
	CheckinDate  string `json:"checkin_date" gorm:"type:varchar(10);not null;uniqueIndex:idx_user_checkin_date"` // 格式: YYYY-MM-DD
	QuotaAwarded int    `json:"quota_awarded" gorm:"not null"`                                                   // 当次奖励(quota unit)，积分模式存换算后的 quota unit
	RewardType   string `json:"reward_type" gorm:"type:varchar(20);default:'quota'"`                             // 当次奖励类型，历史行默认 quota
	CreatedAt    int64  `json:"created_at" gorm:"bigint"`
}

// CheckinRecord 用于API返回的签到记录（不包含敏感字段）
type CheckinRecord struct {
	CheckinDate  string `json:"checkin_date"`
	QuotaAwarded int    `json:"quota_awarded"`
	RewardType   string `json:"reward_type"`
}

func (Checkin) TableName() string {
	return "checkins"
}

// GetUserCheckinRecords 获取用户在指定日期范围内的签到记录
func GetUserCheckinRecords(userId int, startDate, endDate string) ([]Checkin, error) {
	var records []Checkin
	err := DB.Where("user_id = ? AND checkin_date >= ? AND checkin_date <= ?",
		userId, startDate, endDate).
		Order("checkin_date DESC").
		Find(&records).Error
	return records, err
}

// HasCheckedInToday 检查用户今天是否已签到
func HasCheckedInToday(userId int) (bool, error) {
	today := time.Now().Format("2006-01-02")
	var count int64
	err := DB.Model(&Checkin{}).
		Where("user_id = ? AND checkin_date = ?", userId, today).
		Count(&count).Error
	return count > 0, err
}

// UserCheckin 执行用户签到
// MySQL 和 PostgreSQL 使用事务保证原子性
// SQLite 不支持嵌套事务，使用顺序操作 + 手动回滚
func UserCheckin(userId int) (*Checkin, error) {
	setting := operation_setting.GetCheckinSetting()
	if !setting.Enabled {
		return nil, errors.New("签到功能未启用")
	}

	// 检查今天是否已签到
	hasChecked, err := HasCheckedInToday(userId)
	if err != nil {
		return nil, err
	}
	if hasChecked {
		return nil, errors.New("今日已签到")
	}

	isPoints := setting.RewardType == CheckinRewardPoints

	// 积分总开关关闭时停止发放（§11）：报错而非静默回退发额度，
	// 避免管理员误配置下发出真金白银的 quota
	if isPoints && !operation_setting.GetPointsSetting().Enabled {
		return nil, errors.New("积分系统已关闭，签到积分奖励暂不可用")
	}

	// 积分模式：未实名用户拦截并引导实名（§8.1，引导实名优先）
	if isPoints && !IsUserPointsEligible(userId) {
		return nil, errors.New("请先完成实名认证后参与积分签到")
	}

	// 计算奖励额（统一存 quota unit；积分模式先按积分数随机再换算）
	var quotaAwarded int
	rewardType := CheckinRewardQuota
	if isPoints {
		rewardType = CheckinRewardPoints
		points := setting.MinPoints
		if setting.MaxPoints > setting.MinPoints {
			points = setting.MinPoints + rand.Intn(setting.MaxPoints-setting.MinPoints+1)
		}
		// min/max_points 默认 0：管理员切到积分模式但未配积分数时，零奖励签到会白白
		// 吞掉当日签到机会——拒绝并暴露误配置（不写签到记录，修好后当天仍可签）
		if points <= 0 {
			return nil, errors.New("签到积分奖励未配置，请联系管理员")
		}
		quotaAwarded = common.PointsToQuota(points)
	} else {
		quotaAwarded = setting.MinQuota
		if setting.MaxQuota > setting.MinQuota {
			quotaAwarded = setting.MinQuota + rand.Intn(setting.MaxQuota-setting.MinQuota+1)
		}
	}

	today := time.Now().Format("2006-01-02")
	checkin := &Checkin{
		UserId:       userId,
		CheckinDate:  today,
		QuotaAwarded: quotaAwarded,
		RewardType:   rewardType,
		CreatedAt:    time.Now().Unix(),
	}

	// 根据数据库类型选择不同的策略
	if common.UsingSQLite {
		// SQLite 不支持嵌套事务，使用顺序操作 + 手动回滚
		return userCheckinWithoutTransaction(checkin, userId, quotaAwarded, isPoints)
	}

	// MySQL 和 PostgreSQL 支持事务，使用事务保证原子性
	return userCheckinWithTransaction(checkin, userId, quotaAwarded, isPoints)
}

// userCheckinWithTransaction 使用事务执行签到（适用于 MySQL 和 PostgreSQL）
func userCheckinWithTransaction(checkin *Checkin, userId int, quotaAwarded int, isPoints bool) (*Checkin, error) {
	// 目标列：积分模式写 points_balance，否则 quota（列名为内部常量，非用户输入）
	col := "quota"
	if isPoints {
		col = "points_balance"
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		// 步骤1: 创建签到记录
		// 数据库有唯一约束 (user_id, checkin_date)，可以防止并发重复签到
		if err := tx.Create(checkin).Error; err != nil {
			return errors.New("签到失败，请稍后重试")
		}

		// 步骤2: 在事务中增加用户额度/积分
		if err := tx.Model(&User{}).Where("id = ?", userId).
			Update(col, gorm.Expr(col+" + ?", quotaAwarded)).Error; err != nil {
			return errors.New("签到失败：更新额度出错")
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// 事务成功后，异步更新缓存
	go func() {
		if isPoints {
			_ = cacheIncrUserPoints(userId, int64(quotaAwarded))
		} else {
			_ = cacheIncrUserQuota(userId, int64(quotaAwarded))
		}
	}()

	return checkin, nil
}

// userCheckinWithoutTransaction 不使用事务执行签到（适用于 SQLite）
func userCheckinWithoutTransaction(checkin *Checkin, userId int, quotaAwarded int, isPoints bool) (*Checkin, error) {
	// 步骤1: 创建签到记录
	// 数据库有唯一约束 (user_id, checkin_date)，可以防止并发重复签到
	if err := DB.Create(checkin).Error; err != nil {
		return nil, errors.New("签到失败，请稍后重试")
	}

	// 步骤2: 增加用户额度/积分
	// 使用 db=true 强制直接写入数据库，不使用批量更新
	var incErr error
	if isPoints {
		incErr = IncreaseUserPoints(userId, quotaAwarded, true)
	} else {
		incErr = IncreaseUserQuota(userId, quotaAwarded, true)
	}
	if incErr != nil {
		// 如果增加额度失败，需要回滚签到记录
		DB.Delete(checkin)
		return nil, errors.New("签到失败：更新额度出错")
	}

	return checkin, nil
}

// GetUserCheckinStats 获取用户签到统计信息
func GetUserCheckinStats(userId int, month string) (map[string]interface{}, error) {
	// 获取指定月份的所有签到记录
	startDate := month + "-01"
	endDate := month + "-31"

	records, err := GetUserCheckinRecords(userId, startDate, endDate)
	if err != nil {
		return nil, err
	}

	// 转换为不包含敏感字段的记录
	checkinRecords := make([]CheckinRecord, len(records))
	for i, r := range records {
		checkinRecords[i] = CheckinRecord{
			CheckinDate:  r.CheckinDate,
			QuotaAwarded: r.QuotaAwarded,
			RewardType:   r.RewardType,
		}
	}

	// 检查今天是否已签到
	hasCheckedToday, _ := HasCheckedInToday(userId)

	// 获取用户所有时间的签到统计（按奖励类型分开：额度 vs 积分）
	var totalCheckins int64
	var totalQuota int64
	var totalPoints int64
	DB.Model(&Checkin{}).Where("user_id = ?", userId).Count(&totalCheckins)
	// 历史行 reward_type 可能为空/NULL，按额度归类（向后兼容）
	DB.Model(&Checkin{}).Where("user_id = ? AND (reward_type = ? OR reward_type = '' OR reward_type IS NULL)", userId, CheckinRewardQuota).
		Select("COALESCE(SUM(quota_awarded), 0)").Scan(&totalQuota)
	DB.Model(&Checkin{}).Where("user_id = ? AND reward_type = ?", userId, CheckinRewardPoints).
		Select("COALESCE(SUM(quota_awarded), 0)").Scan(&totalPoints)

	return map[string]interface{}{
		"total_quota":      totalQuota,      // 所有时间累计获得的额度(quota unit)
		"total_points":     totalPoints,     // 所有时间累计获得的积分(quota unit，前端换算积分数)
		"total_checkins":   totalCheckins,   // 所有时间累计签到次数
		"checkin_count":    len(records),    // 本月签到次数
		"checked_in_today": hasCheckedToday, // 今天是否已签到
		"records":          checkinRecords,  // 本月签到记录详情（不含id和user_id）
	}, nil
}
