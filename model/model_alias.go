package model

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
)

// 模型别名：对外名 -> 内部名（真实上游名）。
//
// 为什么需要它：model_mapping 只回答「发给上游时换成哪个名字」，它没有「这个名字
// 不该被看见」的概念。于是运营想做「上游叫 gpt-4o、对外卖 my-gpt-4o」时，只能把
// gpt-4o 也塞进渠道的「模型」列表（否则没有 ability 根本路由不到），结果 gpt-4o
// 出现在模型广场、/v1/models、体验区和令牌的模型选择里 —— 等于把上游真实模型名
// 直接贴在价目表上。
//
// 本功能把这件事拆成两个名字、两套约束：
//
//	对外名 b（渠道「模型」列表里唯一该出现的名字）：展示、计费、日志、响应体。
//	内部名 a（别名的值）：只出现在发往上游的请求里，其余任何用户可达的出口都不出现。
//
// 与 model_mapping 是**两段式**关系：先在别名里把 b 换成 a，再让 mapping 在 a 上
// 继续链式求值（渠道适配：Azure 部署名、Gemini 的 -thinking 变体等）。
//
// 内部名还有第二条约束：它不是一个可路由的模型名。abilities 由渠道「模型」列表生成，
// 所以只要内部名不在列表里就自然不可路由；万一运营把它留在列表里（历史配置），
// 构建 ability 时也会被这里剔除（见 ability.go 的 AddAbilities / UpdateAbilities）。

// ParseModelAlias 解析别名 JSON。返回 nil map + nil error 表示没有配置。
//
// 两侧都 trim；丢弃空值、左右相同的自映射 —— 这类项留着只会让链式求值多绕一圈。
// 解析失败时把错误交给调用方决定怎么处理：请求路径上应当像 model_mapping 那样直接
// 报错（配置坏了不能猜），后台重建缓存时则降级成「没有别名」。
func ParseModelAlias(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "{}" {
		return nil, nil
	}
	parsed := make(map[string]string)
	if err := common.UnmarshalJsonStr(raw, &parsed); err != nil {
		return nil, err
	}
	normalized := make(map[string]string, len(parsed))
	for publicName, internalName := range parsed {
		publicName = strings.TrimSpace(publicName)
		internalName = strings.TrimSpace(internalName)
		if publicName == "" || internalName == "" || publicName == internalName {
			continue
		}
		normalized[publicName] = internalName
	}
	if len(normalized) == 0 {
		return nil, nil
	}
	return normalized, nil
}

// GetModelAliasMap 返回该渠道的别名映射；配置损坏时按「没有别名」降级。
//
// 这里**不记日志**：它会在每次缓存重建（60s 一次 + 每次渠道写）里被逐渠道调用，
// 坏配置会刷屏。日志由 InitModelAliasCache 每次重建记一条（见那里）。
func (channel *Channel) GetModelAliasMap() map[string]string {
	if channel == nil {
		return nil
	}
	parsed, err := ParseModelAlias(channel.GetModelAlias())
	if err != nil {
		return nil
	}
	return parsed
}

// modelAliasHidden 是站点级的内部名集合，modelAliasInternalToPublic 记录内部名
// 唯一对应的对外名（存在多个对外名时视为冲突，不参与兼容转发）。
//
// 为什么隐藏是**站点级**而不是渠道级：一个模型名在价目表和 /v1/models 里要么出现、
// 要么不出现，没有「对渠道 A 可见、对渠道 B 不可见」这种中间状态。所以别名虽然配在
// 渠道上，隐藏语义必须按名字全站生效。
var (
	modelAliasLock             sync.RWMutex
	modelAliasHidden           = map[string]struct{}{}
	modelAliasInternalToPublic = map[string]string{}
)

