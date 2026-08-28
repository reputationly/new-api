package model

import (
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"

	"github.com/gin-gonic/gin"

	"github.com/bytedance/gopkg/util/gopool"
)

// UserBase struct remains the same as it represents the cached data structure
type UserBase struct {
	Id               int    `json:"id"`
	Group            string `json:"group"`
	Email            string `json:"email"`
	Quota            int    `json:"quota"`
	PointsBalance    int    `json:"points_balance"`
	Status           int    `json:"status"`
	Username         string `json:"username"`
	Setting          string `json:"setting"`
	Role             int    `json:"role"`
	KycStatus        int    `json:"kyc_status"`
	EnterpriseStatus int    `json:"enterprise_status"`
	ParentUserId     int    `json:"parent_user_id"`
}

func (user *UserBase) WriteContext(c *gin.Context) {
	common.SetContextKey(c, constant.ContextKeyUserGroup, user.Group)
	common.SetContextKey(c, constant.ContextKeyUserQuota, user.Quota)
	common.SetContextKey(c, constant.ContextKeyUserStatus, user.Status)
	common.SetContextKey(c, constant.ContextKeyUserEmail, user.Email)
	common.SetContextKey(c, constant.ContextKeyUserName, user.Username)
	common.SetContextKey(c, constant.ContextKeyUserSetting, user.GetSetting())
	common.SetContextKey(c, constant.ContextKeyUserRole, user.Role)
	common.SetContextKey(c, constant.ContextKeyUserKYCStatus, user.KycStatus)
	common.SetContextKey(c, constant.ContextKeyUserEnterpriseStatus, user.EnterpriseStatus)
	common.SetContextKey(c, constant.ContextKeyUserParentId, user.ParentUserId)
}

func (user *UserBase) GetSetting() dto.UserSetting {
	setting := dto.UserSetting{}
	if user.Setting != "" {
		err := common.Unmarshal([]byte(user.Setting), &setting)
		if err != nil {
			common.SysLog("failed to unmarshal setting: " + err.Error())
		}
	}
	return setting
}

// getUserCacheKey returns the key for user cache
func getUserCacheKey(userId int) string {
	return fmt.Sprintf("user:%d", userId)
}

// invalidateUserCache clears user cache
func invalidateUserCache(userId int) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisDelKey(getUserCacheKey(userId))
}

// InvalidateUserCache is the exported version of invalidateUserCache.
// 供 controller 等上层包在用户状态变更（如禁用、删除、角色变更）后主动清理缓存。
func InvalidateUserCache(userId int) error {
	return invalidateUserCache(userId)
}

// updateUserCache updates all user cache fields using hash
func updateUserCache(user User) error {
	if !common.RedisEnabled {
		return nil
	}

	return common.RedisHSetObj(
		getUserCacheKey(user.Id),
		user.ToBaseUser(),
		time.Duration(common.RedisKeyCacheSeconds())*time.Second,
	)
}

// GetUserCache gets complete user cache from hash
func GetUserCache(userId int) (userCache *UserBase, err error) {
	var user *User
	var fromDB bool
	defer func() {
		// Update Redis cache asynchronously on successful DB read
		if shouldUpdateRedis(fromDB, err) && user != nil {
			gopool.Go(func() {
				if err := updateUserCache(*user); err != nil {
					common.SysLog("failed to update user status cache: " + err.Error())
				}
			})
		}
	}()

	// Try getting from Redis first
	userCache, err = cacheGetUserBase(userId)
	if err == nil {
		return userCache, nil
	}

	// If Redis fails, get from DB
	fromDB = true
	user, err = GetUserById(userId, false)
	if err != nil {
		return nil, err // Return nil and error if DB lookup fails
	}

	// Create cache object from user data
	userCache = &UserBase{
		Id:               user.Id,
		Group:            user.Group,
		Quota:            user.Quota,
		PointsBalance:    user.PointsBalance,
		Status:           user.Status,
		Username:         user.Username,
		Setting:          user.Setting,
		Email:            user.Email,
		Role:             user.Role,
		KycStatus:        user.KycStatus,
		EnterpriseStatus: user.EnterpriseStatus,
		ParentUserId:     user.ParentUserId,
	}

	return userCache, nil
}

func cacheGetUserBase(userId int) (*UserBase, error) {
	if !common.RedisEnabled {
		return nil, fmt.Errorf("redis is not enabled")
	}
	var userCache UserBase
	// Try getting from Redis first
	err := common.RedisHGetObj(getUserCacheKey(userId), &userCache)
	if err != nil {
		return nil, err
	}
	return &userCache, nil
}

