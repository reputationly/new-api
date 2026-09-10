package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
)

// n 作为价格倍率必须在成功与产物拦截两条路上算得一模一样。
//
// 这两条路在 ImageHelper 里隔着一个 return，各算各的时会悄悄分叉：
// 同一个请求，审核触发了按 1 张收费、没触发按 n 张收费。钱随「审核是否触发」
// 而变，用户看到的是同样的 400，账单却不同——而且少收的那一半没有任何报错。
func TestApplyImageNRatio(t *testing.T) {
	n := uint(4)

	t.Run("价格计费按 n 登记倍率", func(t *testing.T) {
		info := &relaycommon.RelayInfo{PriceData: types.PriceData{UsePrice: true}}
		got := applyImageNRatio(info, &dto.ImageRequest{N: &n})
		if got != 4 {
			t.Fatalf("应返回 n=4，得到 %d", got)
		}
		if info.PriceData.OtherRatios["n"] != 4 {
			t.Fatalf("价格倍率 n 未登记：%+v", info.PriceData.OtherRatios)
		}
	})

	t.Run("不传 n 按 1 张", func(t *testing.T) {
		info := &relaycommon.RelayInfo{PriceData: types.PriceData{UsePrice: true}}
		if got := applyImageNRatio(info, &dto.ImageRequest{}); got != 1 {
			t.Fatalf("默认应为 1 张，得到 %d", got)
		}
		if info.PriceData.OtherRatios["n"] != 1 {
			t.Fatalf("默认也要登记 n=1：%+v", info.PriceData.OtherRatios)
		}
	})

	t.Run("适配器已给出更准的张数则不覆盖", func(t *testing.T) {
		// 上游回执里的真实张数比客户端请求的更可信（可能因安全过滤少给）。
		info := &relaycommon.RelayInfo{PriceData: types.PriceData{UsePrice: true}}
		info.PriceData.AddOtherRatio("n", 2)
		applyImageNRatio(info, &dto.ImageRequest{N: &n})
		if info.PriceData.OtherRatios["n"] != 2 {
			t.Fatalf("适配器设过的 n 不该被请求值覆盖，得到 %v", info.PriceData.OtherRatios["n"])
		}
	})

	t.Run("非价格计费不登记倍率", func(t *testing.T) {
		// 倍率计费路径 n 已经体现在 token 用量里，再乘一次就是重复计费。
		info := &relaycommon.RelayInfo{PriceData: types.PriceData{UsePrice: false}}
		if got := applyImageNRatio(info, &dto.ImageRequest{N: &n}); got != 4 {
			t.Fatalf("返回值与计费模式无关，应为 4，得到 %d", got)
		}
		if _, has := info.PriceData.OtherRatios["n"]; has {
			t.Fatal("非价格计费不该登记 n 倍率——那会导致重复计费")
		}
	})
}

// 拦截分支必须调用同一个函数。这条没法从行为侧观测：分支里少了这一次调用，
// 单测里看到的仍是「拦截了、结算了」，只是金额悄悄少了 n 倍。
func TestBlockedBranchAppliesSameNRatio(t *testing.T) {
	src := readRelaySource(t, "image_handler.go")
	branch := sliceBetween(t, src,
		"if relaycommon.IsOutputModerationBlocked(c) {", "// reset status code")
	if !contains(branch, "applyImageNRatio(info, request)") {
		t.Fatal("产物拦截分支没有应用 n 倍率——n>1 时会只按 1 张收费，" +
			"金额随「审核是否触发」而变")
	}
}
