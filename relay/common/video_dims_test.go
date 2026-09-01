package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestVideoResolutionTier(t *testing.T) {
	cases := map[string]string{
		// 档位形态原样归一
		"720P":   "720p",
		"1080p":  "1080p",
		"4K":     "4k",
		" 480P ": "480p",
		// 比例形态不含分辨率信息
		"16:9": "",
		"9:16": "",
		// 像素形态按短边归档
		"1280x720":  "720p",
		"1080x1920": "1080p", // 竖屏：短边 1080
		"1920x1080": "1080p",
		"854x480":   "480p",
		"640x360":   "480p", // 短边 360 → 归到不小于它的最近档
		"3840x2160": "4k",
		// 无法解析
		"":      "",
		"abc":   "",
		"large": "",
	}
	for size, want := range cases {
		require.Equalf(t, want, VideoResolutionTier(size), "size=%q", size)
	}
}

// LTX-2.5 的对外档位是 544P/704P/1080P/2K（relay/channel/task/gpustackplus/ltx25.go
// 的 ltx25SizeTiers），不是行业通用的 480/720/1080/4K。其中 2K 曾经解析不出来——
// 档位正则把 4k 写成了字面值而不是 \d+k，于是 "2K" 落到像素归档、DimsFromSize 失败、
// 返回空串，计费矩阵的 lookupCell 见到空行名直接判未命中，静默回退固定单价。
// 2K 恰好是最贵的档，等于最贵的档唯一收不到钱。
func TestVideoResolutionTier_KTiers(t *testing.T) {
	cases := map[string]string{
		"2K": "2k",
		"2k": "2k",
		"4K": "4k",
		"8k": "8k",
		// LTX 的另外三档走 \d+p 分支，一并钉住
		"544P":  "544p",
		"704P":  "704p",
		"1080P": "1080p",
		// H3 的两档
		"480P": "480p",
		"768p": "768p",
		// 不是档位词：k 前面必须是数字，别把 "k" / "2kk" 也放进来
		"k":     "",
		"2kk":   "",
		"2k4":   "",
		"large": "",
	}
	for size, want := range cases {
		require.Equalf(t, want, VideoResolutionTier(size), "size=%q", size)
	}
}

func TestResolveVideoDims_MetadataResolutionWins(t *testing.T) {
	req := &TaskSubmitReq{
		Size:     "1280x720",
		Metadata: map[string]any{"resolution": "1080P"},
	}
	res, _, _ := ResolveVideoDims(req)
	require.Equal(t, "1080p", res, "metadata 原生键必须压过顶层 size，与适配器优先级一致")
}

// 按次计费的秒数只认 Duration —— kling(:271) / vidu(:232) / jimeng(:387)
// 读的都是 req.Duration，完全忽略 Seconds。按 Seconds 查表会「按 10 秒收费、
// 上游只生成 5 秒」。取不到宁可返回 0 让矩阵未命中，也不猜各渠道的默认值。
func TestResolveVideoDims_Seconds(t *testing.T) {
	cases := []struct {
		name string
		req  *TaskSubmitReq
		want int
	}{
		{"只认 duration", &TaskSubmitReq{Duration: 5}, 5},
		{"duration 压过 seconds", &TaskSubmitReq{Seconds: "10", Duration: 5}, 5},
		{"只给 seconds 视为取不到", &TaskSubmitReq{Seconds: "10"}, 0},
		{"seconds 带单位也不认", &TaskSubmitReq{Seconds: "10s"}, 0},
		{"都没有", &TaskSubmitReq{}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, secs, _ := ResolveVideoDims(c.req)
			require.Equal(t, c.want, secs)
		})
	}
}

func TestVideoHasVideoInput(t *testing.T) {
	cases := []struct {
		name string
		md   map[string]any
		want bool
	}{
		{"nil", nil, false},
		{"空", map[string]any{}, false},
		{"reference_videos 数组", map[string]any{"reference_videos": []any{"https://a/v.mp4"}}, true},
		{"reference_video 单串", map[string]any{"reference_video": "https://a/v.mp4"}, true},
		{"reference_videos 空数组", map[string]any{"reference_videos": []any{}}, false},
		{"reference_video 空串", map[string]any{"reference_video": "  "}, false},
		{"content 里的 video_url 条目", map[string]any{
			"content": []any{map[string]any{"type": "video_url", "video_url": map[string]any{"url": "https://a/v.mp4"}}},
		}, true},
		{"content 里只有图", map[string]any{
			"content": []any{map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://a/i.png"}}},
		}, false},
		// 自建流水线字段不算视频输入——算进来会错判成更便宜的档
		{"metadata.video 不算", map[string]any{"video": "https://a/v.mp4"}, false},
		{"metadata.src_video 不算", map[string]any{"src_video": "https://a/v.mp4"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, VideoHasVideoInput(c.md))
		})
	}
}

func TestResolveVideoDims_NilRequest(t *testing.T) {
	res, secs, hasVideo := ResolveVideoDims(nil)
	require.Equal(t, "", res)
	require.Equal(t, 0, secs)
	require.False(t, hasVideo)
}

// VideoSecondsFallback 只解析 Seconds，不做任何渠道判断——用不用它由 relay 层按
// 渠道决定（见 relay/video_billing.go 的 videoBillingSeconds）。
func TestVideoSecondsFallback(t *testing.T) {
	cases := map[string]int{
		"10": 10,
		" 8": 8,
		"1":  1,
		// 脏值一律 0：算成秒数就是凭空定价
		"":     0,
		"  ":   0,
		"abc":  0,
		"0":    0,
		"-3":   0,
		"5.5":  0,
		"10s":  0, // 带单位不认，与 Duration 的整数语义一致
		"1e3":  0,
		"０":    0, // 全角数字
	}
	for in, want := range cases {
		require.Equalf(t, want, VideoSecondsFallback(&TaskSubmitReq{Seconds: in}), "seconds=%q", in)
	}
	require.Zero(t, VideoSecondsFallback(nil))
}
