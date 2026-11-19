package controller

import (
	"fmt"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/model"
)

// 「模型列表 ↔ 别名」一致性检查的判据，全部建立在**保存生效后的状态**上。
//
// 为什么不能按"本次提交了什么"判断：请求里没带的字段，库里旧值仍然生效（GORM 的
// Updates 只写非零字段）。例如"只改模型、不改别名"的一次保存里，别名其实还是旧的
// `X -> y`；若按提交内容判断，就会把这个"自己的旧别名"当成不存在，于是漏掉
// "旧别名 + 新加的模型名 y" 这种组合 —— 保存成功后 y 不会生成 ability。
//
// 于是每个 target 同时记录两套状态：Stored*（库里，用于判断"这次是不是新增了问题"）
// 与 Effective*（保存后，用于判断"最终会不会坏"）。只有**本次保存新造成的**不可见才拦：
// 历史遗留的脏数据本来就不可用，拦住保存只会让运营没法把它改好。
type modelAliasPolicyTarget struct {
	ChannelId       int
	StoredModels    []string
	EffectiveModels []string
	StoredAlias     map[string]string
	EffectiveAlias  map[string]string
}

// buildPolicyTargetsFromChannels 按"提交了就覆盖、没提交（或提交空）就用库里的"算出
// 每条渠道保存后生效的模型列表与别名 —— 与 channel.Update() 里 GORM Updates 的语义一致。
func buildPolicyTargetsFromChannels(
	channels []*model.Channel,
	submittedModels []string,
	modelsSubmitted bool,
	submittedAlias map[string]string,
	aliasSubmitted bool,
) []modelAliasPolicyTarget {
	targets := make([]modelAliasPolicyTarget, 0, len(channels))
	for _, channel := range channels {
		if channel == nil {
			continue
		}
		storedAlias, _ := model.ParseModelAlias(channel.GetModelAlias())
		target := modelAliasPolicyTarget{
			ChannelId:    channel.Id,
			StoredModels: normalizeModelNames(channel.GetModels()),
			StoredAlias:  storedAlias,
		}
		if modelsSubmitted {
			target.EffectiveModels = normalizeModelNames(submittedModels)
		} else {
			target.EffectiveModels = target.StoredModels
		}
		if aliasSubmitted {
			target.EffectiveAlias = submittedAlias
		} else {
			target.EffectiveAlias = storedAlias
		}
		targets = append(targets, target)
	}
	return targets
}

// policyExcludeSet 是"本次保存涉及的渠道"：判断"别的渠道是否声明/售卖某个名字"时要排除
// 它们 —— 它们的旧配置马上被覆盖，由各自的 Effective* 状态代表。
func policyExcludeSet(targets []modelAliasPolicyTarget) map[int]bool {
	excluded := make(map[int]bool, len(targets))
	for _, target := range targets {
		if target.ChannelId > 0 {
			excluded[target.ChannelId] = true
		}
	}
	return excluded
}

type modelNameViolation struct {
	Model  string
	Reason string
}

// collectUnsafeModelNames 返回"保存后会因为内部名而拿不到 ability"的模型名及原因。
//
// 判据：`名字在生效的模型列表里` 且 `生效后被隐藏`（本次别名声明的内部名，或被别的渠道
// 声明为内部名），并且这一组合在保存前**不成立**（否则就是历史遗留，不拦）。
//
// 抽成独立函数是给上游自动同步用的：那条路径不能因为一个坏名字就整条渠道失败，
// 需要的是"剔掉这些并记日志"（见 channel_upstream_update.go）。
func collectUnsafeModelNames(target modelAliasPolicyTarget, excludeSet map[int]bool) []modelNameViolation {
	declaredNow := make(map[string]bool, len(target.EffectiveAlias))
	for _, internalName := range target.EffectiveAlias {
		if internalName = strings.TrimSpace(internalName); internalName != "" {
			declaredNow[internalName] = true
		}
	}
	declaredBefore := make(map[string]bool, len(target.StoredAlias))
	for _, internalName := range target.StoredAlias {
		if internalName = strings.TrimSpace(internalName); internalName != "" {
			declaredBefore[internalName] = true
		}
	}
	storedModels := make(map[string]bool, len(target.StoredModels))
	for _, name := range target.StoredModels {
		storedModels[name] = true
	}

	declarersByInternal := model.AllModelAliasDeclarers()

	violations := make([]modelNameViolation, 0)
	seen := make(map[string]bool)
	for _, name := range target.EffectiveModels {
		if seen[name] {
			continue
		}
		seen[name] = true
		foreign := filterForeignDeclarers(declarersByInternal[name], excludeSet)
		hiddenAfter := declaredNow[name] || len(foreign) > 0
		if !hiddenAfter {
			continue
		}
		// 保存前就已经是"在模型列表里且被隐藏" -> 历史遗留，不因为一次无关编辑挡住保存
		if storedModels[name] && (declaredBefore[name] || len(foreign) > 0) {
			continue
		}
		if declaredNow[name] {
			violations = append(violations, modelNameViolation{
				Model:  name,
				Reason: fmt.Sprintf("模型 %s 在保存生效的「模型别名」里被声明为内部名：它不会生成 ability，也不会出现在任何模型列表；请把模型列表改成对外名，或把上游真名配进「模型重定向」", name),
			})
			continue
		}
		parts := make([]string, 0, len(foreign))
		for _, d := range foreign {
			parts = append(parts, fmt.Sprintf("渠道 #%d「%s」（对外名 %s）", d.ChannelId, d.ChannelName, d.PublicName))
		}
		violations = append(violations, modelNameViolation{
			Model:  name,
			Reason: fmt.Sprintf("模型 %s 被 %s 的「模型别名」声明为内部名，保存后不会生成 ability，也不会出现在任何模型列表；请改用对外名，或把上游真名配进该渠道的「模型重定向」", name, strings.Join(parts, "、")),
		})
	}

	// 兜底：库里的别名声明已被删掉、而内部名缓存还没同步到（周期重建）时，建 ability 仍会
	// 按旧缓存跳过这个名字。
	for _, name := range target.EffectiveModels {
		if declaredNow[name] || len(filterForeignDeclarers(declarersByInternal[name], excludeSet)) > 0 {
			continue
		}
		if model.IsHiddenModelName(name) && len(declarersByInternal[name]) == 0 && !containsViolation(violations, name) {
			violations = append(violations, modelNameViolation{
				Model:  name,
				Reason: fmt.Sprintf("模型 %s 当前被模型别名的内部名缓存视为内部名（声明已从库里消失，缓存待同步），保存后不会生成 ability；请稍后重试，或检查是否还有渠道把它配成别名的值", name),
			})
		}
	}

	sort.Slice(violations, func(i, j int) bool { return violations[i].Model < violations[j].Model })
	return violations
}

