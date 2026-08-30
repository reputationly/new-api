package relay

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"

	"github.com/gin-gonic/gin"
)

func asyncInfo(channelType int) *relaycommon.RelayInfo {
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeImageSubmit,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: channelType},
	}
	info.OriginModelName = "z-image"
	return info
}

func testCtx() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)
	return c
}

func TestCheckAsyncImageSupportedAllowsGPUStackPlus(t *testing.T) {
	if err := checkAsyncImageSupported(testCtx(), asyncInfo(constant.ChannelTypeGPUStackPlus)); err != nil {
		t.Fatalf("gpustackplus should support async images, got: %+v", err)
	}
}

// 不支持的渠道必须明确 400，不能静默降级为同步：降级会回一个 ImageResponse
// （有 data[]、无 id/status），而客户端在等 job 对象并准备去轮询 —— 解析必崩，
// 且只在某些模型上偶发，比明确报错难查得多。
func TestCheckAsyncImageSupportedRejectsOtherChannels(t *testing.T) {
	for _, ct := range []int{constant.ChannelTypeOpenAI, constant.ChannelTypeAli, constant.ChannelTypeGemini} {
		taskErr := checkAsyncImageSupported(testCtx(), asyncInfo(ct))
		if taskErr == nil {
			t.Fatalf("channel type %d does not support async images but was allowed", ct)
		}
		if taskErr.StatusCode != 400 {
			t.Errorf("channel type %d: status = %d, want 400", ct, taskErr.StatusCode)
		}
		if taskErr.Code != "async_not_supported" {
			t.Errorf("channel type %d: code = %q, want async_not_supported", ct, taskErr.Code)
		}
		// skip-retry：能力缺失不是瞬时故障，跨渠道重试是碰运气。
		// 不标 Local 的话 relay 会拿同一份请求去轮别的渠道，把一次确定的 400
		// 变成 N 次无谓的尝试。
		if !taskErr.LocalError {
			t.Errorf("channel type %d: capability failure must be skip-retry (LocalError)", ct)
		}
		// 报错要能让调用方知道下一步怎么办。
		if !strings.Contains(taskErr.Message, "async") {
			t.Errorf("channel type %d: message should tell the caller to drop async, got: %s",
				ct, taskErr.Message)
		}
	}
}

// 同步图片请求（没被 ImageAsyncConvert 改写过 relay_mode）不该被这道闸门碰到 ——
// 它跑在 RelayTaskSubmit 里，而同步图片根本不走那条路；但万一将来有人把它挪到
// 更靠前的共用位置，这条用例会挡住误伤。
func TestCheckAsyncImageSupportedIgnoresNonAsyncRequests(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayMode:   relayconstant.RelayModeImagesGenerations,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeOpenAI},
	}
	if err := checkAsyncImageSupported(testCtx(), info); err != nil {
		t.Fatalf("sync image request must not be gated by the async capability check, got: %+v", err)
	}
}
