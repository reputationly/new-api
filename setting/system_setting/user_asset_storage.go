package system_setting

import (
	"os"

	"github.com/QuantumNous/new-api/setting/config"
)

// UserAssetStorageSettings 用户素材(画布素材库)独立 OBS 桶配置。
// 与媒体存储(media_storage)互相独立:媒体存储承接生图/生视频结果落盘,
// 本配置承接用户主动上传的素材,可绑定不同的桶与桶规则(生命周期/配额等)。
// 未启用时素材库回落到媒体存储桶(存量素材也保留在媒体存储桶,不迁移)。
// 落 options 表(前缀 user_asset_storage.),AK/SK 优先取环境变量
// USER_ASSET_OBS_AK/USER_ASSET_OBS_SK,否则取本结构体字段(加密入库,getter 解密)。
type UserAssetStorageSettings struct {
	Enabled  bool   `json:"enabled"`
	Endpoint string `json:"endpoint"`
	Region   string `json:"region"`
	Bucket   string `json:"bucket"`

	// 凭证:加密入库(common.EncryptOBSSecret);留空走环境变量。
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`

	SignedURLTTLHours int `json:"signed_url_ttl_hours"` // 签名 URL TTL,默认 7d(168h)
	MaxObjectSizeMB   int `json:"max_object_size_mb"`   // 单素材硬上限,超过直接拒绝
}

var userAssetStorageSettings = UserAssetStorageSettings{
	Enabled:           false,
	SignedURLTTLHours: 168, // 7d
	MaxObjectSizeMB:   200,
}

func init() {
	config.GlobalConfig.Register("user_asset_storage", &userAssetStorageSettings)
}

// GetUserAssetStorageSettings 返回全局单例(config manager 已按 DB 覆盖)。
func GetUserAssetStorageSettings() *UserAssetStorageSettings {
	return &userAssetStorageSettings
}

// GetAccessKeyID 优先环境变量 USER_ASSET_OBS_AK;否则解密入库字段。
func (s *UserAssetStorageSettings) GetAccessKeyID() string {
	if v := os.Getenv("USER_ASSET_OBS_AK"); v != "" {
		return v
	}
	return decryptOrRaw(s.AccessKeyID)
}

// GetSecretAccessKey 优先环境变量 USER_ASSET_OBS_SK;否则解密入库字段。
func (s *UserAssetStorageSettings) GetSecretAccessKey() string {
	if v := os.Getenv("USER_ASSET_OBS_SK"); v != "" {
		return v
	}
	return decryptOrRaw(s.SecretAccessKey)
}
