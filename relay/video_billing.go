package relay

// 视频计费矩阵的提交侧接线。设计见 docs/video-billing-matrix-design.md。
//
// 供应商对 Seedance 这类模型按 token 计费,单价由 (分辨率, 输入是否含视频) 决定;
// 我们此前只有 doubao 适配器里一张写死的 videoInputRatioMap,没有分辨率维度,
// 1080p 一律少收约 10%。矩阵改由运营在后台配置。

import (
	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// videoPerCallPriceable 判断「按次矩阵能否独立给这次请求定价」。
//
// 只有 per_call 模式能脱离 legacy 价格:它的矩阵里就是终价,提交时即可定死。
// token 模式**仍需** ModelRatio 当预扣锚点——矩阵单价乘的是上游返回的 token 数,
// 提交时无从预估,没有锚点就等于预扣 0、不查余额,欠费用户也能随便刷。
//
// 供 RelayTaskSubmit 在 ModelPriceHelperPerCall 报「未配置价格」时决定是否放行。
func videoPerCallPriceable(c *gin.Context, info *relaycommon.RelayInfo) bool {
	if info == nil {
		return false
	}
	entry, ok := ratio_setting.GetVideoPricing(info.OriginModelName)
	if !ok || entry.Mode != ratio_setting.VideoPriceModePerCall {
		return false
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return false
	}
	resolution, seconds, _ := relaycommon.ResolveVideoDims(&req)
	_, hit := entry.LookupPerCall(resolution, seconds)
	return hit
}

// applyVideoPricing 查计费矩阵。命中返回 true,调用方据此**跳过**适配器的
// EstimateBilling 与 OtherRatios 应用——矩阵单价已是终价,再乘一遍硬编码的
// video_input 折扣就是二次计费。
//
// 任一环节未命中都返回 false,完全走改造前的路径。改动因此是惰性的:
// 配一个模型生效一个,没配的模型一个字节的行为都不变。
func applyVideoPricing(c *gin.Context, info *relaycommon.RelayInfo) bool {
	if info == nil || info.TaskRelayInfo == nil {
		return false
	}
	entry, ok := ratio_setting.GetVideoPricing(info.OriginModelName)
	if !ok {
		return false
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return false
	}
	resolution, seconds, hasVideoInput := relaycommon.ResolveVideoDims(&req)

	frozen := &relaycommon.VideoBillingContext{
		Mode:          entry.Mode,
		Resolution:    resolution,
		Seconds:       seconds,
		HasVideoInput: hasVideoInput,
	}

	switch entry.Mode {
	case ratio_setting.VideoPriceModeToken:
		price, hit := entry.LookupToken(resolution, hasVideoInput)
		if !hit {
			return false
		}
		frozen.UnitPrice = price
		// 预扣沿用现有的 ModelRatio 路径(结算是差额的,预扣准不准不影响最终金额),
		// 但必须强制关掉 UsePrice:它为 true 会让 PerCallBilling 也为 true,
		// 而轮询期的 settleTaskBillingOnComplete 第 0 步就会因此跳过全部差额结算,
		// 结果是 480p 与 1080p 收一样的钱——正是本次要修的那个 bug 的加强版。
		info.PriceData.UsePrice = false

	case ratio_setting.VideoPriceModePerCall:
		price, hit := entry.LookupPerCall(resolution, seconds)
		if !hit {
			return false
		}
		frozen.UnitPrice = price
		// 按次:提交时即定死终价,轮询期不再差额结算。
		info.PriceData.UsePrice = true
		info.PriceData.ModelPrice = price
		info.PriceData.Quota = int(price * common.QuotaPerUnit * info.PriceData.GroupRatioInfo.GroupRatio)

	default:
		return false
	}

	// 重算 FreeModel。矩阵能命中就意味着单价 > 0（Lookup 对 <=0 返回 false），
	// 所以这一单是否免费只取决于分组倍率。
	//
	// 必须重算：ModelPriceHelperPerCall 可能已按旧的 ModelPrice/ModelRatio == 0
	// 置了 FreeModel=true（relay/helper/price.go:196-215），而 relay_task.go 拿它
	// 当预扣闸。不重算的话——
	//   per_call：跳过预扣 + PerCallBilling 让结算早退 → **这一单永久免费**
	//   token  ：跳过预扣但结算照常补扣 → 收得到钱，但没查过余额，可透支
	info.PriceData.FreeModel = info.PriceData.GroupRatioInfo.GroupRatio <= 0

	info.VideoBilling = frozen
	return true
}
