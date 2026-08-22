package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

// IsTaskPerCallBilling 判定任务是否「按次/按个」计费。
//
// **这是唯一的判定入口**：轮询期是否跳过差额结算（TaskBillingContext.PerCallBilling）、
// 消费日志是否标 count_billing（对账据此按「个」计数），都必须走它。
//
// 此前这套逻辑在 controller 与 service 各写了一份、靠注释约束「保持一致」，
// 结果给视频矩阵改了 controller 那一份、漏了这一份，对账把一单 20 万 token 的
// 视频任务算成了 1 个计件。收成一个函数就消掉了这个约束本身。
//
// token 模式的视频矩阵必须走轮询期差额结算，故两个来源都要屏蔽：
//   - PriceData.UsePrice —— 已由 applyVideoPricing 在提交侧清零，这里是纵深
//   - TaskPricePatches   —— 环境变量 TASK_PRICE_PATCH 里的模型名单
func IsTaskPerCallBilling(info *relaycommon.RelayInfo) bool {
	if info == nil {
		return false
	}
	if info.TaskRelayInfo != nil && info.VideoBilling != nil &&
		info.VideoBilling.Mode == ratio_setting.VideoPriceModeToken {
		return false
	}
	return common.StringsContains(constant.TaskPricePatches, info.OriginModelName) ||
		info.PriceData.UsePrice
}

// IsDeferredUsageBilling 判定该任务是否走「上游返回用量计费」——提交时不记账，
// 完成拿到真实用量后记一次终值。
//
// 只对命中 token 矩阵的任务启用。那类任务的实收由上游返回的 token 数决定，提交时
// 只能按锚点预扣一个偏保守的数（实测预扣 ¥12.75 / 实收 ¥2.33）。若照常在提交时记账，
// 使用日志会出现「一条 ¥12.75 消费 + 一条 ¥10.42 退款」这种要做减法才看得懂的组合，
// 而供应商侧同一单只有一条 ¥2.33。
//
// **预扣本身照做**——它是唯一的余额闸门，拆掉它余额为 0 的用户能无限提交，
// 几百秒后才发现收不到钱。这里改的只是记账时机，不是资金动作。
//
// 不覆盖 midjourney / suno / sora 等既有 task：它们的预扣与实收本就接近，
// 且改动会波及既有对账口径。
func IsDeferredUsageBilling(info *relaycommon.RelayInfo) bool {
	if info == nil || info.TaskRelayInfo == nil || info.VideoBilling == nil {
		return false
	}
	return info.VideoBilling.Mode == ratio_setting.VideoPriceModeToken
}

// isDeferredTaskUsageBilling 是 IsDeferredUsageBilling 的结算侧版本。
// 判据必须与它严格一致——提交时按哪个口径跳过记账，完成时就得按哪个口径补记，
// 两边不一致会造成「记两次」或「一次都不记」。
func isDeferredTaskUsageBilling(task *model.Task) bool {
	bc := task.PrivateData.BillingContext
	return bc != nil && bc.VideoBilling != nil &&
		bc.VideoBilling.Mode == ratio_setting.VideoPriceModeToken
}

// fillVideoBillingOther 把视频计费矩阵命中的那一格写进日志 other。
//
// 提交侧的消费日志（LogTaskConsumption）与结算/退款日志（taskBillingOther）都要填：
// 差额为 0 时不写结算日志、per_call 模式压根不走结算，那些情况下主消费记录是
// 唯一的记录。缺了这组字段，前端 usage-log 的矩阵分支不会触发，会退回去显示一个
// 没参与计算的 model_ratio。
func fillVideoBillingOther(other map[string]interface{}, mode, resolution string, unitPrice float64, hasVideoInput bool, seconds int) {
	if mode == "" {
		return
	}
	other["video_price_mode"] = mode
	other["video_unit_price"] = unitPrice
	other["video_resolution"] = resolution
	other["video_has_input"] = hasVideoInput
	if seconds > 0 {
		other["video_seconds"] = seconds
	}
}

