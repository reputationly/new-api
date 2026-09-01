package service

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/mediastore"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// 同步生图的任务记录靠 RewriteImageResponseToOBS 回吐的 key 才有图可预览。
// 这些用例钉住的是「哪些情况有 key、哪些情况没有」——不回吐 key 时任务记录照常写，
// 但任务日志里那条记录点开是空的。

func withStubbedImagePersist(t *testing.T, fn func(ctx context.Context, userID int, modelName string, src mediastore.PersistSource, ext string) (string, string, error)) {
	t.Helper()
	s := system_setting.GetMediaStorageSettings()
	prevEnabled := s.Enabled
	prevPersist := persistImageToOBS
	s.Enabled = true
	persistImageToOBS = fn
	t.Cleanup(func() {
		s.Enabled = prevEnabled
		persistImageToOBS = prevPersist
	})
}

// 落盘成功必须把对象 key 一并回吐。只回签名 URL 是不够的——签名 URL 有有效期，
// 存进任务表过期即成死链，任务表要存的是 obs://<key>。
func TestRewriteImageResponseReturnsPersistedKeys(t *testing.T) {
	withStubbedImagePersist(t, func(_ context.Context, _ int, _ string, _ mediastore.PersistSource, _ string) (string, string, error) {
		return "https://bucket.obs.example.com/t2i/a.png?sign=x", "t2i/a.png", nil
	})

	body := []byte(`{"created":1,"data":[{"url":"https://upstream.example.com/a.png"}]}`)
	out, keys := RewriteImageResponseToOBS(context.Background(), 42, 0, "z-image", "", body)

	if len(keys) != 1 || keys[0] != "t2i/a.png" {
		t.Fatalf("keys = %v, want [t2i/a.png]", keys)
	}
	// 回吐 key 的同时，响应体本身仍要被改写成签名 URL（原有行为不能回归）。
	var got struct {
		Data []struct {
			Url string `json:"url"`
		} `json:"data"`
	}
	if err := common.Unmarshal(out, &got); err != nil {
		t.Fatalf("unmarshal rewritten body: %v", err)
	}
	if len(got.Data) != 1 || got.Data[0].Url != "https://bucket.obs.example.com/t2i/a.png?sign=x" {
		t.Errorf("rewritten body url = %+v, want the signed OBS url", got.Data)
	}
}

// 多图逐项回吐，顺序与 data[] 一致。
func TestRewriteImageResponseReturnsKeyPerPersistedItem(t *testing.T) {
	n := 0
	withStubbedImagePersist(t, func(_ context.Context, _ int, _ string, _ mediastore.PersistSource, _ string) (string, string, error) {
		n++
		return "https://bucket.obs.example.com/signed", []string{"k1", "k2"}[n-1], nil
	})

	body := []byte(`{"data":[{"url":"https://up/a.png"},{"url":"https://up/b.png"}]}`)
	_, keys := RewriteImageResponseToOBS(context.Background(), 42, 0, "z-image", "", body)

	if len(keys) != 2 || keys[0] != "k1" || keys[1] != "k2" {
		t.Errorf("keys = %v, want [k1 k2]", keys)
	}
}

// 客户端点名要 b64_json 时不落盘，也就没有 key。这是已知且接受的缺口：
// 这类请求的任务记录只有元信息、没有图。绝不能为了补图把 base64 塞进 tasks.data。
func TestRewriteImageResponseReturnsNoKeysForB64Passthrough(t *testing.T) {
	withStubbedImagePersist(t, func(_ context.Context, _ int, _ string, _ mediastore.PersistSource, _ string) (string, string, error) {
		t.Error("must not persist when the client explicitly asked for b64_json")
		return "", "", nil
	})

	body := []byte(`{"data":[{"b64_json":"aGVsbG8="}]}`)
	out, keys := RewriteImageResponseToOBS(context.Background(), 42, 0, "z-image", "b64_json", body)

	if len(keys) != 0 {
		t.Errorf("keys = %v, want none", keys)
	}
	if string(out) != string(body) {
		t.Errorf("body must be untouched, got %s", out)
	}
}

// 落盘失败降级保留上游 url，且不能回吐一个没真正落盘的 key ——
// 那会让任务记录指向一个不存在的 OBS 对象，点开预览是 403。
func TestRewriteImageResponseReturnsNoKeyWhenPersistFails(t *testing.T) {
	withStubbedImagePersist(t, func(_ context.Context, _ int, _ string, _ mediastore.PersistSource, _ string) (string, string, error) {
		return "", "", context.DeadlineExceeded
	})

	body := []byte(`{"data":[{"url":"https://upstream.example.com/a.png"}]}`)
	out, keys := RewriteImageResponseToOBS(context.Background(), 42, 0, "z-image", "", body)

	if len(keys) != 0 {
		t.Errorf("keys = %v, want none when persist failed", keys)
	}
	if string(out) != string(body) {
		t.Errorf("body must fall back to the upstream url, got %s", out)
	}
}

// 媒体存储没开时既不落盘也没有 key。
//
// 落盘桩故意设成「会成功」：否则 persistThirdPartyImage 内部的 IngestUpstreamURL 开关
// 也会挡住落盘，测试即便在总开关失效时依然绿——变异验证过，这样才有区分度。
func TestRewriteImageResponseReturnsNoKeysWhenStorageDisabled(t *testing.T) {
	withStubbedImagePersist(t, func(_ context.Context, _ int, _ string, _ mediastore.PersistSource, _ string) (string, string, error) {
		return "https://bucket.obs.example.com/signed", "should-not-be-returned", nil
	})
	// withStubbedImagePersist 打开了总开关，这里再关掉——本用例要测的正是关掉之后。
	s := system_setting.GetMediaStorageSettings()
	s.Enabled = false

	body := []byte(`{"data":[{"url":"https://upstream.example.com/a.png"}]}`)
	out, keys := RewriteImageResponseToOBS(context.Background(), 42, 0, "z-image", "", body)
	if len(keys) != 0 {
		t.Errorf("keys = %v, want none when media storage is disabled", keys)
	}
	if string(out) != string(body) {
		t.Errorf("body must be untouched when media storage is disabled, got %s", out)
	}
}
