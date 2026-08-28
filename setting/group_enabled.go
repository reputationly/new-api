package setting

import (
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

// 分组停用态。设计见 docs/user-tier-pricing-and-topup-package-design.md §10.8。
//
// 解决的是一个真实的运营场景：活动分组（如 free）在活动结束后要留着配置等下次再开，
// 测试分组（如 bailian）暂停但不删。此前系统没有「停用」这个概念，这类**正常待命**
// 的分组在管理页会被健康判定标成 🟡 死配置，看起来像该清理的垃圾——本次盘点时
// 就差点被误删。
//
// 存储：只记被停用的分组名，未出现即启用。这样默认值是「全部启用」，
// 新建分组不需要额外配置，而且空配置时行为与改造前逐位相同。
var disabledGroups = map[string]bool{}
var disabledGroupsMutex sync.RWMutex

func GroupEnabled2JSONString() string {
	disabledGroupsMutex.RLock()
	defer disabledGroupsMutex.RUnlock()

	names := make([]string, 0, len(disabledGroups))
	for name := range disabledGroups {
		names = append(names, name)
	}
	jsonBytes, err := common.Marshal(names)
	if err != nil {
		common.SysLog("error marshalling disabled groups: " + err.Error())
		return "[]"
	}
	return string(jsonBytes)
}

func UpdateGroupEnabledByJSONString(jsonStr string) error {
	disabledGroupsMutex.Lock()
	defer disabledGroupsMutex.Unlock()

	disabledGroups = make(map[string]bool)
	if strings.TrimSpace(jsonStr) == "" {
		return nil
	}
	var names []string
	if err := common.Unmarshal([]byte(jsonStr), &names); err != nil {
		return err
	}
	for _, name := range names {
		if name = strings.TrimSpace(name); name != "" {
			disabledGroups[name] = true
		}
	}
	return nil
}

// IsGroupDisabled 报告分组是否处于停用状态。
func IsGroupDisabled(group string) bool {
	if group == "" {
		return false
	}
	disabledGroupsMutex.RLock()
	defer disabledGroupsMutex.RUnlock()
	return disabledGroups[group]
}

// GetDisabledGroupsCopy 返回停用分组的副本，供管理页渲染。
func GetDisabledGroupsCopy() []string {
	disabledGroupsMutex.RLock()
	defer disabledGroupsMutex.RUnlock()

	names := make([]string, 0, len(disabledGroups))
	for name := range disabledGroups {
		names = append(names, name)
	}
	return names
}