// LogTaskConsumption 记录任务消费日志和统计信息（仅记录，不涉及实际扣费）。
// 实际扣费已由 BillingSession（PreConsumeBilling + SettleBilling）完成。
func LogTaskConsumption(c *gin.Context, info *relaycommon.RelayInfo) {
	tokenName := c.GetString("token_name")
	logContent := fmt.Sprintf("操作 %s", info.Action)
	// 支持任务仅按次计费
	if common.StringsContains(constant.TaskPricePatches, info.OriginModelName) {
		logContent = fmt.Sprintf("%s，按次计费", logContent)
	} else {
		if len(info.PriceData.OtherRatios) > 0 {
			var contents []string
			for key, ra := range info.PriceData.OtherRatios {
				if 1.0 != ra {
					contents = append(contents, fmt.Sprintf("%s: %.2f", key, ra))
				}
			}
			if len(contents) > 0 {
				logContent = fmt.Sprintf("%s, 计算参数：%s", logContent, strings.Join(contents, ", "))
			}
		}
	}
	other := make(map[string]interface{})
	other["is_task"] = true
	// 仅「按次/按个」任务才按「个」计费，需打 count_billing 供对账归类
	// （reconcile_helpers.go 据此把整单算作 TokensCount=1）。token 计费任务必须保留
	// token 用量，不能标计件——判定与 TaskBillingContext.PerCallBilling 同源。
	if IsTaskPerCallBilling(info) {
		other["count_billing"] = true
	}
	if info.TaskRelayInfo != nil && info.VideoBilling != nil {
		vb := info.VideoBilling
		fillVideoBillingOther(other, vb.Mode, vb.Resolution, vb.UnitPrice, vb.HasVideoInput, vb.Seconds)
	}
	other["request_path"] = c.Request.URL.Path
	other["model_price"] = info.PriceData.ModelPrice
	if info.PriceData.ModelRatio > 0 {
		other["model_ratio"] = info.PriceData.ModelRatio
	}
	other["group_ratio"] = info.PriceData.GroupRatioInfo.GroupRatio
	if info.PriceData.GroupRatioInfo.HasSpecialRatio {
		other["user_group_ratio"] = info.PriceData.GroupRatioInfo.GroupSpecialRatio
	}
	if info.IsModelMapped {
		other["is_model_mapped"] = true
		other["upstream_model_name"] = info.UpstreamModelName
	}
	model.RecordConsumeLog(c, info.UserId, model.RecordConsumeLogParams{
		ChannelId: info.ChannelId,
		ModelName: info.OriginModelName,
		TokenName: tokenName,
		Quota:     info.PriceData.Quota,
		// 调用方（controller/relay.go）先 SettleBilling 后记日志，混扣积分量已就绪
		PointsConsumed: info.PointsConsumed,
		Content:        logContent,
		TokenId:        info.TokenId,
		Group:          info.UsingGroup,
		Other:          other,
	})
	model.UpdateUserUsedQuotaAndRequestCount(info.UserId, info.PriceData.Quota)
	model.UpdateChannelUsedQuota(info.ChannelId, info.PriceData.Quota)
}

// ---------------------------------------------------------------------------
// 异步任务计费辅助函数
// ---------------------------------------------------------------------------

// resolveTokenKey 通过 TokenId 运行时获取令牌 Key（用于 Redis 缓存操作）。
// 如果令牌已被删除或查询失败，返回空字符串。
func resolveTokenKey(ctx context.Context, tokenId int, taskID string) string {
	token, err := model.GetTokenById(tokenId)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("获取令牌 key 失败 (tokenId=%d, task=%s): %s", tokenId, taskID, err.Error()))
		return ""
	}
	return token.Key
}

// taskIsSubscription 判断任务是否通过订阅计费。
func taskIsSubscription(task *model.Task) bool {
	return task.PrivateData.BillingSource == BillingSourceSubscription && task.PrivateData.SubscriptionId > 0
}

