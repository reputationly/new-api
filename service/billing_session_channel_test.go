package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 积分渠道白名单在**调用点**的行为。
//
// 单测判定函数 IsPointsEnabledForChannel 是不够的——第一版实现在这里读
// relayInfo.ChannelId 就 panic 了，而那批纯函数测试全绿。ChannelId 属于内嵌指针
// ChannelMeta，PreConsumeBilling（controller/relay.go:193）跑在 InitChannelMeta
// 之前，那时它是 nil。这个文件守的就是这条：渠道号只能从 context 取。

func newTestGinContext(channelId int) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	if channelId > 0 {
		common.SetContextKey(c, constant.ContextKeyChannelId, channelId)
	}
	return c
}

// relayInfo 刻意不调 InitChannelMeta，重现主 relay 路径进 PreConsumeBilling 时的状态。
// TokenUnlimited 置 true 是为了绕开令牌额度扣减那一段——本文件测的是渠道白名单，
// 不是令牌记账，给个无限额令牌能让流程走到该走的地方而不引入无关的 seed。
func newTestRelayInfoWithoutChannelMeta(userId int, group string) *relaycommon.RelayInfo {
	return &relaycommon.RelayInfo{
		UserId:         userId,
		UsingGroup:     group,
		UserGroup:      group,
		TokenId:        1,
		TokenUnlimited: true,
	}
}

func seedBillingUser(t *testing.T, id, quota, points int) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.User{
		Id:            id,
		Username:      "bs_user",
		Role:          1,
		Status:        1,
		Group:         "default",
		Quota:         quota,
		PointsBalance: points,
	}).Error)
	t.Cleanup(func() { model.DB.Unscoped().Where("id = ?", id).Delete(&model.User{}) })
}

func withPointsSettingForBilling(t *testing.T, mutate func(ps *operation_setting.PointsSetting)) {
	t.Helper()
	ps := operation_setting.GetPointsSetting()
	backup := *ps
	t.Cleanup(func() { *ps = backup })
	mutate(ps)
}

// 回归：ChannelMeta 为 nil 时不得 panic。
// 这是第一版实现的真实缺陷——所有启用积分的请求都会崩，而纯函数测试一个都没接住。
func TestNewBillingSession_NoPanicWhenChannelMetaNil(t *testing.T) {
	seedBillingUser(t, 90001, 1000000, 500000)
	withPointsSettingForBilling(t, func(ps *operation_setting.PointsSetting) {
		ps.Enabled = true
		ps.EnabledGroups = []string{"default"}
		ps.EnabledChannels = []int{1}
	})

	c := newTestGinContext(1)
	info := newTestRelayInfoWithoutChannelMeta(90001, "default")
	require.Nil(t, info.ChannelMeta, "前提：本用例要覆盖的正是 ChannelMeta 未初始化的状态")

	// 只断言不 panic。走到令牌扣减那步会因本包 TestMain 未初始化 commonKeyCol
	// 而报 SQL 错，那是测试基建的缺口，与本用例要守的行为无关。
	require.NotPanics(t, func() {
		_, _ = NewBillingSession(c, info, 1000)
	})
}

// 白名单渠道 -> 走混扣（积分可用）
func TestNewBillingSession_WhitelistedChannelUsesHybrid(t *testing.T) {
	seedBillingUser(t, 90002, 0, 500000) // 余额 0、只有积分：非混扣会直接判额度不足
	withPointsSettingForBilling(t, func(ps *operation_setting.PointsSetting) {
		ps.Enabled = true
		ps.EnabledGroups = []string{"default"}
		ps.EnabledChannels = []int{1}
	})

	_, apiErr := NewBillingSession(
		newTestGinContext(1),
		newTestRelayInfoWithoutChannelMeta(90002, "default"),
		1000,
	)
	// 余额为 0，若积分没被计入就会在额度检查处被拒。能越过那道检查（哪怕后面因
	// 测试基建在令牌那步报错）就说明混扣生效了——这正是白名单渠道该有的行为。
	require.NotEqual(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode(),
		"白名单渠道下积分应计入可用额度，不该以额度不足拒绝")
}

// 非白名单渠道 -> 不走混扣，余额为 0 时按额度不足拒绝。
// 这条是本功能的目的：免费积分不能用来买外采算力。
func TestNewBillingSession_NonWhitelistedChannelRejectsPointsOnlyUser(t *testing.T) {
	seedBillingUser(t, 90003, 0, 500000)
	withPointsSettingForBilling(t, func(ps *operation_setting.PointsSetting) {
		ps.Enabled = true
		ps.EnabledGroups = []string{"default"}
		ps.EnabledChannels = []int{1} // 只允许 1 号；请求走 9 号（外采）
	})

	_, apiErr := NewBillingSession(
		newTestGinContext(9),
		newTestRelayInfoWithoutChannelMeta(90003, "default"),
		1000,
	)
	require.NotNil(t, apiErr)
	require.Equal(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode(),
		"外采渠道必须把积分排除在可用额度之外——这正是本功能的目的")
}

// 空白名单 = 不限制：升级当天行为不变，这条是升级安全的兜底
func TestNewBillingSession_EmptyChannelWhitelistKeepsLegacyBehaviour(t *testing.T) {
	seedBillingUser(t, 90004, 0, 500000)
	withPointsSettingForBilling(t, func(ps *operation_setting.PointsSetting) {
		ps.Enabled = true
		ps.EnabledGroups = []string{"default"}
		ps.EnabledChannels = []int{} // 未配置
	})

	_, apiErr := NewBillingSession(
		newTestGinContext(9), // 外采渠道，但白名单为空 => 不限制
		newTestRelayInfoWithoutChannelMeta(90004, "default"),
		1000,
	)
	require.NotEqual(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode(),
		"白名单为空时必须维持加这层之前的行为：积分照常计入")
}

// context 里没有渠道号时降级为「不允许积分」，而不是放行或崩溃
func TestNewBillingSession_MissingChannelIdDegradesToWalletOnly(t *testing.T) {
	seedBillingUser(t, 90005, 0, 500000)
	withPointsSettingForBilling(t, func(ps *operation_setting.PointsSetting) {
		ps.Enabled = true
		ps.EnabledGroups = []string{"default"}
		ps.EnabledChannels = []int{1}
	})

	require.NotPanics(t, func() {
		_, apiErr := NewBillingSession(
			newTestGinContext(0), // 不写 ChannelId
			newTestRelayInfoWithoutChannelMeta(90005, "default"),
			1000,
		)
		require.NotNil(t, apiErr)
		require.Equal(t, types.ErrorCodeInsufficientUserQuota, apiErr.GetErrorCode(),
			"取不到渠道号时应保守地不动用积分")
	})
}
