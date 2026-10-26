package relay

import (
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newTaskTextsCtx(t *testing.T, req any) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("task_request", req)
	return c
}

// 音乐这条路上真正唱出来的字不全在 prompt 里：ACE-Step 系在 metadata.lyrics，
// Music3 反过来把歌词放 prompt、把 caption 放 metadata.instructions。
// 只取 prompt 的话，ACE-Step 的歌词整段绕过审核，而记录页看起来 prompt 一条不漏。
func TestExtractTaskTextsCoversMusicLyrics(t *testing.T) {
	req := relaycommon.TaskSubmitReq{
		Prompt: "女声流行，120bpm",
		Metadata: map[string]interface{}{
			"lyrics":       "第一句\n第二句",
			"instructions": "City Pop，明亮合成器",
			// ACE-Step 的 sample 模式按它在服务端写歌词：前端会把它填成 prompt 同值，
			// API 直连却可以让它和 prompt 完全不同——不审就是一个绕过口子。
			"sample_query": "夜店、霓虹、失恋",
		},
	}

	texts := extractTaskTexts(newTaskTextsCtx(t, req), nil)

	require.Contains(t, texts, "女声流行，120bpm")
	require.Contains(t, texts, "第一句\n第二句", "ACE-Step 的歌词必须送审")
	require.Contains(t, texts, "City Pop，明亮合成器", "Music3 的 caption 必须送审")
	require.Contains(t, texts, "夜店、霓虹、失恋", "ACE-Step sample 模式的写词依据必须送审")
}

func TestExtractTaskTextsToleratesMetadataShapes(t *testing.T) {
	// 指针形态（suno 之外多数平台存的是值，但两种都要吃）
	ptr := &relaycommon.TaskSubmitReq{Prompt: "  只有描述  "}
	require.Equal(t, []string{"  只有描述  "}, extractTaskTexts(newTaskTextsCtx(t, ptr), nil),
		"没有 lyrics/instructions 时不应凭空多出空串")

	// 非字符串值（前端/直连都可能塞 null 或对象）不能被当成文本送审
	weird := relaycommon.TaskSubmitReq{
		Prompt:   "描述",
		Metadata: map[string]interface{}{"lyrics": nil, "instructions": 42},
	}
	require.Equal(t, []string{"描述"}, extractTaskTexts(newTaskTextsCtx(t, weird), nil))

	// 空串同样要丢掉：送一个 "" 进去只会白白多一条 L1 记录
	empty := relaycommon.TaskSubmitReq{
		Prompt:   "描述",
		Metadata: map[string]interface{}{"lyrics": "   ", "instructions": ""},
	}
	require.Equal(t, []string{"描述"}, extractTaskTexts(newTaskTextsCtx(t, empty), nil))
}