// taskAdjustFunding 调整任务的资金来源（钱包/订阅/混扣），delta > 0 表示扣费，delta < 0 表示退还。
func taskAdjustFunding(task *model.Task, delta int) error {
	if taskIsSubscription(task) {
		return model.PostConsumeUserSubscriptionDelta(task.PrivateData.SubscriptionId, int64(delta))
	}
	// 混扣任务：按提交时持久化的积分拆分原路调整（codex review 第九轮）。
	// 否则积分实付的任务失败后全额退进钱包——营销积分被洗成真实余额（套利通道）；
	// 重算补扣也会漏掉积分优先。调用方随后的 task.Update() 会持久化拆分变更。
	if task.PrivateData.BillingSource == BillingSourceHybrid {
		return taskAdjustHybridFunding(task, delta)
	}
	if delta > 0 {
		return model.DecreaseUserQuota(task.UserId, delta, false)
	}
	return model.IncreaseUserQuota(task.UserId, -delta, false)
}

// taskAdjustHybridFunding 混扣任务的轮询期资金调整，语义与同步路径 HybridFunding 对齐：
//   - 退款（delta<0）：钱包优先退（真钱保护，与 Settle 负分支同策略），积分部分以提交时
//     实付 PointsConsumed 封顶原路退（db=true 直写）——全额退款自然还原为精确拆分；
//   - 补扣（delta>0）：复用 HybridFunding.Settle（积分优先重试、钱包无条件兜底——
//     服务已交付语义），并同步 PointsUsed 与持久化拆分。
func taskAdjustHybridFunding(task *model.Task, delta int) error {
	pc := task.PrivateData.PointsConsumed
	if delta < 0 {
		refund := -delta
		// 调用时 task.Quota 仍是已计费额（重算在资金调整成功后才改写 task.Quota）
		wc := max(task.Quota-pc, 0)
		pRefund := min(max(refund-wc, 0), pc) // 钱包份额之外的部分退积分，以实付封顶
		wRefund := refund - pRefund
		if wRefund > 0 {
			if err := model.IncreaseUserQuota(task.UserId, wRefund, false); err != nil {
				return err
			}
		}
		if pRefund > 0 {
			if err := model.IncreaseUserPoints(task.UserId, pRefund, true); err != nil {
				return err
			}
			task.PrivateData.PointsConsumed = pc - pRefund
		}
		return nil
	}
	h := &HybridFunding{userId: task.UserId}
	if err := h.Settle(delta); err != nil {
		return err
	}
	if p := h.PointsConsumed(); p > 0 {
		task.PrivateData.PointsConsumed = pc + p
		if err := model.AddUserPointsUsed(task.UserId, p); err != nil {
			common.SysLog("failed to add user points used for task recalc: " + err.Error())
		}
	}
	return nil
}

// taskAdjustTokenQuota 调整任务的令牌额度，delta > 0 表示扣费，delta < 0 表示退还。
// 需要通过 resolveTokenKey 运行时获取 key（不从 PrivateData 中读取）。
func taskAdjustTokenQuota(ctx context.Context, task *model.Task, delta int) {
	if task.PrivateData.TokenId <= 0 || delta == 0 {
		return
	}
	tokenKey := resolveTokenKey(ctx, task.PrivateData.TokenId, task.TaskID)
	if tokenKey == "" {
		return
	}
	var err error
	if delta > 0 {
		err = model.DecreaseTokenQuota(task.PrivateData.TokenId, tokenKey, delta)
	} else {
		err = model.IncreaseTokenQuota(task.PrivateData.TokenId, tokenKey, -delta)
	}
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("调整令牌额度失败 (delta=%d, task=%s): %s", delta, task.TaskID, err.Error()))
	}
}

