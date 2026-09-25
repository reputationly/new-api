package model

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// settings 里的写入型密钥：方舟素材库的 Secret Key。渠道 key 有「列表不返回、查看需安全验证」
// 的保护，SK 放在 settings 里若原样下发，任何能打开渠道列表的管理员都能看到，等于绕过了那道门。
// 所以对外只回一个「已配置」标记；更新时留空 = 不修改。
//
// 一律按通用 map 读写：settings 里可能有 dto.ChannelOtherSettings 没声明的键，
// 经结构体往返会被静默丢掉。
const (
	arkAssetAccessKeyField    = "ark_asset_access_key"
	arkAssetSecretKeyField    = "ark_asset_secret_key"
	arkAssetSecretKeySetField = "ark_asset_secret_key_set"
)

func parseSettingsMap(raw string) map[string]any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	m := map[string]any{}
	if err := common.UnmarshalJsonStr(raw, &m); err != nil {
		return nil
	}
	return m
}

func settingString(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

// RedactSecretSettings 去掉对外响应里的 SK，换成 ark_asset_secret_key_set 标记。
func (channel *Channel) RedactSecretSettings() {
	m := parseSettingsMap(channel.OtherSettings)
	if m == nil || settingString(m, arkAssetSecretKeyField) == "" {
		return
	}
	delete(m, arkAssetSecretKeyField)
	m[arkAssetSecretKeySetField] = true
	if b, err := common.Marshal(m); err == nil {
		channel.OtherSettings = string(b)
	}
}

// KeepSecretSettings 更新渠道时：请求里 SK 留空、AK 仍在，则沿用原渠道的 SK；
// AK 被清空则 SK 一并清掉。展示用的 *_set 标记不落库。
func (channel *Channel) KeepSecretSettings(origin *Channel) {
	m := parseSettingsMap(channel.OtherSettings)
	if m == nil {
		return
	}
	_, hadFlag := m[arkAssetSecretKeySetField]
	delete(m, arkAssetSecretKeySetField)
	changed := hadFlag
	if settingString(m, arkAssetAccessKeyField) == "" {
		if _, ok := m[arkAssetSecretKeyField]; ok {
			delete(m, arkAssetSecretKeyField)
			changed = true
		}
	} else if settingString(m, arkAssetSecretKeyField) == "" && origin != nil {
		if old := settingString(parseSettingsMap(origin.OtherSettings), arkAssetSecretKeyField); old != "" {
			m[arkAssetSecretKeyField] = old
			changed = true
		}
	}
	if !changed {
		return
	}
	if b, err := common.Marshal(m); err == nil {
		channel.OtherSettings = string(b)
	}
}
