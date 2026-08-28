package model

import (
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// 模型可见性：按用户档限制谁能看到并调用某个模型。
// 设计见 docs/user-tier-pricing-and-topup-package-design.md §6bis。
//
// 语义是**默认允许、显式限制**：未配置的模型所有人可见。受限的是少数（内测、专属
// 资源、企业定制），公开的是多数，反过来配会让每加一个模型都要记得开权限。
//
// modelVisibilityCache 是稀疏的：只含配了 VisibleGroups 的模型，key 为**展开后的
// 实际模型名**（前缀/后缀/包含规则已经在构建时展开），所以热路径是一次 map 查找。
var (
	modelVisibilityCache = map[string]map[string]bool{}
	modelVisibilityLock  sync.RWMutex
)

// InitModelVisibilityCache 全量重建可见性缓存。
//
// 与渠道成本缓存同构（见 channel_model_cost.go）：无条件初始化 + 无条件轮询，
// 不搭 InitChannelCache 的车——那个函数在 !MemoryCacheEnabled 时直接 return，
// 而可见性是**权限判定**，任何部署形态下都必须生效。
func InitModelVisibilityCache() {
	var allMeta []Model
	if err := DB.Find(&allMeta).Error; err != nil {
		common.SysError("failed to load model visibility: " + err.Error())
		return
	}

	// 没有任何限制时不必去查 abilities，省掉一次全表扫描——这是绝大多数站点的常态
	hasRestriction := false
	for i := range allMeta {
		if strings.TrimSpace(allMeta[i].VisibleGroups) != "" {
			hasRestriction = true
			break
		}
	}
	if !hasRestriction {
		modelVisibilityLock.Lock()
		modelVisibilityCache = map[string]map[string]bool{}
		modelVisibilityLock.Unlock()
		return
	}

	enableAbilities, err := GetAllEnableAbilityWithChannels()
	if err != nil {
		common.SysError("failed to load abilities for model visibility: " + err.Error())
		return
	}

	// 复用 pricing 的展开规则：两处各写一份匹配逻辑，就会出现「页面上配好了、
	// 实际没拦住」——权限功能最典型的失效方式
	metaMap := BuildModelMetaMap(allMeta, enableAbilities)

	fresh := make(map[string]map[string]bool)
	for modelName, meta := range metaMap {
		groups := parseVisibleGroups(meta.VisibleGroups)
		if groups == nil {
			continue
		}
		fresh[modelName] = groups
	}

	modelVisibilityLock.Lock()
	modelVisibilityCache = fresh
	modelVisibilityLock.Unlock()
}

// SyncModelVisibilityCache 周期性重建，供多节点部署感知其他节点的改动。
func SyncModelVisibilityCache(frequency int) {
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		InitModelVisibilityCache()
	}
}

// parseVisibleGroups 解析逗号分隔的档位列表。
// 返回 nil 表示「不限制」；返回空集合表示「配了但一个档都没勾」，即谁都看不到。
func parseVisibleGroups(raw string) map[string]bool {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	groups := make(map[string]bool)
	for _, g := range strings.Split(raw, ",") {
		if g = strings.TrimSpace(g); g != "" {
			groups[g] = true
		}
	}
	return groups
}

// IsModelVisibleForGroup 报告某用户档能否看到并调用某模型。
//
// 未配置限制的模型一律可见（默认允许）。这是刻意的 fail-open：可见性是营销与商务
// 层面的限制，不是安全边界——把它做成 fail-closed，一次配置失误就会让全站模型集体
// 消失，代价远大于「某个受限模型漏配后被看到」。
func IsModelVisibleForGroup(modelName, userGroup string) bool {
	modelVisibilityLock.RLock()
	defer modelVisibilityLock.RUnlock()

	allowed, restricted := modelVisibilityCache[modelName]
	if !restricted {
		return true
	}
	return allowed[userGroup]
}

// HasModelVisibilityRestrictions 报告全站是否存在任何可见性限制。
//
// 供各过滤点短路：绝大多数站点一条限制都没配，这时不该为每个模型做一次 map 查找
// 与切片重建。
func HasModelVisibilityRestrictions() bool {
	modelVisibilityLock.RLock()
	defer modelVisibilityLock.RUnlock()
	return len(modelVisibilityCache) > 0
}

// FilterModelsByVisibility 按可见性裁剪模型名列表，保持原有顺序。
func FilterModelsByVisibility(models []string, userGroup string) []string {
	if !HasModelVisibilityRestrictions() {
		return models
	}
	out := make([]string, 0, len(models))
	for _, m := range models {
		if IsModelVisibleForGroup(m, userGroup) {
			out = append(out, m)
		}
	}
	return out
}