// InitModelAliasCache 全量重建别名缓存，并返回隐藏名集合相对上一次的变化。
//
// 返回值给渠道写操作使用：这些名字可能还留在某些渠道的「模型」列表里，
// 那份列表对应的 abilities 必须重建，否则旧名字仍然可路由、仍然出现在列表里。
// 周期同步只刷新缓存，不重建 abilities（见 SyncModelAliasCache）。
func InitModelAliasCache() []string {
	var channels []*Channel
	// settings 也要读：是否允许"内部名仍可调用"是渠道级开关（见 modelAliasKeepInternal）。
	if err := DB.Select("id", "models", "model_alias", "settings").Find(&channels).Error; err != nil {
		common.SysError("failed to load model aliases: " + err.Error())
		return nil
	}

	// 内部名 -> 它被映射到的对外名集合。
	//
	// 三件事要分开看：
	//   hidden     —— 只要被任何渠道声明成内部名就隐藏（与是否可调用无关）；
	//   compat     —— 只有开了"内部名仍可调用"的渠道才可能参与兼容转发；
	//   conflict   —— 站点内同一个内部名被映射到多个对外名时，"用内部名调用该回显
	//                 谁"没有答案，此时停止兼容转发（仍然隐藏）。冲突判定要看**全部**
	//                 渠道，不能只看开了开关的那些：否则另一条渠道的另一种对外语义
	//                 会被悄悄忽略，请求会按一个运营没预期到的对外名计费。
	publicsByInternal := make(map[string]map[string]struct{})
	compatInternal := make(map[string]struct{})
	for _, channel := range channels {
		aliasMap, err := ParseModelAlias(channel.GetModelAlias())
		if err != nil {
			// 每次重建最多一条，够运营发现配置坏了；请求路径上会直接报错，
			// 不会静默按"没有别名"跑（见 relay/helper/model_mapped.go）。
			common.SysLog(fmt.Sprintf("channel %d model_alias 配置无效，已忽略: %v", channel.Id, err))
			continue
		}
		keepInternal := modelAliasKeepInternal(channel.OtherSettings)
		for publicName, internalName := range aliasMap {
			set, ok := publicsByInternal[internalName]
			if !ok {
				set = make(map[string]struct{})
				publicsByInternal[internalName] = set
			}
			set[publicName] = struct{}{}
			if keepInternal {
				compatInternal[internalName] = struct{}{}
			}
		}
	}

	freshHidden := make(map[string]struct{}, len(publicsByInternal))
	freshMapping := make(map[string]string, len(publicsByInternal))
	for internalName, publics := range publicsByInternal {
		freshHidden[internalName] = struct{}{}
		if len(publics) > 1 {
			names := make([]string, 0, len(publics))
			for publicName := range publics {
				names = append(names, publicName)
			}
			sort.Strings(names)
			common.SysLog(fmt.Sprintf(
				"model_alias 冲突：内部名 %q 被映射到了多个对外名 %v，已停止用内部名转发（对外名不受影响）",
				internalName, names))
			continue
		}
		if _, ok := compatInternal[internalName]; !ok {
			continue
		}
		for publicName := range publics {
			freshMapping[internalName] = publicName
		}
	}

	modelAliasLock.Lock()
	changed := diffNameSets(modelAliasHidden, freshHidden)
	modelAliasHidden = freshHidden
	modelAliasInternalToPublic = freshMapping
	modelAliasLock.Unlock()
	return changed
}

// SyncModelAliasCache 周期性重建，供多节点部署感知其他节点的改动。
//
// 与 InitChannelCache 同频即可：两者都在等「别的节点改了库」。
// 这里**不做** abilities 重建 —— abilities 存在库里、是共享的，重建只该由写操作触发，
// 否则多节点会同时重建同一批渠道，删除与插入交错时会出现 abilities 短暂缺失。
func SyncModelAliasCache(frequency int) {
	if frequency <= 0 {
		return
	}
	for {
		time.Sleep(time.Duration(frequency) * time.Second)
		InitModelAliasCache()
	}
}

// IsHiddenModelName 报告某模型名是否为内部名（别名右侧），即不该对用户出现。
//
// fail-open：缓存为空（还没建好 / 查询失败）时一律返回 false。隐藏是「运营希望用户
// 看不到」的诉求，一次缓存加载失败就让全站模型集体消失，代价远大于漏隐身一次。
func IsHiddenModelName(name string) bool {
	if name == "" {
		return false
	}
	modelAliasLock.RLock()
	defer modelAliasLock.RUnlock()
	_, ok := modelAliasHidden[name]
	return ok
}

