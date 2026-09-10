package moderation

import (
	"context"
	"testing"

	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/QuantumNous/new-api/types"
)

func TestMediaItemsFromFiles(t *testing.T) {
	t.Run("只取图片和视频", func(t *testing.T) {
		files := []*types.FileMeta{
			{FileType: types.FileTypeImage, Source: types.NewURLFileSource("https://a/1.png")},
			// 音频与通用文件必须跳过：送进 ShieldGemma 会解码失败，blocking 下
			// 变成 fail-close，一个正常的语音请求被拒且理由是「审核服务不可用」。
			{FileType: types.FileTypeAudio, Source: types.NewURLFileSource("https://a/1.wav")},
			{FileType: types.FileTypeFile, Source: types.NewURLFileSource("https://a/1.pdf")},
			{FileType: types.FileTypeVideo, Source: types.NewURLFileSource("https://a/1.mp4")},
		}
		items := MediaItemsFromFiles(files)
		if len(items) != 2 {
			t.Fatalf("应只取图片与视频两项，得到 %d 项: %+v", len(items), items)
		}
		if items[0].Type != types.FileTypeImage || items[1].Type != types.FileTypeVideo {
			t.Fatalf("类型不对: %+v", items)
		}
	})

	t.Run("裸 base64 拼回 data-url", func(t *testing.T) {
		// Claude 那条路给的是裸 base64 + 单独的 media_type（dto/claude.go:112）。
		// 不拼回去的话送给模型的是一串没有头的 base64，必然解码失败。
		files := []*types.FileMeta{
			{FileType: types.FileTypeImage, Source: types.NewFileSourceFromData("AAAA", "image/png")},
		}
		items := MediaItemsFromFiles(files)
		if len(items) != 1 || items[0].URL != "data:image/png;base64,AAAA" {
			t.Fatalf("data-url 拼接不对: %+v", items)
		}
	})

	t.Run("已是 data-url 的不能再套一层", func(t *testing.T) {
		// OpenAI 格式最常见的那种带图请求：
		//   {"type":"image_url","image_url":{"url":"data:image/png;base64,..."}}
		//
		// MediaContent.ToFileSource（dto/openai_request.go:401）把 img.Url 原样交给
		// NewFileSourceFromData，而后者只特判 http(s)://，于是**完整 data-url 整串**
		// 落进 Base64Data；MimeType 又读的是 OpenAI 根本不存在的 mime_type 键，恒为空。
		// 再拼一次前缀就得到 data:image/jpeg;base64,data:image/png;base64,...
		// ——payload 不是合法 base64，节点 500，blocking 下每个带图请求都 503。
		//
		// 这条用例必须用 NewFileSourceFromData 构造，不能图省事直接 NewBase64FileSource：
		// 上一版就是手工构造裸 base64，绕过了生产实际走的入口，于是这个 bug 一路绿灯。
		const dataURL = "data:image/png;base64,iVBORw0KGgo="
		files := []*types.FileMeta{
			{FileType: types.FileTypeImage, Source: types.NewFileSourceFromData(dataURL, "")},
		}
		items := MediaItemsFromFiles(files)
		if len(items) != 1 {
			t.Fatalf("应产出一项，得到 %+v", items)
		}
		if items[0].URL != dataURL {
			t.Fatalf("data-url 被重复加前缀:\n  得到 %s\n  期望 %s", items[0].URL, dataURL)
		}
	})

	t.Run("video_url 的 data-url 同样不能套两层", func(t *testing.T) {
		// ContentTypeVideoUrl 走 NewFileSourceFromData(video.Url, "")，形态与图片一致。
		// 视频这条路后果更重：extractFramesFromDataURL 会从**被注入的**逗号处切，
		// base64.StdEncoding 直接拒绝含 ':' ';' ',' 的串。
		const dataURL = "data:video/mp4;base64,AAAAIGZ0eXA="
		files := []*types.FileMeta{
			{FileType: types.FileTypeVideo, Source: types.NewFileSourceFromData(dataURL, "")},
		}
		items := MediaItemsFromFiles(files)
		if len(items) != 1 || items[0].URL != dataURL {
			t.Fatalf("视频 data-url 被重复加前缀: %+v", items)
		}
	})

	t.Run("MIME 缺失时按类型兜底", func(t *testing.T) {
		files := []*types.FileMeta{
			{FileType: types.FileTypeImage, Source: types.NewBase64FileSource("AAAA", "")},
			{FileType: types.FileTypeVideo, Source: types.NewBase64FileSource("BBBB", "")},
		}
		items := MediaItemsFromFiles(files)
		if len(items) != 2 {
			t.Fatalf("MIME 缺失不该导致丢项，得到 %d 项", len(items))
		}
		if items[0].URL != "data:image/jpeg;base64,AAAA" {
			t.Fatalf("图片兜底 MIME 不对: %s", items[0].URL)
		}
		if items[1].URL != "data:video/mp4;base64,BBBB" {
			t.Fatalf("视频兜底 MIME 不对: %s", items[1].URL)
		}
	})

	t.Run("去重", func(t *testing.T) {
		// 多轮对话里同一张图会反复出现在历史中，重复送审是纯浪费。
		src := types.NewURLFileSource("https://a/1.png")
		files := []*types.FileMeta{
			{FileType: types.FileTypeImage, Source: src},
			{FileType: types.FileTypeImage, Source: types.NewURLFileSource("https://a/1.png")},
		}
		if items := MediaItemsFromFiles(files); len(items) != 1 {
			t.Fatalf("同一 URL 应只审一次，得到 %d 项", len(items))
		}
	})

	t.Run("nil 与空值安全", func(t *testing.T) {
		files := []*types.FileMeta{
			nil,
			{FileType: types.FileTypeImage, Source: nil},
			{FileType: types.FileTypeImage, Source: types.NewBase64FileSource("", "image/png")},
		}
		if items := MediaItemsFromFiles(files); len(items) != 0 {
			t.Fatalf("空值不该产出待审项，得到 %+v", items)
		}
		if items := MediaItemsFromFiles(nil); len(items) != 0 {
			t.Fatal("nil 输入应返回空")
		}
	})
}

