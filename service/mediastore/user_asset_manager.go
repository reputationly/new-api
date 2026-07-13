package mediastore

import (
	"context"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/setting/system_setting"
)

// 用户素材(画布素材库)独立 OBS 桶的包级单例,与主媒体存储(manager.go)平行:
// 客户端按配置指纹惰性构建,系统设置页保存后自动重建。
// 未启用时调用方(controller/canvas_asset.go)回落主媒体存储桶。
var (
	userAssetMu          sync.Mutex
	userAssetCached      Store
	userAssetCachedPrint string
)

// UserAssetEnabled 用户素材独立桶开关。
func UserAssetEnabled() bool {
	return system_setting.GetUserAssetStorageSettings().Enabled
}

// UserAssetSignedURLTTL 用户素材签名 URL 有效期(默认 7d)。
func UserAssetSignedURLTTL() time.Duration {
	h := system_setting.GetUserAssetStorageSettings().SignedURLTTLHours
	if h <= 0 {
		h = 168
	}
	return time.Duration(h) * time.Hour
}

// userAssetConfig 从系统设置映射出 obsConfig。
// 用户素材只做内存字节上传/签名/删除,不涉及 NFS 搬运与上游 URL 下载,
// NFSRoot/AllowedURLHosts 留空。
func userAssetConfig() obsConfig {
	s := system_setting.GetUserAssetStorageSettings()
	return obsConfig{
		Endpoint:        s.Endpoint,
		Region:          s.Region,
		Bucket:          s.Bucket,
		AccessKeyID:     s.GetAccessKeyID(),
		SecretAccessKey: s.GetSecretAccessKey(),
		MaxObjectBytes:  int64(s.MaxObjectSizeMB) * 1024 * 1024,
	}
}

// userAssetStore 返回当前配置对应的 Store,按需(首次或配置变更)重建底层 S3 客户端。
func userAssetStore() (Store, error) {
	cfg := userAssetConfig()
	print := fingerprint(cfg)

	userAssetMu.Lock()
	defer userAssetMu.Unlock()
	if userAssetCached != nil && print == userAssetCachedPrint {
		return userAssetCached, nil
	}
	store, err := newOBSStore(cfg)
	if err != nil {
		return nil, err
	}
	userAssetCached = store
	userAssetCachedPrint = print
	return store, nil
}

// UserAssetPersist 上传素材字节到用户素材桶;开关关闭时返回 ErrNotEnabled。
func UserAssetPersist(ctx context.Context, key string, src PersistSource, meta map[string]string) error {
	if !UserAssetEnabled() {
		return ErrNotEnabled
	}
	store, err := userAssetStore()
	if err != nil {
		return err
	}
	return store.Persist(ctx, key, src, meta)
}

// UserAssetSign 用配置的 TTL 为用户素材桶中的 key 实时签名。
func UserAssetSign(ctx context.Context, key string, opts ...SignOption) (string, error) {
	store, err := userAssetStore()
	if err != nil {
		return "", err
	}
	return store.Sign(ctx, key, UserAssetSignedURLTTL(), opts...)
}

// UserAssetDelete 删除用户素材桶中的单个对象。
func UserAssetDelete(ctx context.Context, key string) error {
	store, err := userAssetStore()
	if err != nil {
		return err
	}
	return store.Delete(ctx, key)
}

// UserAssetHealthcheck 系统设置保存时校验用户素材桶连通性。
func UserAssetHealthcheck(ctx context.Context) error {
	store, err := userAssetStore()
	if err != nil {
		return err
	}
	return store.Healthcheck(ctx)
}
