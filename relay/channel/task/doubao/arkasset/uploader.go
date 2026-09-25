package arkasset

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/cachex"

	"github.com/samber/hot"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
)

// assetIDTTL 同一素材 URL 复用已入库素材的时长。素材本身在素材库里长期有效，TTL 只是
// 兜住「素材在控制台被手动删掉」这种情况，过期后重新入库一次即可。
const assetIDTTL = 24 * time.Hour

const defaultPollInterval = time.Second

// Item 一个待入库的媒体。Type 为 AssetTypeImage / AssetTypeVideo。
type Item struct {
	URL  string
	Type string
}

// FailedError 素材预处理失败（Status=Failed）。是输入本身的问题，换渠道重试也一样。
type FailedError struct {
	URL     string
	Code    string
	Message string
}

func (e *FailedError) Error() string {
	return fmt.Sprintf("ark asset processing failed: %s %s", e.Code, e.Message)
}

type assetAPI interface {
	FindGroup(ctx context.Context, name string) (string, error)
	CreateGroup(ctx context.Context, name, description string) (string, error)
	CreateAsset(ctx context.Context, groupID, url, assetType, name string) (string, error)
	GetAsset(ctx context.Context, id string) (*Asset, error)
}

type idCache interface {
	Get(key string) (string, bool, error)
	SetWithTTL(key string, v string, ttl time.Duration) error
}

type Uploader struct {
	api          assetAPI
	cache        idCache
	scope        string // 缓存隔离：同一账号 + 项目下的素材 ID 才能互相复用
	groupName    string
	pollInterval time.Duration
}

// NewUploader scope 标识素材归属的账号与项目；groupName 是自动查找/创建的素材组名。
func NewUploader(api assetAPI, scope, groupName string) *Uploader {
	return &Uploader{api: api, cache: assetIDCache(), scope: scope, groupName: groupName, pollInterval: defaultPollInterval}
}

// Scope 由素材库地址、项目与凭据身份拼出缓存隔离键。凭据只取摘要，不落明文。
func Scope(cfg Config) string {
	identity := cfg.AccessKey
	if identity == "" {
		identity = "bearer:" + digest(cfg.APIKey)[:16]
	}
	return cfg.Endpoint + "|" + cfg.ProjectName + "|" + identity
}

// Resolve 把 items 去重后并发入库并等到 Active，返回 URL → 素材 ID。任一失败即整体失败
// （其余在途的会被取消）：少一张参考图生成出来的视频不是客户要的。
func (u *Uploader) Resolve(ctx context.Context, items []Item) (map[string]string, error) {
	out := make(map[string]string, len(items))
	var mu sync.Mutex
	g, gctx := errgroup.WithContext(ctx)
	seen := make(map[string]bool, len(items))
	for _, it := range items {
		if seen[it.URL] {
			continue
		}
		seen[it.URL] = true
		g.Go(func() error {
			id, err := u.resolveOne(gctx, it)
			if err != nil {
				return err
			}
			mu.Lock()
			out[it.URL] = id
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

func (u *Uploader) resolveOne(ctx context.Context, it Item) (string, error) {
	cacheKey := digest(u.scope + "|" + it.URL)
	if id, ok, err := u.cache.Get(cacheKey); err == nil && ok && id != "" {
		return id, nil
	}

	groupID, err := u.ensureGroup(ctx)
	if err != nil {
		return "", err
	}
	id, err := u.api.CreateAsset(ctx, groupID, it.URL, it.Type, "newapi-"+cacheKey[:16])
	if err != nil {
		// 素材组可能在控制台被删了：清掉缓存，下个请求重新查找/创建。
		groupIDs.Delete(u.groupKey())
		return "", err
	}
	if err := u.waitActive(ctx, id, it.URL); err != nil {
		return "", err
	}
	if err := u.cache.SetWithTTL(cacheKey, id, assetIDTTL); err != nil {
		common.SysLog("ark asset: cache asset id failed: " + err.Error())
	}
	return id, nil
}

func (u *Uploader) waitActive(ctx context.Context, id, url string) error {
	ticker := time.NewTicker(u.pollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("waiting for ark asset %s to become active: %w", id, ctx.Err())
		case <-ticker.C:
		}
		asset, err := u.api.GetAsset(ctx, id)
		if err != nil {
			return err
		}
		switch asset.Status {
		case StatusActive:
			return nil
		case StatusFailed:
			return &FailedError{URL: url, Code: asset.Error.Code, Message: asset.Error.Message}
		}
	}
}

// 素材组 ID 进程内缓存。组是长期存在的，重启后按名称查回来即可，不需要落库。
var (
	groupIDs    sync.Map
	groupFlight singleflight.Group
)

func (u *Uploader) groupKey() string {
	return u.scope + "|" + u.groupName
}

func (u *Uploader) ensureGroup(ctx context.Context) (string, error) {
	key := u.groupKey()
	if v, ok := groupIDs.Load(key); ok {
		return v.(string), nil
	}
	v, err, _ := groupFlight.Do(key, func() (any, error) {
		id, err := u.api.FindGroup(ctx, u.groupName)
		if err != nil {
			return "", err
		}
		if id == "" {
			if id, err = u.api.CreateGroup(ctx, u.groupName, "new-api 自动创建：客户上传的参考图与参考视频"); err != nil {
				return "", err
			}
		}
		if id == "" {
			return "", errors.New("ark asset: empty asset group id")
		}
		groupIDs.Store(key, id)
		return id, nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}

var (
	assetCacheOnce sync.Once
	assetCache     *cachex.HybridCache[string]
)

func assetIDCache() idCache {
	assetCacheOnce.Do(func() {
		assetCache = cachex.NewHybridCache[string](cachex.HybridCacheConfig[string]{
			Namespace:    cachex.Namespace("ark_asset_id"),
			Redis:        common.RDB,
			RedisEnabled: func() bool { return common.RedisEnabled && common.RDB != nil },
			RedisCodec:   cachex.StringCodec{},
			Memory: func() *hot.HotCache[string, string] {
				return hot.NewHotCache[string, string](hot.LRU, 10_000).
					WithTTL(assetIDTTL).
					WithJanitor().
					Build()
			},
		})
	})
	return assetCache
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
