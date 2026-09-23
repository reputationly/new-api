package service

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// BillingSession — 统一计费会话
// ---------------------------------------------------------------------------

// BillingSession 封装单次请求的预扣费/结算/退款生命周期。
// 实现 relaycommon.BillingSettler 接口。
type BillingSession struct {
	relayInfo        *relaycommon.RelayInfo
	funding          FundingSource
	preConsumedQuota int // 实际预扣额度（信任用户可能为 0）
	tokenConsumed    int // 令牌额度实际扣减量
	extraReserved    int // 发送前补充预扣的额度（订阅退款时需要单独回滚）
	// 权益追加预扣的批次拆分，与下面 Hybrid 那对同构
	lastReserveEntitlement []model.ComputePointSpend
	// Hybrid 追加预扣的积分/钱包拆分（reserveFunding 记录、rollbackFundingReserve
	// 精确原路回滚用；仅在 Reserve 持锁期间读写）
	lastReservePoints int
	lastReserveWallet int
	trusted           bool // 是否命中信任额度旁路
	fundingSettled    bool // funding.Settle 已成功，资金来源已提交
	settled           bool // Settle 全部完成（资金 + 令牌）
	refunded          bool // Refund 已调用
	mu                sync.Mutex
}

// Settle 根据实际消耗额度进行结算。
// 资金来源和令牌额度分两步提交：若资金来源已提交但令牌调整失败，
// 会标记 fundingSettled 防止 Refund 对已提交的资金来源执行退款。
func (s *BillingSession) Settle(actualQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled {
		return nil
	}
	delta := actualQuota - s.preConsumedQuota
	if delta == 0 {
		s.syncPointsConsumed()
		s.syncCreditConsumed(actualQuota)
		s.settled = true
		return nil
	}
	// 1) 调整资金来源（仅在尚未提交时执行，防止重复调用）
	if !s.fundingSettled {
		if err := s.funding.Settle(delta); err != nil {
			return err
		}
		s.fundingSettled = true
	}
	// 2) 调整令牌额度
	var tokenErr error
	if !s.relayInfo.IsPlayground {
		if delta > 0 {
			tokenErr = model.DecreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, delta)
		} else {
			tokenErr = model.IncreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, -delta)
		}
		if tokenErr != nil {
			// 资金来源已提交，令牌调整失败只能记录日志；标记 settled 防止 Refund 误退资金
			common.SysLog(fmt.Sprintf("error adjusting token quota after funding settled (userId=%d, tokenId=%d, delta=%d): %s",
				s.relayInfo.UserId, s.relayInfo.TokenId, delta, tokenErr.Error()))
		}
	}
	// 3) 更新 relayInfo 上的订阅 PostDelta / 权益实耗算力点（用于日志）。
	// 权益的点数在预扣时同步过一次，那是估算值；结算补扣或退还之后必须再同步，
	// 否则日志记的是预扣量而不是真正烧掉的点数。
	if s.funding.Source() == BillingSourceSubscription {
		s.relayInfo.SubscriptionPostDelta += int64(delta)
	}
	if ent, ok := s.funding.(*EntitlementFunding); ok {
		s.relayInfo.EntitlementPointsSpent = ent.PointsSpent()
	}
	s.syncPointsConsumed()
	s.syncCreditConsumed(actualQuota)
	s.settled = true
	return tokenErr
}

// Refund 退还所有预扣费，幂等安全，异步执行。
func (s *BillingSession) Refund(c *gin.Context) {
	s.mu.Lock()
	if s.settled || s.refunded || !s.needsRefundLocked() {
		s.mu.Unlock()
		return
	}
	s.refunded = true
	s.mu.Unlock()

	logger.LogInfo(c, fmt.Sprintf("用户 %d 请求失败, 返还预扣费（token_quota=%s, funding=%s）",
		s.relayInfo.UserId,
		logger.FormatQuota(s.tokenConsumed),
		s.funding.Source(),
	))

	// 复制需要的值到闭包中
	tokenId := s.relayInfo.TokenId
	tokenKey := s.relayInfo.TokenKey
	isPlayground := s.relayInfo.IsPlayground
	tokenConsumed := s.tokenConsumed
	extraReserved := s.extraReserved
	subscriptionId := s.relayInfo.SubscriptionId
	funding := s.funding

	gopool.Go(func() {
		// 1) 退还资金来源
		if err := funding.Refund(); err != nil {
			common.SysLog("error refunding billing source: " + err.Error())
		}
		if extraReserved > 0 && funding.Source() == BillingSourceSubscription && subscriptionId > 0 {
			if err := model.PostConsumeUserSubscriptionDelta(subscriptionId, -int64(extraReserved)); err != nil {
				common.SysLog("error refunding subscription extra reserved quota: " + err.Error())
			}
		}
		// 2) 退还令牌额度
		if tokenConsumed > 0 && !isPlayground {
			if err := model.IncreaseTokenQuota(tokenId, tokenKey, tokenConsumed); err != nil {
				common.SysLog("error refunding token quota: " + err.Error())
			}
		}
	})
}