// TestObserveNeverBlocksWhenServiceDown observe 模式下审核服务挂掉不得拒绝任何请求。
//
// 这是 observe 存在的全部意义：拿真实流量校准误杀率，而**不承担任何业务风险**。
// 如果审核服务一挂就开始拒请求，灰度本身就成了事故，没人敢开。
func TestObserveNeverBlocksWhenServiceDown(t *testing.T) {
	s := system_setting.GetModerationSettings()
	origMode, origEndpoints := s.Mode, s.Endpoints
	t.Cleanup(func() { s.Mode, s.Endpoints = origMode, origEndpoints })

	// 指向一个必然连不上的地址，模拟审核服务整体挂掉。
	s.Endpoints = []system_setting.ModerationEndpoint{{
		Name: "dead", BaseURL: "http://127.0.0.1:1", Model: "x",
		Modality: ModalityImage, TimeoutMS: 200, Enabled: true,
	}}
	ClearAllFreezes()

	items := []MediaItem{{URL: testImagePNG, Type: types.FileTypeImage, Field: "img"}}
	req := &Request{UserId: 1, Group: "default", ModelName: "gpt-4o"}

	s.Mode = system_setting.ModerationModeObserve
	got := ModerateMedia(context.Background(), req, items)
	if got.Blocked {
		t.Fatalf("observe 下审核服务挂掉绝不能拒绝请求，得到 Blocked=true reason=%q", got.Reason)
	}
	if got.Action != ActionError {
		t.Fatalf("判定本身仍应记为 error（供运行态看出服务挂了），得到 %v", got.Action)
	}

	// 反面：同样的故障在 blocking 下必须拒绝。两者的差别就是 observe 的定义。
	ClearAllFreezes()
	s.Mode = system_setting.ModerationModeBlocking
	got = ModerateMedia(context.Background(), req, items)
	if !got.Blocked {
		t.Fatal("blocking 下审核服务挂掉必须 fail-close 拒绝，否则垫长构造就能穿透")
	}
}

