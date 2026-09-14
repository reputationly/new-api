package dto

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// **对象形态的 video_url 必须被解析出来。**
//
// 官方 OpenAI 兼容约定里 video_url 和 image_url 一样是个对象,平台侧实测也
// 只吃这一种。原先只认裸字符串,后果不是报错而是**静默丢弃**:解析不出来就
// 没有 MediaContent,于是 GetTokenCountMeta 不产 FileMeta(输入媒体审核看不到
// 这段视频),按 ParseContent 重建请求体的那些转换器(Claude/Gemini/zhipu/
// dify/ollama)也会把它整段丢掉。
//
// OpenAI 格式直通的渠道因为原样转发 Content 反而看不出问题 —— 这个缺陷
// **跟渠道形态走**,最难发现的那种。
func TestParseContentAcceptsObjectVideoURL(t *testing.T) {
	m := &Message{Role: "user"}
	m.SetMediaContent(nil)
	m.Content = []any{
		map[string]any{"type": "text", "text": "look"},
		map[string]any{"type": "video_url", "video_url": map[string]any{"url": "https://e.com/v.mp4"}},
	}
	got := m.ParseContent()
	require.Len(t, got, 2)
	require.Equal(t, ContentTypeVideoUrl, got[1].Type)
	v := got[1].GetVideoUrl()
	require.NotNil(t, v)
	require.Equal(t, "https://e.com/v.mp4", v.Url)
}

// 裸字符串形态继续支持 —— 这是纯增量,不能把既有调用方弄坏。
func TestParseContentStillAcceptsStringVideoURL(t *testing.T) {
	m := &Message{Role: "user"}
	m.Content = []any{
		map[string]any{"type": "video_url", "video_url": "https://e.com/v.mp4"},
	}
	got := m.ParseContent()
	require.Len(t, got, 1)
	require.Equal(t, "https://e.com/v.mp4", got[0].GetVideoUrl().Url)
}

// **视频要能进输入媒体审核的视野。** 这是上面那个"静默丢弃"最要紧的下游。
func TestObjectVideoURLReachesTokenCountMeta(t *testing.T) {
	req := &GeneralOpenAIRequest{Messages: []Message{{
		Role: "user",
		Content: []any{
			map[string]any{"type": "video_url", "video_url": map[string]any{"url": "https://e.com/v.mp4"}},
		},
	}}}
	meta := req.GetTokenCountMeta()
	require.NotNil(t, meta)
	var found bool
	for _, f := range meta.Files {
		if f.FileType == "video" {
			found = true
		}
	}
	require.True(t, found, "对象形态的视频没进 FileMeta，输入媒体审核看不到它")
}

// 缺 url 或形态不认的,不要造出一条空的媒体项 —— 那会让下游拿到一个
// 指向空串的"视频"。
func TestParseContentSkipsMalformedVideoURL(t *testing.T) {
	m := &Message{Role: "user"}
	m.Content = []any{
		map[string]any{"type": "video_url", "video_url": map[string]any{"detail": "high"}},
		map[string]any{"type": "video_url", "video_url": 42},
	}
	require.Empty(t, m.ParseContent())
}
