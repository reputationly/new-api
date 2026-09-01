package relay

// 视频计费矩阵的提交侧接线。设计见 docs/video-billing-matrix-design.md。
//
// 供应商对 Seedance 这类模型按 token 计费,单价由 (分辨率, 输入是否含视频) 决定;
// 我们此前只有 doubao 适配器里一张写死的 videoInputRatioMap,没有分辨率维度,
// 1080p 一律少收约 10%。矩阵改由运营在后台配置。

import (
	"context"
	"fmt"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// videoBillingSeconds 解析计费用的秒数,按**渠道**决定认不认 Seconds 字段。
//
// 各渠道对 Seconds 的读法不一致,而计费用的秒数必须等于上游实际生成的秒数:
//
//	gpustackplus(自建)  adaptor.go:513-517  Duration 为 0 时回落 Seconds
//	kling                adaptor.go:271      DefaultInt(req.Duration, 5),忽略 Seconds
//	vidu                 adaptor.go:232      同上
//	jimeng               adaptor.go:387      switch req.Duration,同上
//
// 不跟自建那条回落 = 引擎按 10 秒出片、计费拿到 0 秒、矩阵未命中、按固定价收;
// 跟了 kling 那条 = 按 10 秒收费、上游只生成 5 秒。两个方向都是错账。
//
// 判据用 GetTaskPlatform——它就是 RelayTaskSubmit 选适配器用的那个函数,同源就
// 不会分叉。**不能**用引擎族(VideoEngineFamilyForModel):wan2.2 系列同样走
// gpustackplus 的回落,却没有声明 engine,按引擎族白名单会漏掉整个系列。
//
// 渠道认不出来时退回保守口径(只认 Duration):未命中矩阵、走旧计费路径,不会多收。
func videoBillingSeconds(c *gin.Context, req *relaycommon.TaskSubmitReq, seconds int) int {
	if seconds > 0 {
		return seconds
	}
	if GetTaskPlatform(c) != constant.TaskPlatform(strconv.Itoa(constant.ChannelTypeGPUStackPlus)) {
		return 0
	}
	return relaycommon.VideoSecondsFallback(req)
}

// warnVideoMatrixMiss 在「矩阵配了、但这次请求查不到价」时打 WARN。
//
// 这类未命中会静默回退到固定 ModelPrice——设计文档 §6.0 说的「静默失效是本功能的
// 主要失败模式」,轮询期的 warnVideoMatrixSkipped 只覆盖 token 模式的结算侧,提交侧
// 一直没有兜底网。而 per_call / per_second 恰恰是提交时定死终价的,漏在这里就是
// 「按一口价收了、日志上看不出任何异常」。
//
// token 模式不在此列:它的未命中是**设计内**的(图生视频不下发 size,由结算侧按上游
// 回执补查),打 WARN 只会淹掉真正的问题。
//
// 只打日志不拦请求:拦了会把「运营漏配一档」变成「用户直接调不通」,而回退路径本身
// 是能正常出片的。
func warnVideoMatrixMiss(c *gin.Context, info *relaycommon.RelayInfo, mode, resolution string, seconds int) {
	// c.Request 可能为 nil(gin.CreateTestContext 默认不带 Request),而
	// (*http.Request).Context 不做接收者判空,直接取就是段错误。计费路径上任何一处
	// panic 都会把一次正常请求变成 500,为一行告警不值得。
	ctx := context.Background()
	if c != nil && c.Request != nil {
		ctx = c.Request.Context()
	}
	logger.LogWarn(ctx, fmt.Sprintf(
		"模型 %s 配了视频计费矩阵(%s),但本次请求查不到价(分辨率 %q、秒数 %d),"+
			"已回退固定价计费。请补配该档位或加一条 %q 兜底行。",
		info.OriginModelName, mode, resolution, seconds, ratio_setting.VideoPriceRowFallback))
}

// videoPerCallPriceable 判断「按次矩阵能否独立给这次请求定价」。
//
// per_call 与 per_second 能脱离 legacy 价格:两者的矩阵里都是终价(后者乘上秒数),
// 提交时即可定死。token 模式**仍需** ModelRatio 当预扣锚点——矩阵单价乘的是上游
// 返回的 token 数,提交时无从预估,没有锚点就等于预扣 0、不查余额,欠费用户也能随便刷。
//
// 供 RelayTaskSubmit 在 ModelPriceHelperPerCall 报「未配置价格」时决定是否放行。
func videoPerCallPriceable(c *gin.Context, info *relaycommon.RelayInfo) bool {
	if info == nil {
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
	resolution, seconds, _ := relaycommon.ResolveVideoDims(&req)
	seconds = videoBillingSeconds(c, &req, seconds)
	switch entry.Mode {
	case ratio_setting.VideoPriceModePerCall:
		_, hit := entry.LookupPerCall(resolution, seconds)
		return hit
	case ratio_setting.VideoPriceModePerSecond:
		// 秒数必须在这里也判:LookupPerSecond 只查分辨率,秒数缺失时它照样命中,
		// 放行之后 applyVideoPricing 才发现算不出价,变成「放过了预扣闸却没定价」。
		if seconds <= 0 {
			return false
		}
		_, hit := entry.LookupPerSecond(resolution)
		return hit
	default:
		return false
	}
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
	seconds = videoBillingSeconds(c, &req, seconds)

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
			// 提交时定不出档位——图生视频 / 参考生视频按设计就不下发 size(画幅跟随
			// 输入图),而 Ark 的 resolution 本就是可选参数、ratio=adaptive 时画幅跟随
			// 输入、draft 还会强制 480p。请求侧的期望值对这些玩法根本不存在。
			//
			// 仍然冻结 Mode=token:它才是 IsDeferredUsageBilling 的判据。不冻的话整单
			// 退回改造前的路径,使用日志又变回「一条消费 + 一条退款」,且按 ModelRatio
			// 而非矩阵单价收费——正是这次要消除的两个症状。单价留 0,由结算侧按上游
			// 回执的实际分辨率补查(RecalculateTaskQuotaByVideoMatrix)。
			//
			// 返回 false 是刻意的:单价未知时预扣必须继续走 EstimateBilling + OtherRatios
			// 那条老锚点,否则预扣会塌成一个不含时长维度的数,余额闸门形同虚设。
			frozen.UnitPrice = 0
			info.VideoBilling = frozen
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
			warnVideoMatrixMiss(c, info, entry.Mode, resolution, seconds)
			return false
		}
		frozen.UnitPrice = price
		// 按次:提交时即定死终价,轮询期不再差额结算。
		info.PriceData.UsePrice = true
		info.PriceData.ModelPrice = price
		info.PriceData.Quota = int(price * common.QuotaPerUnit * info.PriceData.GroupRatioInfo.GroupRatio)

	case ratio_setting.VideoPriceModePerSecond:
		// 秒数是价格的乘数,缺了就只能算出 0 元——那等于白送,必须回退旧路径。
		// LookupPerSecond 只查分辨率,拦不住这个,所以要在这里单独判。
		if seconds <= 0 {
			warnVideoMatrixMiss(c, info, entry.Mode, resolution, seconds)
			return false
		}
		unit, hit := entry.LookupPerSecond(resolution)
		if !hit {
			warnVideoMatrixMiss(c, info, entry.Mode, resolution, seconds)
			return false
		}
		// 冻结的 UnitPrice 是**每秒单价**而非总价:日志的 video_unit_price 与
		// video_seconds 一起下发,前端与对账靠 unit × seconds 反算,存总价就丢了
		// 「这一单为什么是这个数」的依据。总价只进 PriceData。
		frozen.UnitPrice = unit
		total := unit * float64(seconds)
		// 结算口径与 per_call 完全一致:提交时定死,轮询期不差额结算。
		// service/ 那 5 处 Mode 判断(task_billing.go:34/58/67/570、
		// task_polling.go:641)全是判 token,per_second 天然落进 per_call 这条路。
		info.PriceData.UsePrice = true
		info.PriceData.ModelPrice = total
		info.PriceData.Quota = int(total * common.QuotaPerUnit * info.PriceData.GroupRatioInfo.GroupRatio)

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
