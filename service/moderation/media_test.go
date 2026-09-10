package moderation

import (
	"context"
	"errors"
	"strings"
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
	origMode, origEndpoints, origFailOpen := s.Mode, s.Endpoints, s.FailOpen
	t.Cleanup(func() { s.Mode, s.Endpoints, s.FailOpen = origMode, origEndpoints, origFailOpen })

	// 指向一个必然连不上的地址，模拟审核服务整体挂掉。
	s.Endpoints = []system_setting.ModerationEndpoint{{
		Name: "dead", BaseURL: "http://127.0.0.1:1", Model: "x",
		Modality: ModalityImage, TimeoutMS: 200, Enabled: true,
	}}

	items := []MediaItem{{URL: testImagePNG, Type: types.FileTypeImage, Field: "img"}}
	req := &Request{UserId: 1, Group: "default", ModelName: "gpt-4o"}

	// observe 下无论 FailOpen 怎么配都不拒绝——这是 observe 的定义，
	// 与故障处置策略无关。
	for _, failOpen := range []bool{true, false} {
		ClearAllFreezes()
		s.Mode = system_setting.ModerationModeObserve
		s.FailOpen = failOpen
		got := ModerateMedia(context.Background(), req, items)
		if got.Blocked {
			t.Fatalf("observe 下审核服务挂掉绝不能拒绝请求（fail_open=%v），得到 reason=%q",
				failOpen, got.Reason)
		}
		if got.Action != ActionError {
			t.Fatalf("判定本身仍应记为 error（供运行态看出服务挂了），得到 %v", got.Action)
		}
	}
}