// Add atomic quota operations using hash fields
func cacheIncrUserQuota(userId int, delta int64) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHIncrBy(getUserCacheKey(userId), "Quota", delta)
}

func cacheDecrUserQuota(userId int, delta int64) error {
	return cacheIncrUserQuota(userId, -delta)
}

// cacheIncrUserPoints 原子增减 Redis Hash 中的积分余额（字段名对应 UserBase.PointsBalance）。
func cacheIncrUserPoints(userId int, delta int64) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHIncrBy(getUserCacheKey(userId), "PointsBalance", delta)
}

func cacheDecrUserPoints(userId int, delta int64) error {
	return cacheIncrUserPoints(userId, -delta)
}

func getUserPointsCache(userId int) (int, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return 0, err
	}
	return cache.PointsBalance, nil
}

// Helper functions to get individual fields if needed
func getUserGroupCache(userId int) (string, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return "", err
	}
	return cache.Group, nil
}

func getUserQuotaCache(userId int) (int, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return 0, err
	}
	return cache.Quota, nil
}

func getUserStatusCache(userId int) (int, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return 0, err
	}
	return cache.Status, nil
}

func getUserNameCache(userId int) (string, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return "", err
	}
	return cache.Username, nil
}

func getUserSettingCache(userId int) (dto.UserSetting, error) {
	cache, err := GetUserCache(userId)
	if err != nil {
		return dto.UserSetting{}, err
	}
	return cache.GetSetting(), nil
}

// New functions for individual field updates
func updateUserStatusCache(userId int, status bool) error {
	if !common.RedisEnabled {
		return nil
	}
	statusInt := common.UserStatusEnabled
	if !status {
		statusInt = common.UserStatusDisabled
	}
	return common.RedisHSetField(getUserCacheKey(userId), "Status", fmt.Sprintf("%d", statusInt))
}

func updateUserQuotaCache(userId int, quota int) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(getUserCacheKey(userId), "Quota", fmt.Sprintf("%d", quota))
}

func updateUserPointsCache(userId int, points int) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(getUserCacheKey(userId), "PointsBalance", fmt.Sprintf("%d", points))
}

func updateUserGroupCache(userId int, group string) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(getUserCacheKey(userId), "Group", group)
}

func UpdateUserGroupCache(userId int, group string) error {
	return updateUserGroupCache(userId, group)
}

func updateUserNameCache(userId int, username string) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(getUserCacheKey(userId), "Username", username)
}

func updateUserSettingCache(userId int, setting string) error {
	if !common.RedisEnabled {
		return nil
	}
	return common.RedisHSetField(getUserCacheKey(userId), "Setting", setting)
}

// GetUserLanguage returns the user's language preference from cache
// Uses the existing GetUserCache mechanism for efficiency
func GetUserLanguage(userId int) string {
	userCache, err := GetUserCache(userId)
	if err != nil {
		return ""
	}
	return userCache.GetSetting().Language
}

// GetBillingUserGroup 返回**计费主体**的用户分组。
//
// 企业子账号（ParentUserId > 0）自己不是计费主体：它用的是主账号的令牌，
// `middleware/auth.go` 取 `token.UserId`（主账号）的分组写进上下文，于是折扣、
// 模型可见性、扣费全部按主账号算（`User.ParentUserId` 的字段注释：「恒为只读视图，
// 不参与计费」）。
//
// 展示侧必须跟着走同一个口径。否则子账号登录看到的是自己那档的价，实际扣的是
// 主账号那档的钱——差多少折就差多少，而且方向是**显示价高于实扣**，用户不会投诉，
// 只会默默觉得贵。详见 docs/user-tier-pricing-and-topup-package-design.md §6ter.2。
//
// 父账号查不到时回退到自身分组：宁可展示一个偏差值，也不让模型广场整个 500。
func GetBillingUserGroup(userCache *UserBase) string {
	if userCache == nil {
		return ""
	}
	if userCache.ParentUserId <= 0 {
		return userCache.Group
	}
	parent, err := GetUserCache(userCache.ParentUserId)
	if err != nil || parent == nil {
		common.SysLog(fmt.Sprintf("GetBillingUserGroup: parent user %d not found for sub-account %d, falling back to own group",
			userCache.ParentUserId, userCache.Id))
		return userCache.Group
	}
	return parent.Group
}
