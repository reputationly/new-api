package controller

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// 管理员资金操作 —— 把原先含糊的「改额度」拆成四个语义明确的动作，每个动作都必须
// 说明这笔钱的性质，并落一条 fund_entries 流水。
//
// 拆分的理由：此前 add_quota 只写一条文本日志（「管理员增加用户额度 $12.3」），
// 收没收钱、收了多少人民币、凭证是什么全无结构化记录，导致「真实入账」根本算不出来。
//
// 四个动作与营收的关系（记账铁律：CashFen > 0 才计营收）：
//
//	prepay       真实转账入账  Quota +=           计营收
//	gift         赠送          PointsBalance +=   不计（市场成本）
//	credit_grant 调整授信额度  CreditLimit  =     不计（只是额度，钱还没收到）
//	ar_settle    信用回款核销  CreditUsed  -=     计营收（应收变现）
//
// 刻意不提供 override（覆盖余额）：把余额从 A 改成 B，差额是收了钱还是送的，
// 事后无法判定，是账本上唯一的黑洞。
const (
	FundOpPrepay      = "prepay"
	FundOpGift        = "gift"
	FundOpCreditGrant = "credit_grant"
	FundOpARSettle    = "ar_settle"
)

// handleFundOperation 处理 action=fund_op。调用方已完成用户存在性校验与权限校验。
func handleFundOperation(c *gin.Context, user *model.User, req ManageRequest) {
	adminId := c.GetInt("id")
	adminInfo := map[string]interface{}{
		"admin_id":       adminId,
		"admin_username": c.GetString("username"),
	}
	ref := strings.TrimSpace(req.Ref)
	remark := strings.TrimSpace(req.Remark)

	switch req.Op {
	case FundOpPrepay:
		// 实收金额与到账额度是两个独立字段：线下谈单常见「收 ¥5000 给 ¥6000 额度」，
		// 实收记 5000 进营收、到账给 6000，差额自然表现为折扣，不把营收记虚。
		if req.Value <= 0 {
			common.ApiErrorMsg(c, "到账额度必须大于 0")
			return
		}
		if req.CashFen <= 0 {
			common.ApiErrorMsg(c, "实收金额必须大于 0；若为赠送请改用「赠送」操作")
			return
		}
		if ref == "" {
			common.ApiErrorMsg(c, "请填写凭证号或合同号")
			return
		}
		if err := model.IncreaseUserQuota(user.Id, req.Value, true); err != nil {
			common.ApiError(c, err)
			return
		}
		refId := model.NewAdminOpRefId()
		model.RecordFundEntry(&model.FundEntry{
			UserId:     user.Id,
			Account:    model.FundAccountCash,
			Kind:       model.FundKindPrepay,
			QuotaDelta: int64(req.Value),
			CashFen:    req.CashFen,
			Source:     model.FundSourceAdminCash,
			RefType:    model.FundRefAdminOp,
			RefId:      refId,
			OperatorId: adminId,
			Remark:     buildFundRemark(ref, remark),
		})
		model.RecordLogWithAdminInfo(user.Id, model.LogTypeManage,
			fmt.Sprintf("管理员入账 %s（实收 %s，凭证 %s，流水 %s）",
				logger.LogQuotaShort(req.Value), formatFen(req.CashFen), ref, refId), adminInfo)

	case FundOpGift:
		// 赠送进积分池而非现金池：赠送一旦发成额度就混进 User.Quota，「真实入账」会虚高。
		if req.Value <= 0 {
			common.ApiErrorMsg(c, "赠送额度必须大于 0")
			return
		}
		if remark == "" {
			common.ApiErrorMsg(c, "请填写赠送事由")
			return
		}
		if err := model.IncreaseUserPoints(user.Id, req.Value, true); err != nil {
			common.ApiError(c, err)
			return
		}
		refId := model.NewAdminOpRefId()
		model.RecordFundEntry(&model.FundEntry{
			UserId:     user.Id,
			Account:    model.FundAccountPoints,
			Kind:       model.FundKindGift,
			QuotaDelta: int64(req.Value),
			CashFen:    0, // 赠送恒为 0：这是市场成本，不是营收
			Source:     model.FundSourceAdminGift,
			RefType:    model.FundRefAdminOp,
			RefId:      refId,
			OperatorId: adminId,
			Remark:     buildFundRemark(ref, remark),
		})
		model.RecordLogWithAdminInfo(user.Id, model.LogTypeManage,
			fmt.Sprintf("管理员赠送 %d 积分（事由：%s，流水 %s）",
				common.QuotaToPoints(req.Value), remark, refId), adminInfo)

	case FundOpCreditGrant:
		// 授信上限是绝对值设定而非增量：运营填的是「这个客户的额度是多少」。
		// 不得低于已用未结，否则客户立刻处于超限停服状态，且应收口径会错乱。
		if req.Value < 0 {
			common.ApiErrorMsg(c, "授信上限不能为负")
			return
		}
		if ref == "" {
			common.ApiErrorMsg(c, "请填写授信协议号")
			return
		}
		newLimit := int64(req.Value)
		if newLimit < user.CreditUsed {
			common.ApiErrorMsg(c, fmt.Sprintf("授信上限不得低于已用未结 %s",
				logger.LogQuotaShort(int(user.CreditUsed))))
			return
		}
		oldLimit := user.CreditLimit
		if err := model.SetUserCreditLimit(user.Id, newLimit); err != nil {
			common.ApiError(c, err)
			return
		}
		refId := model.NewAdminOpRefId()
		model.RecordFundEntry(&model.FundEntry{
			UserId:  user.Id,
			Account: model.FundAccountCredit,
			Kind:    model.FundKindCreditGrant,
			// 记录额度变化量而非绝对值：流水是「发生了什么」的账，累加得到当前额度。
			QuotaDelta: newLimit - oldLimit,
			CashFen:    0, // 开额度不是收入，回款时才计（见 ar_settle）
			Source:     model.FundSourceAdminCredit,
			RefType:    model.FundRefAdminOp,
			RefId:      refId,
			OperatorId: adminId,
			Remark:     buildFundRemark(ref, remark),
		})
		model.RecordLogWithAdminInfo(user.Id, model.LogTypeManage,
			fmt.Sprintf("管理员调整授信额度 %s → %s（协议 %s，流水 %s）",
				logger.LogQuotaShort(int(oldLimit)), logger.LogQuotaShort(int(newLimit)), ref, refId), adminInfo)

	case FundOpARSettle:
		// 回款核销：应收变现，这一刻才确认收入。开授信时不计，客户消费时也不计。
		if req.Value <= 0 {
			common.ApiErrorMsg(c, "核销额度必须大于 0")
			return
		}
		if req.CashFen <= 0 {
			common.ApiErrorMsg(c, "回款金额必须大于 0")
			return
		}
		if ref == "" {
			common.ApiErrorMsg(c, "请填写回款凭证")
			return
		}
		if user.CreditUsed <= 0 {
			common.ApiErrorMsg(c, "该用户无未结账款")
			return
		}
		if int64(req.Value) > user.CreditUsed {
			common.ApiErrorMsg(c, fmt.Sprintf("核销额度不得超过已用未结 %s",
				logger.LogQuotaShort(int(user.CreditUsed))))
			return
		}
		if err := model.SettleUserCredit(user.Id, int64(req.Value)); err != nil {
			common.ApiError(c, err)
			return
		}
		refId := model.NewAdminOpRefId()
		model.RecordFundEntry(&model.FundEntry{
			UserId:  user.Id,
			Account: model.FundAccountCredit,
			Kind:    model.FundKindARSettle,
			// 负数：应收余额减少。自洽校验按 Σ信用消耗 - Σ回款 == CreditUsed 对账。
			QuotaDelta: -int64(req.Value),
			CashFen:    req.CashFen,
			Source:     model.FundSourceAdminCash,
			RefType:    model.FundRefAdminOp,
			RefId:      refId,
			OperatorId: adminId,
			Remark:     buildFundRemark(ref, remark),
		})
		model.RecordLogWithAdminInfo(user.Id, model.LogTypeManage,
			fmt.Sprintf("管理员核销回款 %s（实收 %s，凭证 %s，流水 %s）",
				logger.LogQuotaShort(req.Value), formatFen(req.CashFen), ref, refId), adminInfo)

	default:
		common.ApiErrorMsg(c, "未知的资金操作类型")
		return
	}

	// 回读最新余额，前端据此刷新状态条而不关闭弹窗——运营常需连做两步
	// （先核销回款、再调高授信），不该强迫重新打开。
	updated, err := model.GetUserById(user.Id, false)
	if err != nil {
		c.JSON(200, gin.H{"success": true, "message": ""})
		return
	}
	c.JSON(200, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"quota":          updated.Quota,
			"points_balance": updated.PointsBalance,
			"credit_limit":   updated.CreditLimit,
			"credit_used":    updated.CreditUsed,
		},
	})
}