// NeedsRefund 返回是否存在需要退还的预扣状态。
func (s *BillingSession) NeedsRefund() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.needsRefundLocked()
}

func (s *BillingSession) needsRefundLocked() bool {
	if s.settled || s.refunded || s.fundingSettled {
		// fundingSettled 时资金来源已提交结算，不能再退预扣费
		return false
	}
	if s.tokenConsumed > 0 {
		return true
	}
	// 订阅可能在 tokenConsumed=0 时仍预扣了额度
	if sub, ok := s.funding.(*SubscriptionFunding); ok && sub.preConsumed > 0 {
		return true
	}
	return false
}

// GetPreConsumedQuota 返回实际预扣的额度。
func (s *BillingSession) GetPreConsumedQuota() int {
	return s.preConsumedQuota
}

func (s *BillingSession) Reserve(targetQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.settled || s.refunded || s.trusted || targetQuota <= s.preConsumedQuota {
		return nil
	}

	delta := targetQuota - s.preConsumedQuota
	if delta <= 0 {
		return nil
	}

	if err := s.reserveFunding(delta); err != nil {
		return err
	}
	if err := s.reserveToken(delta); err != nil {
		s.rollbackFundingReserve(delta)
		return err
	}

	s.preConsumedQuota += delta
	s.tokenConsumed += delta
	s.extraReserved += delta
	s.syncRelayInfo()
	return nil
}

// ---------------------------------------------------------------------------
// PreConsume — 统一预扣费入口（含信任额度旁路）
// ---------------------------------------------------------------------------

