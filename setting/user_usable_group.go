package setting

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
)

var userUsableGroups = map[string]string{
	"default": "默认分组",
	"vip":     "vip分组",
}
var userUsableGroupsMutex sync.RWMutex

func GetUserUsableGroupsCopy() map[string]string {
	userUsableGroupsMutex.RLock()
	defer userUsableGroupsMutex.RUnlock()

	copyUserUsableGroups := make(map[string]string)
	for k, v := range userUsableGroups {
		copyUserUsableGroups[k] = v
	}
	return copyUserUsableGroups
}

func UserUsableGroups2JSONString() string {
	userUsableGroupsMutex.RLock()
	defer userUsableGroupsMutex.RUnlock()

	jsonBytes, err := json.Marshal(userUsableGroups)
	if err != nil {
		common.SysLog("error marshalling user groups: " + err.Error())
	}
	return string(jsonBytes)
}

func UpdateUserUsableGroupsByJSONString(jsonStr string) error {
	userUsableGroupsMutex.Lock()
	defer userUsableGroupsMutex.Unlock()

	userUsableGroups = make(map[string]string)
	if strings.TrimSpace(jsonStr) == "" {
		return nil
	}
	return json.Unmarshal([]byte(jsonStr), &userUsableGroups)
}

func GetUsableGroupDescription(groupName string) string {
	userUsableGroupsMutex.RLock()
	defer userUsableGroupsMutex.RUnlock()

	if desc, ok := userUsableGroups[groupName]; ok {
		return desc
	}
	return groupName
}

// groupDescriptions 独立存储分组描述，不依赖分组是否勾选「用户可选」。
// 兼容历史数据：已勾选用户可选的分组，描述曾经唯一存在 userUsableGroups 里，
// 这里没有值时要 fallback 过去，否则老数据在这次改造后会读出空值。
var groupDescriptions = map[string]string{}
var groupDescriptionsMutex sync.RWMutex

func GroupDescriptions2JSONString() string {
	groupDescriptionsMutex.RLock()
	defer groupDescriptionsMutex.RUnlock()

	jsonBytes, err := common.Marshal(groupDescriptions)
	if err != nil {
		common.SysLog("error marshalling group descriptions: " + err.Error())
	}
	return string(jsonBytes)
}

func UpdateGroupDescriptionsByJSONString(jsonStr string) error {
	groupDescriptionsMutex.Lock()
	defer groupDescriptionsMutex.Unlock()

	groupDescriptions = make(map[string]string)
	if strings.TrimSpace(jsonStr) == "" {
		return nil
	}
	return common.Unmarshal([]byte(jsonStr), &groupDescriptions)
}

// GetGroupDescription 返回分组描述：优先取独立存储，没有则回退到用户可选分组表里
// 历史遗留的描述值，最后兜底为分组名本身。
func GetGroupDescription(groupName string) string {
	groupDescriptionsMutex.RLock()
	if desc, ok := groupDescriptions[groupName]; ok {
		groupDescriptionsMutex.RUnlock()
		return desc
	}
	groupDescriptionsMutex.RUnlock()

	return GetUsableGroupDescription(groupName)
}