// taskBillingOther 从 task 的 BillingContext 构建日志 Other 字段。
func taskBillingOther(task *model.Task) map[string]interface{} {
	other := make(map[string]interface{})
	if bc := task.PrivateData.BillingContext; bc != nil {
		other["model_price"] = bc.ModelPrice
		if bc.ModelRatio > 0 {
			other["model_ratio"] = bc.ModelRatio
		}
		other["group_ratio"] = bc.GroupRatio
		if len(bc.OtherRatios) > 0 {
			for k, v := range bc.OtherRatios {
				other[k] = v
			}
		}
		// 视频计费矩阵命中时把查到哪一格也记进日志——否则运营对着一条金额
		// 无从判断是取了 720p 还是 1080p、含不含视频输入，对账就没法追。
		//
		// 单价为 0 = 提交时定不出档位、上游回执也没能补上（见 applyVideoPricing 的
		// 未命中分支），这一单的金额其实是 token 重算或兜底算出来的，没走矩阵。
		// 此时**不能**写这组字段：前端只按 video_price_mode 是否存在就切到矩阵展示，
		// 会渲染出一条算得 0 的算式，还把真正参与计算的 model_ratio 那行屏蔽掉。
		if vb := bc.VideoBilling; vb != nil && vb.UnitPrice > 0 {
			fillVideoBillingOther(other, vb.Mode, vb.Resolution, vb.UnitPrice, vb.HasVideoInput, vb.Seconds)
		}
	}
	props := task.Properties
	if props.UpstreamModelName != "" && props.UpstreamModelName != props.OriginModelName {
		other["is_model_mapped"] = true
		other["upstream_model_name"] = props.UpstreamModelName
	}
	// 只有按次/按个任务才标计件。token 计费任务（PerCallBilling=false，差额结算走
	// RecalculateTaskQuotaByTokens）保留 token 用量，避免对账把它误当 1 个计件。
	// BillingContext 缺失时无从判定，保守沿用旧行为（视为按次）。
	if bc := task.PrivateData.BillingContext; bc == nil || bc.PerCallBilling {
		other["count_billing"] = true
	}
	return other
}

// taskModelName 从 BillingContext 或 Properties 中获取模型名称。
func taskModelName(task *model.Task) string {
	if bc := task.PrivateData.BillingContext; bc != nil && bc.OriginModelName != "" {
		return bc.OriginModelName
	}
	return task.Properties.OriginModelName
}

// RefundTaskQuota 统一的任务失败退款逻辑。
// 当异步任务失败时，将预扣的 quota 退还给用户（支持钱包和订阅），并退还令牌额度。
func RefundTaskQuota(ctx context.Context, task *model.Task, reason string) {
	quota := task.Quota
	if quota == 0 {
		return
	}

	// 1. 退还资金来源（钱包或订阅）
	if err := taskAdjustFunding(task, -quota); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("退还资金来源失败 task %s: %s", task.TaskID, err.Error()))
		return
	}

	// 2. 退还令牌额度
	taskAdjustTokenQuota(ctx, task, -quota)

	// 3. 「上游返回用量计费」的任务提交时没记过账，全额退款后净消费为 0——
	//    既不用回冲统计，也不该写日志。写了就是一条没有对应消费行的孤儿退款，
	//    比这次要消除的「两行」还难懂。任务本身的失败状态在 tasks 表里有记录。
	if isDeferredTaskUsageBilling(task) {
		logger.LogInfo(ctx, fmt.Sprintf("任务 %s 失败退款 %s（延迟记账，未产生消费记录）：%s",
			task.TaskID, logger.LogQuota(quota), reason))
		return
	}

	// 4. 回冲已用额度统计。提交时 LogTaskConsumption 按预扣额记过一次，任务失败全额
	//    退款后这笔消费实际为 0，不冲的话用户看到的「已用额度」会把失败的单也算进去。
	//    次数不回冲——请求确实发生过。
	model.UpdateUserUsedQuotaOnly(task.UserId, -quota)
	model.UpdateChannelUsedQuota(task.ChannelId, -quota)

	// 5. 记录日志
	other := taskBillingOther(task)
	other["task_id"] = task.TaskID
	other["reason"] = reason
	model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:    task.UserId,
		LogType:   model.LogTypeRefund,
		Content:   "",
		ChannelId: task.ChannelId,
		ModelName: taskModelName(task),
		Quota:     quota,
		TokenId:   task.PrivateData.TokenId,
		TokenName: task.PrivateData.TokenName,
		Group:     task.Group,
		Other:     other,
	})
}