// recordAdjustEntry 为旧的 add_quota / add_points 补记流水。
//
// 这两个入口不说明资金性质——加的这笔是收了钱还是白送，事后无从判定——所以一律
// 记为 kind=adjust、CashFen=0，在报表里单列一栏做异常项监控，不计入营收。
// 不补记的话余额变了而流水没有，每日自洽校验会长期不平，噪声会淹掉「有代码绕过
// 流水表」这个真信号。
//
// 新的资金操作请走 fund_op 的四个动作，它们能说清楚性质。
func recordAdjustEntry(userId, adminId int, account string, delta int64, note string) string {
	refId := model.NewAdminOpRefId()
	model.RecordFundEntry(&model.FundEntry{
		UserId:     userId,
		Account:    account,
		Kind:       model.FundKindAdjust,
		QuotaDelta: delta,
		CashFen:    0, // 性质不明，一律不计营收
		Source:     model.FundSourceAdminAdjust,
		RefType:    model.FundRefAdminOp,
		RefId:      refId,
		OperatorId: adminId,
		Remark:     truncateRunes(note, 255),
	})
	return refId
}

// buildFundRemark 把凭证号与备注合并进流水的 Remark（varchar(255)）。
// 凭证号在前：对账时肉眼扫的是它。
func buildFundRemark(ref, remark string) string {
	var s string
	switch {
	case ref != "" && remark != "":
		s = ref + " | " + remark
	case ref != "":
		s = ref
	default:
		s = remark
	}
	return truncateRunes(s, 255)
}

// truncateRunes 按 rune 截断，保证不把中文切成半个字符（Remark 列是 varchar(255)）。
func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}

// formatFen 把「分」格式化成人民币展示串，用于管理日志。
func formatFen(fen int64) string {
	return fmt.Sprintf("¥%.2f", float64(fen)/100)
}
