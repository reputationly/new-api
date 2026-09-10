package mediastore

import (
	"context"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/setting/system_setting"
)

// 审核取证独立 OBS 桶的包级单例，与主媒体存储（manager.go）、用户素材
// （user_asset_manager.go）三者平行：客户端按配置指纹惰性构建，系统设置页保存后自动重建。
//
// 未启用时调用方（service/moderation）回落主媒体存储桶的 `moderation/` 前缀。
var (
	moderationStoreMu    sync.Mutex
	moderationCached     Store
	moderationCachedName string
)

// ModerationStorageEnabled 审核取证独立桶开关。
func ModerationStorageEnabled() bool {
	return system_setting.GetModerationStorageSettings().Enabled
}

// ModerationSignedURLTTL 取证链接的有效期（默认 1 小时）。
//
// 刻意比另外两个桶短得多：它指向的是违规内容，只在管理员点开的那一刻有用。
// 长期有效等于把取证材料变成一条可随手转发的链接。
func ModerationSignedURLTTL() time.Duration {
	h := system_setting.GetModerationStorageSettings().SignedURLTTLHours
	if h <= 0 {
		h = 1
	}
	return time.Duration(h) * time.Hour
}

// moderationConfig 从系统设置映射出 obsConfig。
//
// 取证只做内存字节上传 / 签名 / 删除，不涉及 NFS 搬运；
// 上游 URL 下载由调用方先取回字节，所以 NFSRoot / AllowedURLHosts 留空。
func moderationConfig() obsConfig {
	s := system_setting.GetModerationStorageSettings()
	return obsConfig{
		Endpoint:        s.Endpoint,
		Region:          s.Region,
		Bucket:          s.Bucket,
		AccessKeyID:     s.GetAccessKeyID(),
		SecretAccessKey: s.GetSecretAccessKey(),
		MaxObjectBytes:  int64(s.MaxObjectSizeMB) * 1024 * 1024,
	}
}

// moderationStore 返回当前配置对应的 Store，按需（首次或配置变更）重建底层 S3 客户端。
func moderationStore() (Store, error) {
	cfg := moderationConfig()
	print := fingerprint(cfg)

	moderationStoreMu.Lock()
	defer moderationStoreMu.Unlock()
	if moderationCached != nil && print == moderationCachedName {
		return moderationCached, nil
	}
	store, err := newOBSStore(cfg)
	if err != nil {
		return nil, err
	}
	moderationCached = store
	moderationCachedName = print
	return store, nil
}

// ModerationPersist 上传取证字节；开关关闭时返回 ErrNotEnabled。
func ModerationPersist(ctx context.Context, key string, src PersistSource, meta map[string]string) error {
	if !ModerationStorageEnabled() {
		return ErrNotEnabled
	}
	store, err := moderationStore()
	if err != nil {
		return err
	}
	return store.Persist(ctx, key, src, meta)
}

// ModerationSign 用配置的 TTL 为取证桶中的 key 实时签名。
func ModerationSign(ctx context.Context, key string, opts ...SignOption) (string, error) {
	store, err := moderationStore()
	if err != nil {
		return "", err
	}
	return store.Sign(ctx, key, ModerationSignedURLTTL(), opts...)
}

// ModerationExists 判断取证对象是否存在（HeadObject）。
//
// 取证记录里的 key 是**上传前**就写好的（让记录能立刻落库，不必等网络 IO），
// 所以 key 存在不代表对象传上去了。签名是纯离线计算，对不存在的对象照样签得出
// URL——不先确认一次，管理员点开拿到的是一个无从解释的 403。
func ModerationExists(ctx context.Context, key string) (bool, error) {
	if !ModerationStorageEnabled() {
		return false, ErrNotEnabled
	}
	store, err := moderationStore()
	if err != nil {
		return false, err
	}
	return store.Exists(ctx, key)
}

// ModerationDelete 删除取证桶中的单个对象。
//
// 审核记录清理（model.CleanupModerationLogs）会顺带调它：取证对象的生命周期
// 必须跟着记录走，而不是靠桶级规则各管各的——两边的天数一旦对不上，
// 要么记录还在图没了，要么图留着却再也没人能通过记录找到它。
func ModerationDelete(ctx context.Context, key string) error {
	store, err := moderationStore()
	if err != nil {
		return err
	}
	return store.Delete(ctx, key)
}

// ModerationHealthcheck 系统设置保存时校验取证桶连通性。
func ModerationHealthcheck(ctx context.Context) error {
	store, err := moderationStore()
	if err != nil {
		return err
	}
	return store.Healthcheck(ctx)
}