// RecalculateTaskQuota 通用的异步差额结算。
// actualQuota 是任务完成后的实际应扣额度，与预扣额度 (task.Quota) 做差额结算。
// reason 用于日志记录（例如 "token重算" 或 "adaptor调整"）。
func RecalculateTaskQuota(ctx context.Context, task *model.Task, actualQuota int, reason string) {
	recalculateTaskQuota(ctx, task, actualQuota, 0, reason)
}

// recalculateTaskQuota 带用量的版本。
//
// completionTokens 是上游返回的用量，会写进日志的「输出」列。按 token 计费的任务
// 必须传：不传的话日志里只有一个孤零零的金额，前端拼不出
// 「tokens × 单价 × 倍率 = 金额」这条算式，对账时无法与供应商明细逐项核对。
// 0 表示不适用（按次计费等）。
//
// 返回是否真的结算了。返回 false 时调用方须继续找别的结算路径或走兜底——
// 静默失败会让「上游返回用量计费」的任务既不退预扣也不记账。
func recalculateTaskQuota(ctx context.Context, task *model.Task, actualQuota, completionTokens int, reason string) bool {
	deferred := isDeferredTaskUsageBilling(task)

	// actualQuota == 0 对延迟记账是**合法结果**：免费分组（groupRatio=0），
	// 或极小额被 int() 截断。此时仍要退还预扣、并记一条 0 元消费——
	// 早退的话免费任务永远不出现在日志里，被截断的那种还会白留一笔预扣。
	//
	// 非延迟记账保持既有语义（0 = 无调整），不动 sora / midjourney 那些渠道。
	if actualQuota < 0 || (actualQuota == 0 && !deferred) {
		return false
	}
	preConsumedQuota := task.Quota
	quotaDelta := actualQuota - preConsumedQuota

	// 延迟记账的任务提交时没写过日志，即便预扣恰好等于实收也必须在这里补记一条，
	// 否则这一单在使用日志里完全不存在。
	if quotaDelta == 0 && !deferred {
		logger.LogInfo(ctx, fmt.Sprintf("任务 %s 预扣费准确（%s，%s）",
			task.TaskID, logger.LogQuota(actualQuota), reason))
		return true
	}

	logger.LogInfo(ctx, fmt.Sprintf("任务 %s 差额结算：delta=%s（实际：%s，预扣：%s，%s）",
		task.TaskID,
		logger.LogQuota(quotaDelta),
		logger.LogQuota(actualQuota),
		logger.LogQuota(preConsumedQuota),
		reason,
	))

	// 调整资金来源。预扣是真实发生过的，延迟记账也一样要多退少补。
	if quotaDelta != 0 {
		if err := taskAdjustFunding(task, quotaDelta); err != nil {
			logger.LogError(ctx, fmt.Sprintf("差额结算资金调整失败 task %s: %s", task.TaskID, err.Error()))
			return false
		}
	}

	// 调整令牌额度
	taskAdjustTokenQuota(ctx, task, quotaDelta)

	task.Quota = actualQuota

	var logType int
	var logQuota int
	// 积分实付只跟着「整单终值」那条日志走。差额日志的 Quota 是增量，挂上整单的
	// 积分数对不上；而延迟记账下这一条就是整单，不填的话混扣的视频单在使用日志里
	// 一律显示成纯余额扣费（前端按 points_consumed>0 才显示「积分抵扣」标签）。
	//
	// 取值放在 taskAdjustFunding 之后：多退少补会改写 PointsConsumed（退款按实付
	// 封顶原路退、补扣走积分优先），这里要的是调整**之后**的实付额。
	var logPoints int
	if deferred {
		// 「上游返回用量计费」：提交时既没记 used_quota 也没记次数，这里一次记终值。
		// 使用日志里这一单因此只有一条、金额就是实收，与供应商账单同形。
		model.UpdateUserUsedQuotaAndRequestCount(task.UserId, actualQuota)
		model.UpdateChannelUsedQuota(task.ChannelId, actualQuota)
		logType = model.LogTypeConsume
		logQuota = actualQuota
		logPoints = task.PrivateData.PointsConsumed
	} else {
		// 差额结算只调整**额度**，不碰请求次数——那一次请求在提交时（LogTaskConsumption）
		// 已经计过数了。用 UpdateUserUsedQuotaAndRequestCount 会把同一次请求计成两次。
		//
		// 回冲同样必须做：提交时按**预扣额**记的 used_quota，实收更低不冲的话，
		// 用户/渠道的「已用额度」永远停在预扣值上。余额本身由 taskAdjustFunding 调过了，
		// 这里修的是统计口径。
		model.UpdateUserUsedQuotaOnly(task.UserId, quotaDelta)
		model.UpdateChannelUsedQuota(task.ChannelId, quotaDelta)

		if quotaDelta > 0 {
			logType = model.LogTypeConsume
			logQuota = quotaDelta
		} else {
			logType = model.LogTypeRefund
			logQuota = -quotaDelta
		}
	}
	other := taskBillingOther(task)
	other["task_id"] = task.TaskID
	other["pre_consumed_quota"] = preConsumedQuota
	other["actual_quota"] = actualQuota
	model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId:           task.UserId,
		LogType:          logType,
		Content:          reason,
		ChannelId:        task.ChannelId,
		ModelName:        taskModelName(task),
		Quota:            logQuota,
		TokenId:          task.PrivateData.TokenId,
		TokenName:        task.PrivateData.TokenName,
		Group:            task.Group,
		CompletionTokens: completionTokens,
		PointsConsumed:   logPoints,
		Other:            other,
	})
	return true
}

