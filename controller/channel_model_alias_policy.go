package controller

import (
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/model"
)

// 「模型列表 ↔ 别名」一致性检查的上下文。
//
// exclude* 与 stored* 都来自**库里保存前的旧值**：
//   - excludeChannelIds 是本次保存即将覆盖的渠道（单个保存是自己，按标签编辑是该标签下的
//     全部渠道）。它们不算冲突方 —— 否则"渠道自己改自己"会被拒：例如渠道直接卖 y，
//     一次性改成卖 X 且配别名 X -> y，旧行里的 y 会被当成本渠道造成的冲突。
//   - storedModels 是这些渠道当前已有的模型名。已存在的名字属于历史配置，不因为一次
//     无关编辑（改 key、改优先级）把保存挡死 —— 那些名字本来就不可用，拦住保存只会让
//     运营没法把它改好。
//   - storedInternals 同理，用于别名侧：本次沿用的旧内部名不再重复告警。
type modelAliasPolicyContext struct {
	excludeChannelIds []int
	excludeSet        map[int]bool
	storedModels      map[string]bool
	storedInternals   map[string]bool
}

func buildModelAliasPolicyContext(channels []*model.Channel) modelAliasPolicyContext {
	ctx := modelAliasPolicyContext{
		excludeChannelIds: make([]int, 0, len(channels)),
		excludeSet:        make(map[int]bool, len(channels)),
		storedModels:      make(map[string]bool),
		storedInternals:   make(map[string]bool),
	}
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		if channel.Id > 0 && !ctx.excludeSet[channel.Id] {
			ctx.excludeSet[channel.Id] = true
			ctx.excludeChannelIds = append(ctx.excludeChannelIds, channel.Id)
		}
		for _, name := range channel.GetModels() {
			if name = strings.TrimSpace(name); name != "" {
				ctx.storedModels[name] = true
			}
		}
		aliasMap, err := model.ParseModelAlias(channel.GetModelAlias())
		if err != nil {
			// 坏 JSON 由 validateChannel 单独拦，这里按"没有别名"处理
			continue
		}
		for _, internalName := range aliasMap {
			if internalName = strings.TrimSpace(internalName); internalName != "" {
				ctx.storedInternals[internalName] = true
			}
		}
	}
	return ctx
}

// validateModelAliasConsistency 拦住两类会让模型**静默消失**的配置错误。
//
// 这里的"消失"是字面意思：保存接口返回成功、库里也有那条记录，但它不会生成 ability，
// 于是模型广场、/v1/models、体验区、令牌白名单都看不到它，页面上没有任何痕迹。
//
//  1. **模型列表里出现内部名**（别名的右侧）：AddAbilities / UpdateAbilities 会直接跳过
//     这类名字（见 model/model_alias.go 顶部说明）；
//  2. **别名把「正在被别的渠道售卖的名字」声明成内部名**：那些渠道的 ability 会在
//     ReconcileHiddenModelAbilities 里被删掉。
//
// 第二类才是线上那次事故的镜像形态（别名与受害渠道是两条不同的渠道），它比第一类更
// 隐蔽：出事的渠道页面上什么都不显示。
func validateModelAliasConsistency(
	models []string,
	aliasMap map[string]string,
	ctx modelAliasPolicyContext,
) error {
	// 本次保存后会被隐藏的名字 = 本次别名声明的内部名
	declaredNow := make(map[string]bool, len(aliasMap))
	for _, internalName := range aliasMap {
		if internalName = strings.TrimSpace(internalName); internalName != "" {
			declaredNow[internalName] = true
		}
	}

	for _, raw := range models {
		name := strings.TrimSpace(raw)
		if name == "" || ctx.storedModels[name] {
			continue
		}
		if declaredNow[name] {
			return fmt.Errorf("模型 %s 在本次保存的「模型别名」里被声明为内部名：它不会生成 ability，也不会出现在任何模型列表；请改成对外名，或把上游真名配进「模型重定向」", name)
		}
		foreign := filterForeignDeclarers(model.FindModelAliasDeclarers(name), ctx.excludeSet)
		if len(foreign) > 0 {
			parts := make([]string, 0, len(foreign))
			for _, d := range foreign {
				parts = append(parts, fmt.Sprintf("渠道 #%d「%s」（对外名 %s）", d.ChannelId, d.ChannelName, d.PublicName))
			}
			return fmt.Errorf("模型 %s 被 %s 的「模型别名」声明为内部名，保存后不会生成 ability，也不会出现在任何模型列表；请改用对外名，或把上游真名配进该渠道的「模型重定向」", name, strings.Join(parts, "、"))
		}
		// 兜底：库里的别名声明已被删掉、而内部名缓存还没同步到时，AddAbilities 仍会按旧
		// 缓存跳过这个名字。提示与上面区分开，免得运营去查一个已经查不到的声明。
		if model.IsHiddenModelName(name) && len(model.FindModelAliasDeclarers(name)) == 0 {
			return fmt.Errorf("模型 %s 当前被模型别名的内部名缓存视为内部名（声明已从库里消失，缓存待同步），保存后不会生成 ability；请稍后重试，或检查是否还有渠道把它配成别名的值", name)
		}
	}

	for publicName, rawInternal := range aliasMap {
		internalName := strings.TrimSpace(rawInternal)
		if internalName == "" || ctx.storedInternals[internalName] {
			continue
		}
		// 这个名字早就在别处被声明成内部名 -> 伤害已经发生，不因为一次无关编辑挡住保存
		if len(filterForeignDeclarers(model.FindModelAliasDeclarers(internalName), ctx.excludeSet)) > 0 {
			continue
		}
		owners := make([]string, 0)
		for _, owner := range model.FindChannelsWithAbilityFor(internalName) {
			if owner == nil || ctx.excludeSet[owner.Id] {
				continue
			}
			owners = append(owners, fmt.Sprintf("渠道 #%d「%s」（对外名 %s）", owner.Id, owner.Name, publicName))
		}
		if len(owners) > 0 {
			sort.Strings(owners)
			return fmt.Errorf("别名把 %s 声明为内部名，但 %s 正在售卖它：保存后那些模型会从所有模型列表里消失（ability 会被重建删掉）。请改用「模型重定向」（只改写发往上游的名字、不隐藏），或先把它改成对外名", internalName, strings.Join(owners, "、"))
		}
	}
	return nil
}

func filterForeignDeclarers(declarers []model.ModelAliasDeclarer, excludeSet map[int]bool) []model.ModelAliasDeclarer {
	foreign := make([]model.ModelAliasDeclarer, 0, len(declarers))
	for _, d := range declarers {
		if excludeSet[d.ChannelId] {
			continue
		}
		foreign = append(foreign, d)
	}
	return foreign
}
