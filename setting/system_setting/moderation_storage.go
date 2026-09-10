package system_setting

import (
	"os"

	"github.com/QuantumNous/new-api/setting/config"
)

// ModerationStorageSettings 审核取证（被拦下的图片/视频）独立 OBS 桶配置。
//
// 与媒体存储（media_storage）、用户素材（user_asset_storage）三者互相独立。
// 单独一个桶的理由有两条，都不是洁癖：
//
//  1. **生命周期规则冲突。** 主媒体桶上挂着 `prefix: ""` 的整桶 7 天规则
//     （见 docs/inbound-media-offload-design.md §4.7），而审核记录要留 180~360 天。
//     同桶时那批取证材料会在第 8 天被静默删掉——记录还在、图没了，
//     而签名是纯离线计算的，对已删对象照样签得出 URL，只会得到一个打不开的链接。
//  2. **权限可以单独收紧。** 这个桶里装的是违规内容，理应用一套只有管理端会用到的
//     凭证，而不是和正常业务产物共享同一个 AK。
//
// 未启用时回落主媒体存储桶（`moderation/` 前缀），行为与用户素材一致——
// 但那时上面第 1 条的风险就回来了，配置页会就此给出提示。
//
// 落 options 表（前缀 moderation_storage.），AK/SK 优先取环境变量
// MODERATION_OBS_AK/MODERATION_OBS_SK，否则取本结构体字段（加密入库，getter 解密）。
type ModerationStorageSettings struct {
	Enabled  bool   `json:"enabled"`
	Endpoint string `json:"endpoint"`
	Region   string `json:"region"`
	Bucket   string `json:"bucket"`

	// 凭证：加密入库（common.EncryptOBSSecret）；留空走环境变量。
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`

	// SignedURLTTLHours 签名 URL 有效期。
	//
	// 默认 1 小时，远短于其它两个桶的 7 天：这个链接指向的是违规内容，
	// 只在管理员点开「查看原图」的那一刻有用。签发即长期有效等于把取证材料
	// 变成一条可以随手转发的公开链接。
	SignedURLTTLHours int `json:"signed_url_ttl_hours"`

	// MaxObjectSizeMB 单个取证对象上限，超过直接不留存（判定结果不受影响）。
	MaxObjectSizeMB int `json:"max_object_size_mb"`
}

var moderationStorageSettings = ModerationStorageSettings{
	Enabled:           false,
	SignedURLTTLHours: 1,
	MaxObjectSizeMB:   200,
}

func init() {
	config.GlobalConfig.Register("moderation_storage", &moderationStorageSettings)
}

// GetModerationStorageSettings 返回全局单例（config manager 已按 DB 覆盖）。
func GetModerationStorageSettings() *ModerationStorageSettings {
	return &moderationStorageSettings
}

// GetAccessKeyID 优先环境变量 MODERATION_OBS_AK；否则解密入库字段。
func (s *ModerationStorageSettings) GetAccessKeyID() string {
	if v := os.Getenv("MODERATION_OBS_AK"); v != "" {
		return v
	}
	return decryptOrRaw(s.AccessKeyID)
}

// GetSecretAccessKey 优先环境变量 MODERATION_OBS_SK；否则解密入库字段。
func (s *ModerationStorageSettings) GetSecretAccessKey() string {
	if v := os.Getenv("MODERATION_OBS_SK"); v != "" {
		return v
	}
	return decryptOrRaw(s.SecretAccessKey)
}