// HasHiddenModelNames 供各过滤点短路：绝大多数站点一条别名都没配。
func HasHiddenModelNames() bool {
	modelAliasLock.RLock()
	defer modelAliasLock.RUnlock()
	return len(modelAliasHidden) > 0
}

// FilterHiddenModelNames 从模型名列表里剔除内部名，保持原有顺序。
func FilterHiddenModelNames(models []string) []string {
	if len(models) == 0 || !HasHiddenModelNames() {
		return models
	}
	out := make([]string, 0, len(models))
	for _, name := range models {
		if !IsHiddenModelName(name) {
			out = append(out, name)
		}
	}
	return out
}

// ResolveHiddenModelName 把内部名解析成它唯一的对外名。
//
// 只用于「允许内部名仍可调用」的兼容渠道：请求方用内部名发过来时，按对外名计费、
// 记账、回显。返回 false 表示这个内部名没有唯一的对外名（冲突或未配置），
// 此时调用方应当按「模型不存在」处理，不要猜。
func ResolveHiddenModelName(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	modelAliasLock.RLock()
	defer modelAliasLock.RUnlock()
	publicName, ok := modelAliasInternalToPublic[name]
	if !ok || publicName == "" {
		return "", false
	}
	return publicName, true
}

// ReconcileHiddenModelAbilities 重建「模型列表里含这些名字」的渠道的 abilities。
//
// 只重建受影响的渠道，不做全表 FixAbility：别名只在运营改配置时才变，
// 而一次全表重建在大站点上足够慢，不该塞进一次普通的渠道保存请求里。
func ReconcileHiddenModelAbilities(changedNames []string) {
	if len(changedNames) == 0 {
		return
	}
	changed := make(map[string]struct{}, len(changedNames))
	for _, name := range changedNames {
		if name = strings.TrimSpace(name); name != "" {
			changed[name] = struct{}{}
		}
	}
	if len(changed) == 0 {
		return
	}
	var channels []*Channel
	if err := DB.Select("id", "models", "group", "status", "priority", "weight", "tag", "model_alias").Find(&channels).Error; err != nil {
		common.SysError("failed to load channels for model_alias reconcile: " + err.Error())
		return
	}
	for _, channel := range channels {
		affected := false
		for _, name := range channel.GetModels() {
			if _, ok := changed[name]; ok {
				affected = true
				break
			}
		}
		if !affected {
			continue
		}
		if err := channel.UpdateAbilities(nil); err != nil {
			common.SysLog(fmt.Sprintf("failed to rebuild abilities after model_alias change: channel_id=%d, error=%v", channel.Id, err))
		}
	}
}

// refreshModelAliasCache 供渠道写操作调用：刷新缓存，必要时重建受影响渠道的 abilities。
func refreshModelAliasCache() {
	changed := InitModelAliasCache()
	if len(changed) == 0 {
		return
	}
	ReconcileHiddenModelAbilities(changed)
	// 模型广场是一分钟级的缓存。别名一改就得立刻失效，否则运营配完还会看到
	// "内部名仍在价目表上"，只能等下一轮刷新 —— 这类"看起来没生效"最容易引发误操作。
	InvalidatePricingCache()
}

// modelAliasKeepInternal 读渠道「其他设置」里的同名开关。
//
// 刻意不用 Channel.GetOtherSettings()：那个访问器在 JSON 解析失败时会**回写数据库**，
// 而这里是在全表扫描里逐行调用（每次缓存重建都跑一遍），不该带副作用。
func modelAliasKeepInternal(rawSettings string) bool {
	if strings.TrimSpace(rawSettings) == "" {
		return false
	}
	var settings dto.ChannelOtherSettings
	if err := common.UnmarshalJsonStr(rawSettings, &settings); err != nil {
		return false
	}
	return settings.ModelAliasKeepInternal
}

// diffNameSets 返回两个集合的对称差（排好序，便于日志与测试）。
func diffNameSets(oldSet, newSet map[string]struct{}) []string {
	diff := make([]string, 0)
	for name := range newSet {
		if _, ok := oldSet[name]; !ok {
			diff = append(diff, name)
		}
	}
	for name := range oldSet {
		if _, ok := newSet[name]; !ok {
			diff = append(diff, name)
		}
	}
	if len(diff) == 0 {
		return nil
	}
	sort.Strings(diff)
	return diff
}