// preConsume 执行预扣费：信任检查 -> 令牌预扣 -> 资金来源预扣。
// 任一步骤失败时原子回滚已完成的步骤。
func (s *BillingSession) preConsume(c *gin.Context, quota int) *types.NewAPIError {
	effectiveQuota := quota

	// ---- 信任额度旁路 ----
	if s.shouldTrust(c) {
		s.trusted = true
		effectiveQuota = 0
		logger.LogInfo(c, fmt.Sprintf("用户 %d 额度充足, 信任且不需要预扣费 (funding=%s)", s.relayInfo.UserId, s.funding.Source()))
	} else if effectiveQuota > 0 {
		logger.LogInfo(c, fmt.Sprintf("用户 %d 需要预扣费 %s (funding=%s)", s.relayInfo.UserId, logger.FormatQuota(effectiveQuota), s.funding.Source()))
	}

	// ---- 1) 预扣令牌额度 ----
	if effectiveQuota > 0 {
		if err := PreConsumeTokenQuota(s.relayInfo, effectiveQuota); err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		s.tokenConsumed = effectiveQuota
	}

	// ---- 2) 预扣资金来源 ----
	if err := s.funding.PreConsume(effectiveQuota); err != nil {
		// 预扣费失败，回滚令牌额度
		if s.tokenConsumed > 0 && !s.relayInfo.IsPlayground {
			if rollbackErr := model.IncreaseTokenQuota(s.relayInfo.TokenId, s.relayInfo.TokenKey, s.tokenConsumed); rollbackErr != nil {
				common.SysLog(fmt.Sprintf("error rolling back token quota (userId=%d, tokenId=%d, amount=%d, fundingErr=%s): %s",
					s.relayInfo.UserId, s.relayInfo.TokenId, s.tokenConsumed, err.Error(), rollbackErr.Error()))
			}
			s.tokenConsumed = 0
		}
		// 混扣预扣钱包不足（积分被并发抢占后钱包无法覆盖）→ 403 额度不足
		if errors.Is(err, ErrHybridWalletInsufficient) {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		// TODO: model 层应定义哨兵错误（如 ErrNoActiveSubscription），用 errors.Is 替代字符串匹配
		errMsg := err.Error()
		if strings.Contains(errMsg, "no active subscription") || strings.Contains(errMsg, "subscription quota insufficient") {
			return types.NewErrorWithStatusCode(fmt.Errorf("订阅额度不足或未配置订阅: %s", errMsg), types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}

	s.preConsumedQuota = effectiveQuota

	// ---- 同步 RelayInfo 兼容字段 ----
	s.syncRelayInfo()

	return nil
}

func (s *BillingSession) reserveFunding(delta int) error {
	switch funding := s.funding.(type) {
	case *WalletFunding:
		if err := model.DecreaseUserQuota(funding.userId, delta, false); err != nil {
			return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
		}
		funding.consumed += delta
		return nil
	case *HybridFunding:
		// 追加预扣：按积分优先扣，并记录本次拆分供 rollbackFundingReserve 精确回滚
		pPart, wPart, err := funding.reserveExtra(delta)
		if err != nil {
			if errors.Is(err, ErrHybridWalletInsufficient) {
				return types.NewErrorWithStatusCode(err, types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
			}
			return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
		}
		s.lastReservePoints, s.lastReserveWallet = pPart, wPart
		return nil
	case *EntitlementFunding:
		// 权益追加预扣：继续扣算力点。服务未交付，扣不到就必须拒绝——
		// 此时已经无法退回现有资金链路（预扣阶段早已过去），与订阅同语义返回 403。
		spent, ok, err := funding.reserveExtra(delta)
		if err != nil {
			return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
		}
		if !ok {
			return types.NewErrorWithStatusCode(
				fmt.Errorf("套餐算力点不足，无法继续本次请求"),
				types.ErrorCodeInsufficientUserQuota,
				http.StatusForbidden,
				types.ErrOptionWithSkipRetry(),
				types.ErrOptionWithNoRecordErrorLog(),
			)
		}
		s.lastReserveEntitlement = spent
		return nil
	case *SubscriptionFunding:
		if err := model.PostConsumeUserSubscriptionDelta(funding.subscriptionId, int64(delta)); err != nil {
			return types.NewErrorWithStatusCode(
				fmt.Errorf("订阅额度不足或未配置订阅: %s", err.Error()),
				types.ErrorCodeInsufficientUserQuota,
				http.StatusForbidden,
				types.ErrOptionWithSkipRetry(),
				types.ErrOptionWithNoRecordErrorLog(),
			)
		}
		return nil
	default:
		return types.NewError(fmt.Errorf("unsupported funding source: %s", s.funding.Source()), types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
}

func (s *BillingSession) rollbackFundingReserve(delta int) {
	switch funding := s.funding.(type) {
	case *WalletFunding:
		if err := model.IncreaseUserQuota(funding.userId, delta, false); err != nil {
			common.SysLog("error rolling back wallet funding reserve: " + err.Error())
		} else {
			funding.consumed -= delta
		}
	case *HybridFunding:
		// 撤销刚追加的 delta：按 reserveFunding 记录的拆分精确原路退还。
		// 不能复用 Settle(-delta)——其「先退钱包」全局策略在原始预扣走过钱包、
		// 本次追加走积分时会退错桶（codex review P2）
		funding.unreserveExtra(s.lastReservePoints, s.lastReserveWallet)
		s.lastReservePoints, s.lastReserveWallet = 0, 0
	case *EntitlementFunding:
		// 按快照精确逆转，理由与 Hybrid 那条同构：追加那笔可能落在与原始预扣
		// 不同的批次上，按总额退会退错批次。
		funding.unreserveExtra(s.lastReserveEntitlement)
		s.lastReserveEntitlement = nil
	case *SubscriptionFunding:
		if err := model.PostConsumeUserSubscriptionDelta(funding.subscriptionId, -int64(delta)); err != nil {
			common.SysLog("error rolling back subscription funding reserve: " + err.Error())
		}
	}
}

func (s *BillingSession) reserveToken(delta int) error {
	if delta <= 0 || s.relayInfo.IsPlayground {
		return nil
	}
	if err := PreConsumeTokenQuota(s.relayInfo, delta); err != nil {
		return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	return nil
}

// shouldTrust 统一信任额度检查，适用于钱包和订阅。
func (s *BillingSession) shouldTrust(c *gin.Context) bool {
	// 异步任务（ForcePreConsume=true）必须预扣全额，不允许信任旁路
	if s.relayInfo.ForcePreConsume {
		return false
	}

	trustQuota := common.GetTrustQuota()
	if trustQuota <= 0 {
		return false
	}

	// 检查令牌是否充足
	tokenTrusted := s.relayInfo.TokenUnlimited
	if !tokenTrusted {
		tokenQuota := c.GetInt("token_quota")
		tokenTrusted = tokenQuota > trustQuota
	}
	if !tokenTrusted {
		return false
	}

	switch s.funding.Source() {
	case BillingSourceWallet:
		return s.relayInfo.UserQuota > trustQuota
	case BillingSourceHybrid:
		// 积分+余额合并判断信任旁路；信任下 preConsume=0，结算时按积分优先补扣
		return s.relayInfo.UserQuota+s.relayInfo.UserPoints > trustQuota
	case BillingSourceSubscription:
		// 订阅不能启用信任旁路。原因：
		// 1. PreConsumeUserSubscription 要求 amount>0 来创建预扣记录并锁定订阅
		// 2. SubscriptionFunding.PreConsume 忽略参数，始终用 s.amount 预扣
		// 3. 若信任旁路将 effectiveQuota 设为 0，会导致 preConsumedQuota 与实际订阅预扣不一致
		return false
	default:
		return false
	}
}

// syncPointsConsumed 结算完成后把混扣的积分抵扣量同步到 RelayInfo（供日志），
// 并按最终值一次性累加 PointsUsed（对账用）。仅 HybridFunding 生效，其余为 no-op。
// 记账前先把积分抵扣量向上取整到整积分（不足 1 积分按 1 积分烧，加速营销积分消耗）；
// 本函数仅在 Settle 的 settled 闸门内执行一次，取整不会重复。
func (s *BillingSession) syncPointsConsumed() {
	hf, ok := s.funding.(*HybridFunding)
	if !ok {
		return
	}
	hf.roundUpToWholePoints()
	pc := hf.PointsConsumed()
	s.relayInfo.PointsConsumed = pc
	if pc > 0 {
		if err := model.AddUserPointsUsed(s.relayInfo.UserId, pc); err != nil {
			common.SysLog("failed to add user points used: " + err.Error())
		}
	}
}

// EntitlementSpend 暴露权益会话的资金拆分，供异步任务持久化。
//
// 走访问器而不是挂到 RelayInfo 上：拆分的类型是 model.ComputePointSpend，而
// model 已经导入了 relay/common（model/task.go），反向导入会成环。控制器同时
// 导入 service 与 model，从这里取是唯一不引入新依赖方向的路径。
//
// 非权益会话返回零值，调用方据此跳过。
func (s *BillingSession) EntitlementSpend() (counterId int, discount float64, spent []model.ComputePointSpend) {
	ent, ok := s.funding.(*EntitlementFunding)
	if !ok || ent.match == nil {
		return 0, 0, nil
	}
	d := ent.match.Entitlement.ConsumeDiscount
	if d <= 0 {
		d = 1
	}
	return ent.match.CounterId, d, ent.spent
}

// syncRelayInfo 将 BillingSession 的状态同步到 RelayInfo 的兼容字段上。
func (s *BillingSession) syncRelayInfo() {
	info := s.relayInfo
	info.FinalPreConsumedQuota = s.preConsumedQuota
	info.BillingSource = s.funding.Source()

	if sub, ok := s.funding.(*SubscriptionFunding); ok {
		info.SubscriptionId = sub.subscriptionId
		info.SubscriptionPreConsumed = sub.preConsumed + int64(s.extraReserved)
		info.SubscriptionPostDelta = 0
		info.SubscriptionAmountTotal = sub.AmountTotal
		info.SubscriptionAmountUsedAfterPreConsume = sub.AmountUsedAfter + int64(s.extraReserved)
		info.SubscriptionPlanId = sub.PlanId
		info.SubscriptionPlanTitle = sub.PlanTitle
	} else {
		info.SubscriptionId = 0
		info.SubscriptionPreConsumed = 0
	}

	if ent, ok := s.funding.(*EntitlementFunding); ok {
		if m := ent.Match(); m != nil {
			info.EntitlementId = m.Entitlement.Id
			info.EntitlementPlanId = m.PlanId
			info.SubscriptionId = m.UserSubscriptionId
			if info.EntitlementPlanTitle == "" {
				info.EntitlementPlanTitle = planTitleOf(m.PlanId)
			}
			// 次数快照：匹配时读到的已用次数 + 本次占用的 1 次。并发请求下可能与库里
			// 的实时值差几次——只用于日志展示，真正的闸门在条件更新里，不看这个数。
			info.EntitlementLimitCount = m.LimitCount
			info.EntitlementUsedCount = m.UsedCount + 1
		}
		info.EntitlementPointsSpent = ent.PointsSpent()
	}
}

// ---------------------------------------------------------------------------
// NewBillingSession 工厂 — 根据计费偏好创建会话并处理回退
// ---------------------------------------------------------------------------

// NewBillingSession 根据用户计费偏好创建 BillingSession，处理 subscription_first / wallet_first 的回退。
func NewBillingSession(c *gin.Context, relayInfo *relaycommon.RelayInfo, preConsumedQuota int) (*BillingSession, *types.NewAPIError) {
	if relayInfo == nil {
		return nil, types.NewError(fmt.Errorf("relayInfo is nil"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	pref := common.NormalizeBillingPreference(relayInfo.UserSetting.BillingPreference)

	// 钱包路径需要先检查用户额度
	tryWallet := func() (*BillingSession, *types.NewAPIError) {
		userQuota, err := model.GetUserQuota(relayInfo.UserId, false)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}

		// 积分混扣判定：总开关开 + 使用分组在白名单。
		// UsingGroup 此时已是 auto 解析后的真实分组（§2.3）。
		//
		// 刻意不判实名：RequireKyc 只是「发放侧」的门（签到、邀请人赠分），进了账的积分
		// 一律可花。手里有余额却因为没实名花不掉，对用户是没收；防薅羊毛的位置在发放侧
		// 与白名单分组，不在这里。
		// 渠道白名单是第二道：分组合并后（docs §10.2）default 组下自建与外采渠道
		// 并存，只判分组等于拿免费积分去买外采算力。空白名单时这层恒为 true，
		// 行为与加这层之前一致。
		//
		// 渠道号从 context 取而**不是** relayInfo.ChannelId：后者属于内嵌指针
		// ChannelMeta，而本函数所在的 PreConsumeBilling（controller/relay.go:193）
		// 跑在 InitChannelMeta 之前，那时 ChannelMeta 仍是 nil，直接读会 panic
		// （同一个坑见 service/upstream_moderation.go:81）。context 里的值由
		// middleware.Distribute 在进 handler 前就写好了，这里一定拿得到。
		//
		// 取不到时得 0，白名单非空则判定为不允许——降级方向是「不用积分、扣余额」，
		// 保守且不漏钱。
		//
		// ⚠️ 已知边界：这里判的是**预扣费时**选中的渠道。跨渠道重试若从自建切到
		// 外采，本次仍按预扣时的 funding 类型结算，会出现用积分付外采的钱。
		// 同分组内重试才可能触发、且单笔封顶在预扣额度，暂不处理；真要堵需要把
		// funding 类型的决策推迟到结算，那会改动 BillingSession 的核心时序。
		channelId := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
		useHybrid := operation_setting.GetPointsSetting().Enabled &&
			operation_setting.IsPointsEnabledForGroup(relayInfo.UsingGroup) &&
			operation_setting.IsPointsEnabledForChannel(channelId)
		userPoints := 0
		if useHybrid {
			if p, perr := model.GetUserPoints(relayInfo.UserId, false); perr == nil {
				userPoints = p
			}
		}

		// 授信额度并入可用性判断：现金花完后允许在授信内继续用（先用后付）。
		// 取自用户缓存而非另打一次 DB 查询——这里是每请求的热路径。
		var creditAvailable int
		if uc, cerr := model.GetUserCache(relayInfo.UserId); cerr == nil {
			if remain := uc.CreditLimit - uc.CreditUsed; remain > 0 {
				creditAvailable = int(remain)
			}
		}

		// 白名单分组把积分并入可用性判断：余额为 0 但积分充足也放行（营销积分的意义）。
		available := userQuota + userPoints + creditAvailable
		if available <= 0 {
			return nil, newInsufficientFundsError(userQuota, creditAvailable, relayInfo.UserId)
		}
		if available-preConsumedQuota < 0 {
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("预扣费额度失败, 用户剩余额度: %s, 需要预扣费额度: %s", logger.FormatQuota(available), logger.FormatQuota(preConsumedQuota)),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		// UserQuota 维持「钱包余额」语义不变（额度通知依赖它）；UserPoints 供日志/信任旁路。
		relayInfo.UserQuota = userQuota
		relayInfo.UserPoints = userPoints

		var funding FundingSource
		if useHybrid && userPoints > 0 {
			funding = &HybridFunding{userId: relayInfo.UserId}
		} else {
			funding = &WalletFunding{userId: relayInfo.UserId}
		}

		session := &BillingSession{
			relayInfo: relayInfo,
			funding:   funding,
		}
		if apiErr := session.preConsume(c, preConsumedQuota); apiErr != nil {
			return nil, apiErr
		}
		return session, nil
	}

	// tryEntitlement 尝试走套餐权益。返回 nil 表示「没走成」——未命中、次数用尽、
	// 算力点不足、甚至查询报错，一律降级到后面的资金链路而不是让请求失败。
	//
	// 降级是这条路径的核心语义：权益是优惠不是准入门槛，任何一个维度不满足都只意味着
	// 「这次没享受到套餐价」，服务必须照常交付（§6 ④）。所以这里刻意吞掉错误只记日志，
	// 唯独不能让权益侧的问题变成用户侧的 4xx/5xx。
	tryEntitlement := func() *BillingSession {
		// 重试时会重新走一遍，上一轮的降级记录不能留到这一轮的日志里
		relayInfo.EntitlementFallback = nil
		// 渠道号从 context 取而不是 relayInfo.ChannelId：本函数跑在 InitChannelMeta
		// 之前，那时 ChannelMeta 仍是 nil，直接读会 panic（同一个坑见 tryWallet 的说明）。
		channelId := common.GetContextKeyInt(c, constant.ContextKeyChannelId)
		match, err := model.MatchUserEntitlement(relayInfo.UserId, relayInfo.OriginModelName, channelId)
		if err != nil {
			common.SysLog("entitlement match failed, falling back: " + err.Error())
			return nil
		}
		if match == nil {
			return nil
		}
		funding := &EntitlementFunding{
			userId: relayInfo.UserId,
			match:  match,
		}
		session := &BillingSession{
			relayInfo: relayInfo,
			funding:   funding,
		}
		if apiErr := session.preConsume(c, preConsumedQuota); apiErr != nil {
			if reason, need := funding.FailReason(); reason != "" {
				relayInfo.EntitlementFallback = buildEntitlementFallback(
					relayInfo.UserId, match, reason, need)
			}
			return nil
		}
		return session
	}

	trySubscription := func() (*BillingSession, *types.NewAPIError) {
		subConsume := int64(preConsumedQuota)
		if subConsume <= 0 {
			subConsume = 1
		}
		session := &BillingSession{
			relayInfo: relayInfo,
			funding: &SubscriptionFunding{
				requestId: relayInfo.RequestId,
				userId:    relayInfo.UserId,
				modelName: relayInfo.OriginModelName,
				amount:    subConsume,
			},
		}
		// 必须传 subConsume 而非 preConsumedQuota，保证 SubscriptionFunding.amount、
		// preConsume 参数和 FinalPreConsumedQuota 三者一致，避免订阅多扣费。
		if apiErr := session.preConsume(c, int(subConsume)); apiErr != nil {
			return nil, apiErr
		}
		return session, nil
	}

	// 权益属于套餐权益，用户显式选了 wallet_only 就不该再动它——那是「这次别用我的
	// 套餐」的意思。其余偏好下权益都排在订阅额度之前：它更具体（限定了模型与渠道），
	// 且不消耗订阅的通用额度。
	switch pref {
	case "subscription_only":
		if session := tryEntitlement(); session != nil {
			return session, nil
		}
		return trySubscription()
	case "wallet_only":
		return tryWallet()
	case "wallet_first":
		session, err := tryWallet()
		if err != nil {
			if err.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
				if entSession := tryEntitlement(); entSession != nil {
					return entSession, nil
				}
				return trySubscription()
			}
			return nil, err
		}
		return session, nil
	case "subscription_first":
		fallthrough
	default:
		if session := tryEntitlement(); session != nil {
			return session, nil
		}
		hasSub, subCheckErr := model.HasActiveUserSubscription(relayInfo.UserId)
		if subCheckErr != nil {
			return nil, types.NewError(subCheckErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if !hasSub {
			return tryWallet()
		}
		session, apiErr := trySubscription()
		if apiErr != nil {
			if apiErr.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
				return tryWallet()
			}
			return nil, apiErr
		}
		return session, nil
	}
}

// syncCreditConsumed 结算收尾：把本次消费造成的透支结转为信用欠款。
//
// 扣费侧照常扣 User.Quota、允许短暂透支为负；这里把负的那部分挪进 CreditUsed 并让
// quota 归零，欠款从此有独立科目（应收账款），不与预付余额混在一个数字里。
// 仅在 Settle 的 settled 闸门内执行一次，与 syncPointsConsumed 同构。
//
// 失败只记日志：服务已交付、钱已扣，不该因记账失败而报错给用户。漏结转的后果是
// quota 停在负数，由每日自洽校验发现（现金账会差出恰好那一笔）。
func (s *BillingSession) syncCreditConsumed(actualQuota int) {
	// 不动 User.Quota 的资金来源一律不结转：订阅扣的是 UserSubscription.AmountUsed，
	// 权益扣的是次数与算力点批次，两者都没让 quota 少一分。
	//
	// 若此时用户 quota 恰好因别的原因为负（比如上一笔钱包请求的透支还没结转），
	// 这里会把那笔与本请求无关的透支结转掉、并记在本请求的 CreditConsumed 上——
	// 而 getFundConsumeStats 把这两种来源的日志都排除在外，于是 credit_used 涨了、
	// 对账侧看不到，信用账报假不平。
	//
	// 用白名单（只有真正扣 quota 的来源才结转）而不是黑名单列出例外：再加资金来源时
	// 漏改这里的默认结果是「不结转」，比「误结转别人的透支」安全得多。
	switch s.funding.Source() {
	case BillingSourceWallet, BillingSourceHybrid:
	default:
		return
	}
	settled, err := model.SettleOverdraftToCredit(s.relayInfo.UserId, int64(actualQuota))
	if err != nil {
		common.SysLog(fmt.Sprintf("failed to settle overdraft to credit (userId=%d): %s",
			s.relayInfo.UserId, err.Error()))
		return
	}
	if settled > 0 {
		s.relayInfo.CreditConsumed = int(settled)
	}
}

// newInsufficientFundsError 区分「余额不足」与「授信用尽」。
//
// 两者用户要做的事完全相反：一个去充值，一个去结清账款。给同一句提示会让企业客户
// 反复充值却仍被拒——他们的账户本来就该是 0 余额 + 授信。
func newInsufficientFundsError(userQuota, creditAvailable, userId int) *types.NewAPIError {
	limit, used, _, err := model.GetUserCreditState(userId)
	if err == nil && limit > 0 && used >= limit && creditAvailable <= 0 {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("授信额度已用尽，请结清账款后继续使用（已用 %s / 授信 %s）",
				logger.FormatQuota(int(used)), logger.FormatQuota(int(limit))),
			types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
			types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	}
	return types.NewErrorWithStatusCode(
		fmt.Errorf("用户额度不足, 剩余额度: %s", logger.FormatQuota(userQuota)),
		types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
		types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
}

// planTitleOf 取套餐名供日志展示。走套餐缓存；取不到时返回空串——日志缺一个名字
// 不该影响扣费。
func planTitleOf(planId int) string {
	plan, err := model.GetSubscriptionPlanById(planId)
	if err != nil || plan == nil {
		return ""
	}
	return plan.Title
}

// buildEntitlementFallback 组装降级记录。点数按当时的换算率换成展示点数落库：
// 所需点数向上取整（与扣费侧 ceil 一致，「需要 N 点」必须是真正要扣的量），
// 剩余点数向下取整（与余额展示同口径，不虚报）。
func buildEntitlementFallback(userId int, match *model.EntitlementMatch, reason string, needQuota int64) *relaycommon.EntitlementFallback {
	fb := &relaycommon.EntitlementFallback{
		Reason:     reason,
		PlanId:     match.PlanId,
		PlanTitle:  planTitleOf(match.PlanId),
		LimitCount: match.LimitCount,
	}
	if reason == EntitlementFallbackPointsInsufficient {
		fb.PointsNeeded = common.QuotaToComputePointsCeil(int(needQuota))
		if bal, err := model.GetComputePointBalance(userId); err == nil && bal != nil {
			fb.PointsAvailable = common.QuotaToComputePoints(int(bal.Available))
		}
	}
	return fb
}
