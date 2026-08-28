package model

import (
	"strings"
)

// 分组管理页要回答的两类问题，设计见 docs/group-management-redesign.md §7.0：
//
//  1. 这个分组「通不通」——有没有渠道挂载、覆盖多少模型。分组配了但没渠道，
//     用户选中就报「无可用渠道」；渠道挂了但分组没配，middleware/auth.go 判
//     「分组已被弃用」。两种失配现在都没有任何地方能发现，只能等用户报错。
//  2. 这个分组能不能删——被 user / token / channel / subscription_plan 四处
//     引用，从配置里抹掉一行不会有任何提示。

// GroupCoverage 一个分组的渠道挂载情况，来自 abilities 表（group, model, channel_id）。
type GroupCoverage struct {
	Group        string `json:"group"`
	ChannelCount int    `json:"channel_count"`
	ModelCount   int    `json:"model_count"`
}

// GetGroupCoverage 按分组聚合启用中的 ability。
//
// 只统计 enabled：被禁用的渠道对用户等同于不存在，把它算进「已挂载」会让一个
// 实际不可用的分组显示成健康。
func GetGroupCoverage() (map[string]GroupCoverage, error) {
	// 别名成 group_name 而不是保留字本身：三种数据库对 `SELECT "group" as "group"`
	// 的引号规则各有脾气，绕开比逐库验证便宜。
	//
	// commonGroupCol 只能出现在 Select / Where 这类**原样拼接的 SQL 片段**里。
	// 凡是接收「列名」的 GORM 方法（Group / Pluck / Distinct / Order）都会自己
	// 按方言加引号，再传预引用的值就会变成 ``group``，三种库一律语法错误。
	var rows []struct {
		GroupName    string
		ChannelCount int
		ModelCount   int
	}
	err := DB.Model(&Ability{}).
		Select(commonGroupCol+" as group_name"+
			", COUNT(DISTINCT channel_id) as channel_count"+
			", COUNT(DISTINCT model) as model_count").
		Where("enabled = ?", true).
		Group("group").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}

	result := make(map[string]GroupCoverage, len(rows))
	for _, r := range rows {
		result[r.GroupName] = GroupCoverage{
			Group:        r.GroupName,
			ChannelCount: r.ChannelCount,
			ModelCount:   r.ModelCount,
		}
	}
	return result, nil
}

// GetGroupModels 返回某分组下实际有启用渠道覆盖的模型名。
//
// 分组折扣编辑器的模型下拉用它，而不是全站模型表：给一个本分组根本没有的模型
// 配折扣是纯废配置，不该让人配得出来。
func GetGroupModels(group string) ([]string, error) {
	var models []string
	err := DB.Model(&Ability{}).
		Distinct("model").
		Where(commonGroupCol+" = ? and enabled = ?", group, true).
		Order("model").
		Pluck("model", &models).Error
	return models, err
}

// GetAllEnabledModelNames 返回全站有渠道覆盖的模型名（去重、有序）。
//
// 供「档位折扣」编辑器用：用户档折扣按 (用户分组, 模型) 索引，与走哪条供应链无关，
// 因此不能按分组过滤——否则运营在档位折扣里就配不出那些挂在别的分组上的模型。
func GetAllEnabledModelNames() ([]string, error) {
	var models []string
	err := DB.Model(&Ability{}).
		Distinct("model").
		Where("enabled = ?", true).
		Order("model").
		Pluck("model", &models).Error
	return models, err
}

// GroupReferences 一个分组被引用的次数，用于删除前的影响面提示。
type GroupReferences struct {
	Users    int64 `json:"users"`
	Tokens   int64 `json:"tokens"`
	Channels int64 `json:"channels"`
	Plans    int64 `json:"plans"`
}

func (r GroupReferences) Total() int64 {
	return r.Users + r.Tokens + r.Channels + r.Plans
}

// GetGroupReferences 统计分组的四类引用。
func GetGroupReferences(group string) (GroupReferences, error) {
	var refs GroupReferences

	if err := DB.Model(&User{}).Where(commonGroupCol+" = ?", group).Count(&refs.Users).Error; err != nil {
		return refs, err
	}
	if err := DB.Model(&Token{}).Where(commonGroupCol+" = ?", group).Count(&refs.Tokens).Error; err != nil {
		return refs, err
	}
	if err := DB.Model(&SubscriptionPlan{}).Where("upgrade_group = ?", group).Count(&refs.Plans).Error; err != nil {
		return refs, err
	}

	channels, err := countChannelsInGroup(group)
	if err != nil {
		return refs, err
	}
	refs.Channels = channels
	return refs, nil
}

// countChannelsInGroup 统计 group 字段里包含该分组的渠道。
//
// 不用 SQL LIKE：channel.group 是逗号拼接的字符串（"default,vip"），LIKE '%vip%'
// 会把 "vip_special" 也算进来，而带分隔符的 LIKE 在三种数据库上的转义规则并不一致。
// 渠道数量级是几十到几百，全量拉回来在 Go 里精确切分更省心也更不会算错。
//
// 与 GetGroupCoverage 的分工：那个只看启用中的 ability（回答「用户现在能不能用」），
// 这个要把停用渠道也算上（回答「删掉这个分组会影响到谁」）。
func countChannelsInGroup(group string) (int64, error) {
	var groups []string
	// Pluck 传裸列名，理由见 GetGroupCoverage 的注释
	if err := DB.Model(&Channel{}).Pluck("group", &groups).Error; err != nil {
		return 0, err
	}
	var count int64
	for _, g := range groups {
		for _, item := range strings.Split(g, ",") {
			if strings.TrimSpace(item) == group {
				count++
				break
			}
		}
	}
	return count, nil
}

// GetGroupsWithUsers 返回至少有一个用户属于它的分组。
//
// 判断分组「可达性」要用它：一个没勾「用户可选」的分组，只要管理员把用户分配了
// 进去就是生效的。只看「用户可选」的话，按用户等级定价的分组会被误报成死配置。
func GetGroupsWithUsers() (map[string]bool, error) {
	var groups []string
	// Pluck 传裸列名，理由见 GetGroupCoverage 的注释
	if err := DB.Model(&User{}).Distinct("group").Pluck("group", &groups).Error; err != nil {
		return nil, err
	}
	result := make(map[string]bool, len(groups))
	for _, g := range groups {
		if g = strings.TrimSpace(g); g != "" {
			result[g] = true
		}
	}
	return result, nil
}

// GetGroupsUsedByChannels 返回所有被渠道挂载过的分组名。
//
// 用于「失配提示条」：这里面出现、但分组配置里没有的名字，意味着那些渠道当前
// 完全不可用——用户拿不到令牌，管理员在任何页面也看不到异常。
func GetGroupsUsedByChannels() ([]string, error) {
	var groups []string
	// Pluck 传裸列名，理由见 GetGroupCoverage 的注释
	if err := DB.Model(&Channel{}).Pluck("group", &groups).Error; err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	result := make([]string, 0)
	for _, g := range groups {
		for _, item := range strings.Split(g, ",") {
			name := strings.TrimSpace(item)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			result = append(result, name)
		}
	}
	return result, nil
}