func TestItemEnforced(t *testing.T) {
	// enforced 那一列的存在理由是「本周拦了多少按它数，不按 action 数」
	// （model/moderation_log.go:44）。所以它必须回答「这条判定真的执行了吗」，
	// 而不是「判成什么了」。
	block := &Verdict{Action: ActionBlock}
	errV := &Verdict{Action: ActionError}
	pass := &Verdict{Action: ActionPass}

	// blocking 下真拦下来的
	if !itemEnforced(block, true, true) {
		t.Fatal("blocking 下的 block 必须记 enforced=true")
	}
	// observe 下判了但没拦
	if itemEnforced(block, false, false) {
		t.Fatal("observe 下不该记 enforced=true——那正是 observe 与 blocking 的分界")
	}
	// fail-close：审核未完成本身不拦请求，是整体决策拦的，所以要看整体
	if !itemEnforced(errV, true, true) {
		t.Fatal("blocking 下 fail-close 拒绝时，导致它的 error 项必须记 enforced=true；" +
			"记 false 会让请求被拒但统计里查无此事，而文本链对同一件事记的是 true")
	}
	if itemEnforced(errV, false, false) {
		t.Fatal("observe 下审核故障不拒请求，不该记 enforced=true")
	}
	// 同一请求里通过的那些项，不能因为别的项导致了拒绝就跟着记成 enforced
	if itemEnforced(pass, true, true) {
		t.Fatal("通过的项不该记 enforced=true——否则「这张图被拦了」会凭空多出几条")
	}
}

func TestMediaHash(t *testing.T) {
	// 调用方给了 hash 就用它——那是对字节算的，比对 URL 算更准
	// （同一张图换个签名参数不该重新审）。
	it := MediaItem{URL: "https://a/1.png", Hash: "deadbeef"}
	if got := mediaHash(&it); got != "deadbeef" {
		t.Fatalf("应优先用调用方给的 hash，得到 %s", got)
	}

	// 没给时对 URL 算，且必须稳定
	a := MediaItem{URL: "https://a/1.png"}
	b := MediaItem{URL: "https://a/1.png"}
	if mediaHash(&a) != mediaHash(&b) {
		t.Fatal("同一 URL 的 hash 必须稳定，否则缓存永远不命中")
	}
	c := MediaItem{URL: "https://a/2.png"}
	if mediaHash(&a) == mediaHash(&c) {
		t.Fatal("不同 URL 不该算出相同 hash")
	}
}

func TestObjectKeyForLog(t *testing.T) {
	// data-url 一律不入库：它本身是内容不是引用，存前 512 字节既回溯不了，
	// 又把用户上传的内容明文摊进一张无需审计就能列出来的表。
	if got := objectKeyForLog("data:image/png;base64,AAAA"); got != "" {
		t.Fatalf("data-url 不该入库，得到 %q", got)
	}
	url := "https://obs.example.com/ingest/2026/09/10/1/abc.png"
	if got := objectKeyForLog(url); got != url {
		t.Fatalf("http URL 应原样保留，得到 %q", got)
	}
	long := "https://a.com/" + string(make([]byte, 600))
	if got := objectKeyForLog(long); len(got) != 512 {
		t.Fatalf("超长 URL 应截断到列宽 512，得到 %d", len(got))
	}
}

func TestImageScoreCacheKeyIsVersioned(t *testing.T) {
	// 版本号必须出现在 key 里：换模型或改策略文本后旧缓存依然能反序列化成功，
	// 只是含义变了——这种失效不报错，只会让判定悄悄沿用旧模型的结论最长七天。
	key := imageScoreCacheKey("abc")
	if key == "moderation:img:abc" {
		t.Fatal("缓存 key 必须带版本号")
	}
	if imageScoreCacheKey("abc") == videoScoreCacheKey("abc") {
		t.Fatal("图片与视频的缓存 key 不能相同——它们的值结构不一样")
	}
}
