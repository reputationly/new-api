package arkasset

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type fakeAPI struct {
	mu            sync.Mutex
	existingGroup string
	createdGroups int32
	createdAssets int32
	// statusFor 决定每个 URL 入库后轮询到的终态，缺省 Active。
	statusFor map[string]string
	assetURL  map[string]string
	pollsLeft map[string]int
}

func newFakeAPI() *fakeAPI {
	return &fakeAPI{statusFor: map[string]string{}, assetURL: map[string]string{}, pollsLeft: map[string]int{}}
}

func (f *fakeAPI) FindGroup(context.Context, string) (string, error) { return f.existingGroup, nil }

func (f *fakeAPI) CreateGroup(context.Context, string, string) (string, error) {
	atomic.AddInt32(&f.createdGroups, 1)
	return "group-new", nil
}

func (f *fakeAPI) CreateAsset(_ context.Context, groupID, url, _, _ string) (string, error) {
	n := atomic.AddInt32(&f.createdAssets, 1)
	id := "asset-" + string(rune('a'+n-1))
	f.mu.Lock()
	f.assetURL[id] = url
	f.pollsLeft[id] = 2 // 先轮询到两次 Processing，模拟异步预处理
	f.mu.Unlock()
	return id, nil
}

func (f *fakeAPI) GetAsset(_ context.Context, id string) (*Asset, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pollsLeft[id] > 0 {
		f.pollsLeft[id]--
		return &Asset{ID: id, Status: StatusProcessing}, nil
	}
	a := &Asset{ID: id, Status: StatusActive}
	if s, ok := f.statusFor[f.assetURL[id]]; ok {
		a.Status = s
		a.Error.Code = "InputImageInvalid"
		a.Error.Message = "bad image"
	}
	return a, nil
}

type mapCache struct {
	mu sync.Mutex
	m  map[string]string
}

func (c *mapCache) Get(k string) (string, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[k]
	return v, ok, nil
}

func (c *mapCache) SetWithTTL(k, v string, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.m[k] = v
	return nil
}

func testUploader(t *testing.T, api assetAPI) *Uploader {
	// 每个用例独立 scope，互不共享进程级的素材组缓存。
	u := &Uploader{api: api, cache: &mapCache{m: map[string]string{}}, scope: t.Name(), groupName: "g", pollInterval: time.Millisecond}
	return u
}

func TestResolveDedupesUploadsConcurrentlyAndCreatesGroupOnce(t *testing.T) {
	api := newFakeAPI()
	u := testUploader(t, api)

	got, err := u.Resolve(context.Background(), []Item{
		{URL: "https://x/a.jpg", Type: AssetTypeImage},
		{URL: "https://x/v.mp4", Type: AssetTypeVideo},
		{URL: "https://x/a.jpg", Type: AssetTypeImage},
	})
	require.NoError(t, err)
	require.Len(t, got, 2)
	require.NotEqual(t, got["https://x/a.jpg"], got["https://x/v.mp4"])
	require.EqualValues(t, 2, api.createdAssets, "同一 URL 只入库一次")
	require.EqualValues(t, 1, api.createdGroups, "并发下素材组只建一次")
}

func TestResolveReusesCachedAssetAndExistingGroup(t *testing.T) {
	api := newFakeAPI()
	api.existingGroup = "group-existing"
	u := testUploader(t, api)

	first, err := u.Resolve(context.Background(), []Item{{URL: "https://x/a.jpg", Type: AssetTypeImage}})
	require.NoError(t, err)
	second, err := u.Resolve(context.Background(), []Item{{URL: "https://x/a.jpg", Type: AssetTypeImage}})
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.EqualValues(t, 1, api.createdAssets, "第二次命中缓存，不再入库")
	require.EqualValues(t, 0, api.createdGroups, "同名素材组已存在就复用")
}

func TestResolveSurfacesProcessingFailure(t *testing.T) {
	api := newFakeAPI()
	api.statusFor["https://x/bad.jpg"] = StatusFailed
	u := testUploader(t, api)

	_, err := u.Resolve(context.Background(), []Item{
		{URL: "https://x/ok.jpg", Type: AssetTypeImage},
		{URL: "https://x/bad.jpg", Type: AssetTypeImage},
	})
	var failed *FailedError
	require.True(t, errors.As(err, &failed))
	require.Equal(t, "https://x/bad.jpg", failed.URL)
	require.Equal(t, "InputImageInvalid", failed.Code)
}

func TestResolveTimesOutWhileProcessing(t *testing.T) {
	api := newFakeAPI()
	u := testUploader(t, api)
	u.pollInterval = 50 * time.Millisecond // 两次 Processing 需要 ~150ms，超时设得更短

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	_, err := u.Resolve(ctx, []Item{{URL: "https://x/a.jpg", Type: AssetTypeImage}})
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestScopeSeparatesAccountsWithoutLeakingKeys(t *testing.T) {
	a := Scope(Config{Endpoint: "https://e", ProjectName: "default", APIKey: "sk-secret-1"})
	b := Scope(Config{Endpoint: "https://e", ProjectName: "default", APIKey: "sk-secret-2"})
	require.NotEqual(t, a, b)
	require.NotContains(t, a, "sk-secret-1")
	require.NotEqual(t, a, Scope(Config{Endpoint: "https://e", ProjectName: "other", APIKey: "sk-secret-1"}))
}