// RecalculateTaskQuotaByTokens 根据实际 token 消耗重新计费（异步差额结算）。
// 当任务成功且返回了 totalTokens 时，根据模型倍率和分组倍率重新计算实际扣费额度，
// 与预扣费的差额进行补扣或退还。支持钱包和订阅计费来源。
// 返回是否真的记了账。调用方据此决定要不要走延迟记账的兜底——静默 return 会让
// 「上游返回用量计费」的任务在使用日志里完全不存在，而预扣的钱已经扣了。
func RecalculateTaskQuotaByTokens(ctx context.Context, task *model.Task, totalTokens int) bool {
	if totalTokens <= 0 {
		return false
	}

	modelName := taskModelName(task)

	// 获取模型价格和倍率
	modelRatio, hasRatioSetting, _ := ratio_setting.GetModelRatio(modelName)
	// 只有配置了倍率(非固定价格)时才按 token 重新计费
	if !hasRatioSetting || modelRatio <= 0 {
		return false
	}

	finalGroupRatio, ok := taskGroupRatio(task)
	if !ok {
		return false
	}

	// 计算 OtherRatios 乘积（视频折扣、时长等）
	otherMultiplier := 1.0
	if bc := task.PrivateData.BillingContext; bc != nil {
		for _, r := range bc.OtherRatios {
			if r != 1.0 && r > 0 {
				otherMultiplier *= r
			}
		}
	}

	// 计算实际应扣费额度: totalTokens * modelRatio * groupRatio * otherMultiplier
	actualQuota := int(float64(totalTokens) * modelRatio * finalGroupRatio * otherMultiplier)

	reason := fmt.Sprintf("token重算：tokens=%d, modelRatio=%.2f, groupRatio=%.2f, otherMultiplier=%.4f", totalTokens, modelRatio, finalGroupRatio, otherMultiplier)
	return recalculateTaskQuota(ctx, task, actualQuota, totalTokens, reason)
}