func containsViolation(violations []modelNameViolation, name string) bool {
	for _, v := range violations {
		if v.Model == name {
			return true
		}
	}
	return false
}

// validateModelAliasConsistency 拦住会让模型**静默消失**的配置错误。
//
// 两类事故：
//  1. 模型列表里出现内部名（别名的右侧）→ AddAbilities 直接跳过，连 ability 都不生成；
//  2. 别名把「正在被别的渠道售卖的名字」声明成内部名 → 那些渠道的 ability 在
//     ReconcileHiddenModelAbilities 里被删掉（线上那次事故的镜像形态，更隐蔽）。
//
// 不检查「别名的对外名是否在模型列表里」：那属于"别名配了但不生效"的死配置，不会造成
// 静默消失；而按标签编辑时同一份别名会覆盖标签下**所有**成员，成员各自的模型列表常常
// 不同（A 卖这个、B 卖那个），这条规则会把合法的标签编辑整体拒掉。真正的伤害（模型列表
// 里出现内部名）已由模型列表侧拦住。
func validateModelAliasConsistency(targets []modelAliasPolicyTarget) error {
	excludeSet := policyExcludeSet(targets)

	for _, target := range targets {
		if violations := collectUnsafeModelNames(target, excludeSet); len(violations) > 0 {
			return fmt.Errorf("%s", violations[0].Reason)
		}
	}

	declarersByInternal := model.AllModelAliasDeclarers()
	for _, target := range targets {
		declaredBefore := make(map[string]bool, len(target.StoredAlias))
		for _, internalName := range target.StoredAlias {
			if internalName = strings.TrimSpace(internalName); internalName != "" {
				declaredBefore[internalName] = true
			}
		}

		publicNames := make([]string, 0, len(target.EffectiveAlias))
		for publicName := range target.EffectiveAlias {
			publicNames = append(publicNames, publicName)
		}
		sort.Strings(publicNames)

		for _, publicName := range publicNames {
			internalName := strings.TrimSpace(target.EffectiveAlias[publicName])
			if internalName == "" {
				continue
			}
			// 这个名字早就是内部名（自己旧别名 / 别的渠道）-> 伤害已经发生，不重复告警
			if declaredBefore[internalName] || len(filterForeignDeclarers(declarersByInternal[internalName], excludeSet)) > 0 {
				continue
			}
			owners := make([]string, 0)
			for _, owner := range model.FindChannelsWithAbilityFor(internalName) {
				if owner == nil || excludeSet[owner.Id] {
					continue
				}
				owners = append(owners, fmt.Sprintf("渠道 #%d「%s」（对外名 %s）", owner.Id, owner.Name, publicName))
			}
			if len(owners) > 0 {
				sort.Strings(owners)
				return fmt.Errorf("别名把 %s 声明为内部名，但 %s 正在售卖它：保存后那些模型会从所有模型列表里消失（ability 会被重建删掉）。请改用「模型重定向」（只改写发往上游的名字、不隐藏），或先把它改成对外名", internalName, strings.Join(owners, "、"))
			}
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
