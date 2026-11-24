package model

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

type OptionAudit struct {
	ActorID   int
	Username  string
	Role      int
	IP        string
	RequestID string
	Action    string
	Summary   string
}

type optionAuditChange struct {
	Key          string   `json:"key"`
	Path         []string `json:"path,omitempty"`
	Before       any      `json:"before"`
	After        any      `json:"after"`
	BeforeExists bool     `json:"before_exists"`
	AfterExists  bool     `json:"after_exists"`
}

func (audit OptionAudit) log(before, after map[string]string) (*Log, error) {
	const maxChanges = 100
	const maxDiffBytes = 24 * 1024
	changes := make([]optionAuditChange, 0)
	total, size := 0, 0
	add := func(change optionAuditChange) {
		total++
		if len(changes) >= maxChanges {
			return
		}
		data, err := common.Marshal(change)
		if err != nil || size+len(data) > maxDiffBytes {
			return
		}
		size += len(data)
		changes = append(changes, change)
	}
	keys := make([]string, 0, len(after))
	for key := range after {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		oldRaw, oldExists := before[key]
		newRaw, newExists := after[key]
		depth := 1
		switch key {
		case "GroupTimeRatio":
			depth = 3
		case "GroupModelRatio", "UserGroupModelRatio", "GroupGroupRatio",
			"group_ratio_setting.group_special_usable_group":
			depth = 2
		}
		diffOption(key, nil, optionAuditValue(oldRaw, oldExists), optionAuditValue(newRaw, newExists), oldExists, newExists, depth, add)
	}
	if total == 0 {
		return nil, nil
	}
	other, err := common.Marshal(map[string]any{"admin_info": map[string]any{
		"action": audit.Action, "admin_id": audit.ActorID, "admin_username": audit.Username,
		"admin_role": audit.Role, "operator_ip": audit.IP, "changes": changes,
		"total_changes": total, "truncated": total > len(changes),
	}})
	if err != nil {
		return nil, err
	}
	return &Log{UserId: audit.ActorID, Username: audit.Username, CreatedAt: common.GetTimestamp(),
		Type: LogTypeManage, Content: fmt.Sprintf("%s（%d 项变更）", audit.Summary, total),
		Other: string(other), Ip: audit.IP, RequestId: audit.RequestID}, nil
}

func optionAuditValue(raw string, exists bool) any {
	if !exists {
		return nil
	}
	var v any
	if err := common.UnmarshalJsonStr(raw, &v); err != nil {
		return raw
	}
	return v
}

func diffOption(key string, path []string, before, after any, beforeExists, afterExists bool, depth int, add func(optionAuditChange)) {
	if beforeExists == afterExists && reflect.DeepEqual(before, after) {
		return
	}
	oldMap, oldObject := before.(map[string]any)
	newMap, newObject := after.(map[string]any)
	if depth > 0 && (oldObject || !beforeExists) && (newObject || !afterExists) && len(oldMap)+len(newMap) > 0 {
		keys := make([]string, 0, len(oldMap)+len(newMap))
		for name := range oldMap {
			keys = append(keys, name)
		}
		for name := range newMap {
			if _, exists := oldMap[name]; !exists {
				keys = append(keys, name)
			}
		}
		slices.Sort(keys)
		for _, name := range keys {
			a, aExists := oldMap[name]
			b, bExists := newMap[name]
			diffOption(key, append(slices.Clone(path), name), a, b, aExists, bExists, depth-1, add)
		}
		return
	}
	add(optionAuditChange{Key: key, Path: path, Before: before, After: after, BeforeExists: beforeExists, AfterExists: afterExists})
}

func validateGroupOrToolOption(key, value string) error {
	var parsed any
	switch key {
	case "GroupRatio":
		return ratio_setting.CheckGroupRatio(value)
	case "GroupModelRatio":
		return ratio_setting.CheckGroupModelRatio(value)
	case "UserGroupModelRatio":
		return ratio_setting.CheckUserGroupModelRatio(value)
	case "ModelRequestRateLimitGroup":
		return setting.CheckModelRequestRateLimitGroup(value)
	case "GroupGroupRatio":
		parsed = new(map[string]map[string]float64)
	case "group_ratio_setting.group_special_usable_group":
		parsed = new(map[string]map[string]string)
	case "AutoGroups", "GroupEnabled", "points_setting.enabled_groups":
		parsed = new([]string)
	case "UserUsableGroups", "GroupDescription":
		parsed = new(map[string]string)
	case "TopupGroupRatio", "tool_price_setting.prices":
		parsed = new(map[string]float64)
	case "DefaultUseAutoGroup":
		if _, err := strconv.ParseBool(value); err != nil || (value != "true" && value != "false") {
			return fmt.Errorf("%s 必须是 true 或 false", key)
		}
		return nil
	default:
		return fmt.Errorf("该接口不支持批量写入：%s", key)
	}
	if err := common.UnmarshalJsonStr(value, parsed); err != nil {
		return fmt.Errorf("%s 格式不正确: %w", key, err)
	}
	return nil
}