// TestFailOpenPolicy 审核服务挂掉时，blocking 下的处置由 FailOpen 决定。
//
// 这是业务侧拍板的取舍（§15.8）：GPUStack 升级、模型挂掉这类我方运维事件不该
// 变成用户可见的全站拒绝。两种配置都要能工作——关掉它就该回到原来的 fail-close。
func TestFailOpenPolicy(t *testing.T) {
	s := system_setting.GetModerationSettings()
	origMode, origEndpoints, origFailOpen := s.Mode, s.Endpoints, s.FailOpen
	t.Cleanup(func() { s.Mode, s.Endpoints, s.FailOpen = origMode, origEndpoints, origFailOpen })

	s.Endpoints = []system_setting.ModerationEndpoint{{
		Name: "dead", BaseURL: "http://127.0.0.1:1", Model: "x",
		Modality: ModalityImage, TimeoutMS: 200, Enabled: true,
	}}
	s.Mode = system_setting.ModerationModeBlocking

	items := []MediaItem{{URL: testImagePNG, Type: types.FileTypeImage, Field: "img"}}
	req := &Request{UserId: 1, Group: "default", ModelName: "gpt-4o"}

	t.Run("fail_open 开启时放行", func(t *testing.T) {
		ClearAllFreezes()
		s.FailOpen = true
		before := FailOpenCount()
		got := ModerateMedia(context.Background(), req, items)
		if got.Blocked {
			t.Fatalf("fail_open 开启时审核不可用必须放行，得到 reason=%q", got.Reason)
		}
		// 放行不等于「审核通过」：记录里必须仍是 error，否则事后分不清
		// 「这条审过没问题」和「这条根本没审」。
		if got.Action != ActionError {
			t.Fatalf("放行时判定仍应是 error，得到 %v", got.Action)
		}
		// 必须计数：fail-open 是彻底静默的，审核挂一整天业务毫无异常，
		// 只有这个数能说明有多少请求其实没审。
		if FailOpenCount() <= before {
			t.Fatal("放行必须被计数，否则这段降级完全不可见")
		}
	})

	t.Run("fail_open 关闭时拒绝", func(t *testing.T) {
		ClearAllFreezes()
		s.FailOpen = false
		got := ModerateMedia(context.Background(), req, items)
		if !got.Blocked {
			t.Fatal("fail_open 关闭时应回到 fail-close 拒绝")
		}
		if got.Reason == "" {
			t.Fatal("拒绝必须给出面向用户的原因")
		}
	})
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

func TestSignEvidenceURLDispatchesByScheme(t *testing.T) {
	ctx := context.Background()

	// 空 key：这条记录没留证据，必须是一个明确的错误而不是空字符串——
	// 调用方拿到空串会当成「签出来了」，前端就显示一个点不开的链接。
	if _, err := SignEvidenceURL(ctx, ""); !errors.Is(err, ErrNoEvidence) {
		t.Fatalf("空 object_key 应返回 ErrNoEvidence，得到 %v", err)
	}

	// 独立桶的 key，但桶没启用：要说清是配置问题。
	// 直接拿去主桶签会得到一个 403 的链接，管理员完全无从判断原因。
	s := system_setting.GetModerationStorageSettings()
	orig := s.Enabled
	t.Cleanup(func() { s.Enabled = orig })
	s.Enabled = false
	_, err := SignEvidenceURL(ctx, evidenceSchemeDedicated+"evidence/2026/09/10/1/abc.png")
	if err == nil {
		t.Fatal("对象在未启用的取证桶里时必须报错，不能退回主桶签名")
	}
	if !strings.Contains(err.Error(), "未启用") {
		t.Fatalf("错误信息要指出是桶未启用: %v", err)
	}

	// 无 scheme 的历史值原样返回：早期版本这里存的是完整 URL。
	legacy := "https://obs.example.com/ingest/2026/09/10/1/abc.png"
	got, err := SignEvidenceURL(ctx, legacy)
	if err != nil || got != legacy {
		t.Fatalf("历史值应原样返回，得到 %q / %v", got, err)
	}
}

func TestEvidenceKeyIsDeterministic(t *testing.T) {
	// key 必须能在**上传之前**算出来——记录要立刻落库，不能等最长 30 秒的网络 IO。
	// 一旦它依赖上传结果，记录就会被挂在慢操作后面：记录页要几十秒才出现、
	// 时间戳偏移、进程重启时连记录一起丢。
	// **必须把媒体存储也打开**。上一版只关了取证桶就以为会走回落分支，
	// 但那条分支还有一道 mediastore.Enabled() 的闸——测试进程里它同样是 false，
	// 于是 prepareEvidence 两次都返回空串，`k1 != k2` 比较的是两个 ""，
	// 断言完全空跑。探针打出来的就是 key=""。
	ms := system_setting.GetMediaStorageSettings()
	origMS := *ms
	t.Cleanup(func() { *ms = origMS })
	ms.Enabled = true
	ms.Endpoint = "https://obs.example.com"
	ms.Bucket = "test-bucket"

	es := system_setting.GetModerationStorageSettings()
	origES := es.Enabled
	t.Cleanup(func() { es.Enabled = origES })
	es.Enabled = false // 走回落主桶那条路，BuildKey 不需要真实连接

	it := MediaItem{URL: "data:image/png;base64,AAAA", Type: types.FileTypeImage}
	k1, up1 := prepareEvidence(&it, "abc123", 7)
	k2, _ := prepareEvidence(&it, "abc123", 7)

	if k1 == "" {
		t.Fatal("媒体存储已启用时必须算出 key，否则这条断言又是空跑")
	}
	if !strings.HasPrefix(k1, evidenceSchemeShared) {
		t.Fatalf("回落主桶时应带 %q 前缀，得到 %q", evidenceSchemeShared, k1)
	}
	if !strings.Contains(k1, "abc123") {
		t.Fatalf("key 应包含内容 hash（内容寻址），得到 %q", k1)
	}
	if k1 != k2 {
		t.Fatalf("同样的输入必须算出同样的 key，得到 %q / %q", k1, k2)
	}
	if up1 == nil {
		t.Fatal("data-url 需要真正上传一份，upload 闭包不该为 nil")
	}

	// 已经在我方 OBS 里的直接引用，不重复搬——这条路不需要上传。
	own := MediaItem{
		URL:  "https://test-bucket.obs.example.com/ingest/2026/09/10/1/x.png?AccessKeyId=AK",
		Type: types.FileTypeImage,
	}
	k3, up3 := prepareEvidence(&own, "def456", 7)
	if up3 != nil {
		t.Fatal("已在我方 OBS 的对象不该再上传一份")
	}
	if !strings.Contains(k3, "ingest/2026/09/10/1/x.png") {
		t.Fatalf("应直接反解出原 key，得到 %q", k3)
	}
}

func TestEvidenceExt(t *testing.T) {
	// 扩展名必须是纯字符串推导：它跑在请求路径上，不能为了拿个后缀去解码
	// 一个 200 MB 的 data-url。
	cases := []struct {
		url  string
		typ  types.FileType
		want string
	}{
		{"data:image/png;base64,AAAA", types.FileTypeImage, "png"},
		{"data:image/jpeg;base64,AAAA", types.FileTypeImage, "jpg"},
		{"data:video/mp4;base64,AAAA", types.FileTypeVideo, "mp4"},
		{"https://a.com/x/y.webp?sig=1", types.FileTypeImage, "webp"},
		{"https://a.com/x/y.mov?sig=1", types.FileTypeVideo, "mov"},
		// 认不出来时按类型兜底，不能返回空——空扩展名会让 key 以点结尾。
		{"https://a.com/noext", types.FileTypeImage, "jpg"},
		{"https://a.com/noext", types.FileTypeVideo, "mp4"},
		{"data:application/octet-stream;base64,AAAA", types.FileTypeImage, "jpg"},
	}
	for _, c := range cases {
		it := MediaItem{URL: c.url, Type: c.typ}
		if got := evidenceExt(&it); got != c.want {
			t.Errorf("evidenceExt(%q, %s) = %q, want %q", c.url, c.typ, got, c.want)
		}
	}
}

func TestEvidenceConcurrencyIsBounded(t *testing.T) {
	// 每个留存任务把整个对象读进内存（上界 MaxObjectSizeMB，默认 200 MB）。
	// 不限并发的话，一批并发被拦的大文件能直接把进程 OOM——而留存跑在 gopool
	// 的默认池上，那个池本身没有容量限制。
	held := 0
	defer func() {
		for i := 0; i < held; i++ {
			releaseEvidenceSlot()
		}
	}()
	for i := 0; i < evidenceConcurrency; i++ {
		if !acquireEvidenceSlot() {
			t.Fatalf("占满闸之前第 %d 个就拿不到名额", i)
		}
		held++
	}
	if acquireEvidenceSlot() {
		held++
		t.Fatal("超过上限时必须拿不到名额，否则并发无界")
	}
	releaseEvidenceSlot()
	held--
	if !acquireEvidenceSlot() {
		t.Fatal("释放后应能再次获取")
	}
	held++
}

func TestShouldCleanupLogsAfterEvidence(t *testing.T) {
	orig := evidenceCleanupSkips.Load()
	t.Cleanup(func() { evidenceCleanupSkips.Store(orig) })
	evidenceCleanupSkips.Store(0)

	// 清干净了就照常删记录，并把跳过计数归零。
	if !ShouldCleanupLogsAfterEvidence(true) {
		t.Fatal("取证清完后应当继续删记录")
	}
	if evidenceCleanupSkips.Load() != 0 {
		t.Fatal("成功一次应把连续跳过计数归零")
	}

	// 没清完就跳过——记录一删，object_key 就没了，剩下的对象永久变成
	// 桶里无人认领的违规内容。这正是那条「必须一次清完」注释存在的理由。
	for i := 1; i <= evidenceCleanupMaxSkips; i++ {
		if ShouldCleanupLogsAfterEvidence(false) {
			t.Fatalf("第 %d 轮未清完时不该删记录", i)
		}
	}

	// 但不能无限跳：OBS 长期不可用会让过期记录一直堆着，表无限增长。
	// 攒够轮数就强制收口（并告警），否则「防孤儿」会变成「表撑爆」。
	if !ShouldCleanupLogsAfterEvidence(false) {
		t.Fatalf("连续跳过超过 %d 轮后必须强制清理，否则表会无限增长", evidenceCleanupMaxSkips)
	}
	if evidenceCleanupSkips.Load() != 0 {
		t.Fatal("强制清理后应重置计数，否则下一轮立刻又强制")
	}
}

func TestEvidenceSchemesAreDistinct(t *testing.T) {
	// 两个前缀不能互为前缀，否则 strings.HasPrefix 的分派会串——
	// 独立桶的 key 会被当成主桶的，拿错凭证去签，结果是 403。
	if strings.HasPrefix(evidenceSchemeDedicated, evidenceSchemeShared) ||
		strings.HasPrefix(evidenceSchemeShared, evidenceSchemeDedicated) {
		t.Fatalf("两个 scheme 不能互为前缀: %q / %q", evidenceSchemeDedicated, evidenceSchemeShared)
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
