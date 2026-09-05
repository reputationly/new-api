package controller

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// 聚合模型必须能被存进令牌白名单。
//
// 它没有渠道 ability,不会出现在 GetGroupEnabledModels 里,而 validateTokenModelLimits
// 正是拿那份名单当 available 集合的 —— 不额外放行的话,凡是开了模型限制的令牌都存不进
// 聚合模型名(保存直接报错)。而"隐藏能力的定向发放"恰恰要靠令牌白名单圈定集成方,
// 存不进去等于整个功能对目标用户不可用。
//
// 与 middleware 侧的展开是同一件事的两面:那边让聚合模型名能被路由,这边让它能被存。
// 只改一边都会坏,所以两处的分组语义必须一致(见 TestGroupAllowedMatchesTokenSideSemantics)。
func TestValidateTokenModelLimitsAcceptsAggregateModel(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       3001,
		Username: "agg-limit-user",
		Password: "password",
		Group:    "default",
		AffCode:  "agg-aff-3001",
		Status:   common.UserStatusEnabled,
	}).Error)
	withAggregateModelConfig(t, `[{
		"name":"h3-2k","type":"video","enabled":true,
		"generate":{"model":"minimax-h3"}
	}]`)

	err := validateTokenModelLimits(3001, &model.Token{
		ModelLimitsEnabled: true,
		ModelLimits:        "h3-2k",
	})
	require.NoError(t, err, "聚合模型名应能存进令牌白名单")
}

// 配了 groups 的聚合模型,不在名单里的分组不该能存进白名单 ——
// 否则会存下一份「配得上却调不通」的令牌(调用时展开那步会按分组拒绝)。
func TestValidateTokenModelLimitsRespectsAggregateGroups(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       3002,
		Username: "agg-limit-user2",
		Password: "password",
		Group:    "default",
		AffCode:  "agg-aff-3002",
		Status:   common.UserStatusEnabled,
	}).Error)
	withAggregateModelConfig(t, `[{
		"name":"vip-only","type":"video","enabled":true,
		"groups":["vip"],
		"generate":{"model":"minimax-h3"}
	}]`)

	err := validateTokenModelLimits(3002, &model.Token{
		ModelLimitsEnabled: true,
		ModelLimits:        "vip-only",
	})
	require.Error(t, err, "default 分组的用户不该能把 vip 专属的聚合模型存进白名单")
}

// 停用的聚合模型同样存不进去(GetAggregateModels 只返回 enabled 的)。
func TestValidateTokenModelLimitsRejectsDisabledAggregate(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.Create(&model.User{
		Id:       3003,
		Username: "agg-limit-user3",
		Password: "password",
		Group:    "default",
		AffCode:  "agg-aff-3003",
		Status:   common.UserStatusEnabled,
	}).Error)
	withAggregateModelConfig(t, `[{
		"name":"off-one","type":"video","enabled":false,
		"generate":{"model":"minimax-h3"}
	}]`)

	err := validateTokenModelLimits(3003, &model.Token{
		ModelLimitsEnabled: true,
		ModelLimits:        "off-one",
	})
	require.Error(t, err, "停用的聚合模型不该能存进白名单")
}
