package relay

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// h3DeliveredBillingSeconds:只有配置里声明了 minimax-h3 引擎族的模型才算交付秒,
// 其它模型/未指定时长一律返回 0(调用方退回请求值,行为与改造前一致)。
func TestH3DeliveredBillingSeconds(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	previous, had := common.OptionMap["VideoModelConfig"]
	common.OptionMap["VideoModelConfig"] = `{"models":{"h3-public":{"engine":"minimax-h3"},"wan":{"engine":"wan"}}}`
	common.OptionMapRWMutex.Unlock()
	defer func() {
		common.OptionMapRWMutex.Lock()
		if had {
			common.OptionMap["VideoModelConfig"] = previous
		} else {
			delete(common.OptionMap, "VideoModelConfig")
		}
		common.OptionMapRWMutex.Unlock()
	}()

	h3 := &relaycommon.RelayInfo{OriginModelName: "h3-public"}
	other := &relaycommon.RelayInfo{OriginModelName: "wan"}

	if got := h3DeliveredBillingSeconds(h3, 1); math.Abs(got-1.625) > 1e-9 {
		t.Fatalf("H3 1 s 的交付秒 = %v, want 1.625", got)
	}
	if got := h3DeliveredBillingSeconds(h3, 20); math.Abs(got-20.041666666666668) > 1e-9 {
		t.Fatalf("H3 20 s 的交付秒 = %v, want 481/24", got)
	}
	if got := h3DeliveredBillingSeconds(other, 5); got != 0 {
		t.Fatalf("非 H3 模型不该有交付口径, got %v", got)
	}
	if got := h3DeliveredBillingSeconds(h3, 0); got != 0 {
		t.Fatalf("未指定时长不该编一个数, got %v", got)
	}
	if got := h3DeliveredBillingSeconds(nil, 5); got != 0 {
		t.Fatalf("nil info 应返回 0, got %v", got)
	}
}

// effectiveVideoBillingSeconds:有交付口径用它,没有就用请求值(保持存量行为)。
func TestEffectiveVideoBillingSeconds(t *testing.T) {
	if got := effectiveVideoBillingSeconds(5, 5.166666666666667); math.Abs(got-5.166666666666667) > 1e-9 {
		t.Fatalf("有交付口径时应使用交付值, got %v", got)
	}
	if got := effectiveVideoBillingSeconds(5, 0); got != 5 {
		t.Fatalf("没有交付口径时应退回请求值, got %v", got)
	}
}