// DeferredBillingFallback 「上游返回用量计费」的兜底：结算没走到任何记账分支时，
// 按预扣额补记一条消费。
//
// 这条兜底是必需的。延迟记账把「记账」推到了完成时，而 settleTaskBillingOnComplete
// 有多个静默 return 的分支（见 docs/video-billing-matrix-design.md §2.4 的守卫清单）。
// 任何一个分支走掉，这一单在使用日志里就完全不存在——但预扣的钱已经扣了。
// 用户看到余额少了、日志里查无此单，是最糟的一种失败。
//
// 非延迟记账的任务无事发生：它们在提交时已经记过账。
func DeferredBillingFallback(ctx context.Context, task *model.Task, reason string) {
	if !isDeferredTaskUsageBilling(task) {
		return
	}
	RecalculateTaskQuota(ctx, task, task.Quota, "延迟记账兜底："+reason)
}

// taskGroupRatio 解析任务的最终分组倍率。
//
// **优先用提交时冻结的值**，理由与 RecalculateTaskQuotaByVideoMatrix 里那段注释
// 完全相同（跨分组信息会丢、期间改配置会前后两个价、日志反算要自洽），而这三条
// 对模型级折扣只会更成立：促销规则的改动频率天然高于分组基础倍率。
//
// 改前这里是**结算时重新解析**，与视频矩阵那条路径用冻结值不一致——同一个任务
// 走 token 重算还是走矩阵，管理员在执行期间改了倍率就会得到两个价。这次一并修掉。
//
// 回退分支只服务没有 BillingContext 的老任务：那时无从得知提交时的倍率，
// 重新解析是唯一选择。此时传入 taskModelName(task) 让模型级折扣至少能生效，
// 但它算的是**当前**配置，与预扣可能对不上——这是老数据的固有损失，不是本次引入。
func taskGroupRatio(task *model.Task) (float64, bool) {
	if bc := task.PrivateData.BillingContext; bc != nil && bc.GroupRatio > 0 {
		return bc.GroupRatio, true
	}

	group := task.Group
	if group == "" {
		user, err := model.GetUserById(task.UserId, false)
		if err == nil {
			group = user.Group
		}
	}
	if group == "" {
		return 0, false
	}
	// 这里把使用分组同时当用户分组传，是沿袭改造前的既有行为（查不到就退回基础倍率），
	// 不借重构改语义。
	return ratio_setting.ResolveGroupRatio(group, group, taskModelName(task)).Final, true
}

