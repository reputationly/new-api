package relay

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 计费秒数的探测要接住 `task:<id>` 这种输入形态。
//
// 背景（一次真实的账单口径不一致）：体验区提交超分段时 `metadata.video` 是
// `task:<父任务id>`，而探测原本只认「绝对路径 / http(s) / base64」三种形态 ——
// `task:` 直接被判成"不认识"，seconds=0，按秒的视频计费矩阵随即回退固定价。
// 症状是"配了按秒矩阵、账单却按次收"，而日志里只有一条 warnVideoMatrixMiss。
// 聚合路径发的是 NFS 绝对路径，所以同一个模型两条路两种账单。
//
// 这里钉住的是**回退安全**：拿不到归属/拿不到产物时必须返回 0（回退固定价），
// 绝不能 panic、也不能误把 `task:` 当成 base64 去解码。

func TestProbeTaskRefFallsBackWithoutCaller(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// 没有 gin 上下文（结算侧的部分调用就是这样）→ 0，且不碰库。
	require.Equal(t, 0.0, probeVideoDurationFromInput(nil, "task:abc"))

	// 有上下文但没有用户身份 → 0。归属校验的第一道闸就在这里。
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	require.Equal(t, 0.0, probeVideoDurationFromTaskRef(c, "task:abc"))
	require.Equal(t, 0.0, probeVideoDurationFromInput(c, "task:abc"))
}

// `task:` 引用必须在 base64 启发式**之前**被判掉。
//
// base64 判定是"看着像就试"，`task:xxx` 这种带前缀的串一旦先进去解码，
// 轻则白跑一次解码，重则解出一段垃圾字节去解析 mvhd。
func TestProbeTaskRefWinsOverBase64Heuristic(t *testing.T) {
	require.False(t, looksLikeBase64Video("task:abc"),
		"task: 引用不该被当成 base64")
}