// RecalculateTaskQuotaByVideoMatrix 按提交时冻结的视频计费矩阵单价做差额结算。
// 设计见 docs/video-billing-matrix-design.md。
//
//	quota = tokens ÷ 1e6 × 单价($/百万 tokens) × QuotaPerUnit × groupRatio
//
// 与 RecalculateTaskQuotaByTokens 的三点区别，都是刻意的：
//   - **不读 GetModelRatio**：模型可能配的是固定价格（hasRatioSetting=false），
//     那条路径会直接 return，导致 480p 与 1080p 收一样的钱。
//   - **不乘 OtherRatios**：矩阵单价已是终价，再乘一遍适配器的 video_input 折扣是二次计费。
//   - **不碰汇率**：单价是美元，货币换算只发生在管理端编辑器里。
//
// upstreamResolution 是上游回执里的**实际**出片档位，只在提交时没查到单价时用到。
func RecalculateTaskQuotaByVideoMatrix(ctx context.Context, task *model.Task, totalTokens int, upstreamResolution string) bool {
	if totalTokens <= 0 {
		return false
	}
	bc := task.PrivateData.BillingContext
	if bc == nil || bc.VideoBilling == nil {
		return false
	}
	vb := bc.VideoBilling
	if vb.Mode != ratio_setting.VideoPriceModeToken {
		return false
	}
	if vb.UnitPrice <= 0 && !resolveUnitPriceFromUpstream(ctx, task, vb, upstreamResolution) {
		return false
	}

	// 用**提交时冻结**的分组倍率，不重新解析。三个理由：
	//  1. 重新解析会丢信息：提交时用的是 GetGroupGroupRatio(用户分组, 使用分组)，
	//     而这里只有 task.Group（= 使用分组），拿它当两个参数传会漏掉
	//     「用户分组 × 使用分组」的特殊倍率，跨分组用户的结算金额与预扣对不上。
	//  2. 结算发生在几百秒后，其间管理员改了倍率配置就会让同一单前后两个价。
	//  3. 日志里 other["group_ratio"] 写的就是这个冻结值，用它才能自洽——
	//     否则运营按日志上的倍率反算金额永远对不上。
	//
	// 不做「为 0 就回退重查」的兜底：VideoBilling 只由本次新增的冻结逻辑写入，
	// 那条路径上 GroupRatio 必然同时落库（controller/relay.go），所以 0 一定是
	// 「免费分组」这个合法值（见 ModelPriceHelperPerCall 的 groupRatio==0 免费分支），
	// 不是「没冻结过」。当成缺失去重查会把 GroupGroupRatio 里的 0 折算回 1.0，
	// 对一单提交时免费的任务按原价补扣。
	finalGroupRatio := bc.GroupRatio
	if finalGroupRatio < 0 {
		return false
	}

	actualQuota := int(float64(totalTokens) / 1e6 * vb.UnitPrice * common.QuotaPerUnit * finalGroupRatio)

	reason := fmt.Sprintf("视频矩阵重算：tokens=%d, %s/%s, unitPrice=%g, groupRatio=%.2f",
		totalTokens, vb.Resolution, videoInputLabel(vb.HasVideoInput), vb.UnitPrice, finalGroupRatio)
	// 透传是否真的结算了：算出 0 且非延迟记账时 recalculateTaskQuota 会拒绝，
	// 无条件 return true 会让调用方误以为结算完了，跳过兜底。
	return recalculateTaskQuota(ctx, task, actualQuota, totalTokens, reason)
}

// resolveUnitPriceFromUpstream 用上游回执里的实际分辨率补查矩阵单价，命中则就地
// 回写冻结值并返回 true。
//
// 提交时查不到单价是**常态**而非异常：图生视频、参考生视频按设计不下发 size
// （画幅跟随输入图），而供应商的 resolution 本就是可选参数、ratio=adaptive 时画幅
// 跟随输入、draft 还会强制 480p。这些玩法在提交时根本不存在「期望档位」这个东西。
//
// 回写 vb 而不是只用局部变量：taskBillingOther 从 BillingContext 取分辨率与单价写进
// 日志，不回写的话日志里会是一个空分辨率 + 0 单价，前端算式拼不出来、对账也追不到
// 是哪一格。资金动作用的也是同一份值，两者因此自洽。
func resolveUnitPriceFromUpstream(ctx context.Context, task *model.Task, vb *model.TaskVideoBilling, upstreamResolution string) bool {
	if strings.TrimSpace(upstreamResolution) == "" {
		return false
	}
	entry, ok := ratio_setting.GetVideoPricing(taskModelName(task))
	if !ok {
		return false
	}
	price, hit := entry.LookupToken(upstreamResolution, vb.HasVideoInput)
	if !hit {
		// 最常见的成因是矩阵里 with_video 那一列没配：lookupCell 对 0 一律当「未配置」，
		// 于是同一个模型不传参考视频时正常结算、一传就掉队。喊出来，别静默按预扣收费。
		logger.LogWarn(ctx, fmt.Sprintf(
			"任务 %s 按上游回执分辨率 %s/%s 仍查不到矩阵单价，请检查该模型这一格是否配价。",
			task.TaskID, upstreamResolution, videoInputLabel(vb.HasVideoInput)))
		return false
	}
	vb.Resolution = upstreamResolution
	vb.UnitPrice = price
	return true
}

func videoInputLabel(hasVideo bool) string {
	if hasVideo {
		return ratio_setting.VideoPriceKeyWithVideo
	}
	return ratio_setting.VideoPriceKeyWithoutVideo
}
