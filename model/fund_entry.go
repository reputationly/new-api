package model

import (
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// FundEntry 资金入账流水——收入对账的唯一事实来源。
//
// 所有让余额增加的动作、以及信用回款，统一在这里落一条。此前现金流水散在三处
// （topups 表、bank_transfer_orders 表、logs 表的纯文本），口径、金额单位、状态机
// 各不相同，无法一次查全「真实入账多少」。
//
// 记账铁律：**CashFen > 0 才计入营收**，且只出现在 KindPrepay（预付充值）与
// KindARSettle（信用回款）两种 Kind 上。赠送（KindGift）与授信开额（KindCreditGrant）
// 恒为 0——前者是市场成本，后者只是给了额度、钱还没收到。
type FundEntry struct {
	Id        int   `json:"id"`
	UserId    int   `json:"user_id" gorm:"index"`
	CreatedAt int64 `json:"created_at" gorm:"bigint;index"`

	Account string `json:"account" gorm:"type:varchar(16);index"` // cash | points | credit
	Kind    string `json:"kind" gorm:"type:varchar(24);index"`

	// QuotaDelta 入账的 quota unit，可为负（冲正/扣减）。积分账户同样以 quota unit 记账，
	// 与 User.PointsBalance 同单位（见 docs/points-design.md §三）。
	QuotaDelta int64 `json:"quota_delta" gorm:"bigint"`
	// CashFen 实收人民币（分）。用「分」存整数避免浮点累加误差——报表要对到分。
	CashFen int64 `json:"cash_fen" gorm:"bigint"`

	Source string `json:"source" gorm:"type:varchar(32);index"`

	// (RefType, RefId) 复合唯一索引 = 幂等键。支付补单、审批重试、赠品补发都靠它防重复记账。
	// 两者都必须非空：空串在三库中都不等价于 NULL，多条空 ref 会互相撞唯一索引。
	RefType string `json:"ref_type" gorm:"type:varchar(24);not null;uniqueIndex:idx_fund_entry_ref,priority:1"`
	RefId   string `json:"ref_id" gorm:"type:varchar(64);not null;uniqueIndex:idx_fund_entry_ref,priority:2"`

	OperatorId int    `json:"operator_id" gorm:"index"` // 管理员 id；0 = 系统或用户自助
	Remark     string `json:"remark" gorm:"type:varchar(255)"`
}

// Account —— 资金归属的账户。三者语义彻底分离，不可互转。
const (
	FundAccountCash   = "cash"   // User.Quota，预收账款（负债），真金白银
	FundAccountPoints = "points" // User.PointsBalance，营销赠送，市场成本
	FundAccountCredit = "credit" // 信用额度，应收账款
	// FundAccountSubscription 套餐/订阅账户。客户付的钱没进 User.Quota，而是变成
	// 订阅额度（UserSubscription.AmountTotal）——它是**未履约的服务义务**，与预收账款
	// 同性质但不同池子。计入营收（CashFen > 0），但必须排除在现金自洽校验之外，
	// 否则会出现「收了钱而 users.quota 没涨」的假不平。
	FundAccountSubscription = "subscription"
)

// Kind —— 这笔钱的性质。决定它计不计营收。
const (
	FundKindPrepay      = "prepay"       // 预付充值（在线支付/对公/线下手工），计营收
	FundKindGift        = "gift"         // 赠送发放，市场成本，CashFen 恒 0
	FundKindCreditGrant = "credit_grant" // 授信开额，只是额度，CashFen 恒 0
	FundKindARSettle    = "ar_settle"    // 信用回款核销，计营收
	FundKindRefund      = "refund"       // 退款，QuotaDelta 为负
	FundKindAdjust      = "adjust"       // 差错调整/管理员扣减，报表单列做异常监控
)

// Source —— 入账渠道，用于报表下钻。
const (
	FundSourceOnlinePay    = "online_pay"    // 在线支付（支付宝/微信/Stripe/Creem/易支付）
	FundSourceBankTransfer = "bank_transfer" // 对公转账审批入账
	FundSourceSubscription = "subscription"  // 套餐/订阅售卖
	FundSourceAdminCash    = "admin_cash"    // 管理员线下手工入账
	FundSourceAdminGift    = "admin_gift"    // 管理员赠送
	FundSourceAdminCredit  = "admin_credit"  // 管理员调整授信
	FundSourceAdminAdjust  = "admin_adjust"  // 管理员差错调整
	FundSourceRedemption   = "redemption"    // 兑换码
	FundSourceCheckin      = "checkin"       // 签到
	FundSourceKyc          = "kyc"           // 实名奖励
	FundSourceInvite       = "invite"        // 邀请奖励
	FundSourceRegister     = "register"      // 注册礼
	FundSourcePackageBonus = "package_bonus" // 充值套餐赠品
)

// RefType —— 幂等键的业务对象类型。
const (
	FundRefTopUp             = "topup"              // topups.trade_no
	FundRefSubscriptionOrder = "subscription_order" // subscription_orders.trade_no
	FundRefBankTransfer      = "bank_transfer"      // bank_transfer_orders.trade_no
	FundRefRedemption        = "redemption"         // redemptions.id
	FundRefCheckin           = "checkin"            // userId:date
	FundRefUser              = "user"               // userId（注册礼、实名奖励等一次性发放）
	FundRefAdminOp           = "admin_op"           // 管理员操作，单号在写入时生成
)

var ErrFundEntryRefEmpty = errors.New("fund entry ref_type/ref_id 不能为空")

// NewAdminOpRefId 生成管理员操作的幂等单号。管理员每次操作都是独立事件，没有天然
// 业务单号可挂，但幂等键又必须非空且唯一（否则多条空 ref 互撞索引），故在此生成。
// 格式与 bank_transfer 的 TradeNo 一致，便于运营在流水里肉眼区分来源。
func NewAdminOpRefId() string {
	return fmt.Sprintf("AD%s%d", common.GetRandomString(6), time.Now().Unix())
}

// CheckinRefId 构造签到的幂等键。同一用户同一天只能签到一次，天然唯一。
func CheckinRefId(userId int, date string) string {
	return fmt.Sprintf("%d:%s", userId, date)
}

// YuanToFen 把「元」换算成「分」。订单金额在库里是 float64（元），而流水以分存整数——
// 报表要对到分，浮点累加会飘。用 decimal 而非 int64(yuan*100)：后者在 19.99 这类
// 值上会因二进制表示误差截成 1998。
func YuanToFen(yuan float64) int64 {
	return decimal.NewFromFloat(yuan).Mul(decimal.NewFromInt(100)).Round(0).IntPart()
}

// recordCashInflowTx 在到账事务内落一条现金入账流水（预付充值/套餐售卖共用）。
//
// 必须与余额变更同事务：余额改了流水没落会让自洽校验长期不平，而那个告警本来是用来
// 发现「有代码绕过流水表」的。幂等键取业务单号，支付回调重投与手工补单都只记一次营收。
//
// ⚠️ moneyYuan 必须是**人民币元**。现网启用的渠道（支付宝/微信直连、易支付、对公转账）
// 的 TopUp.Money 都是人民币，故直接换算。
// 但 Stripe 的 Money 是**美元**（GetChargedAmount 不乘汇率，见 ManualCompleteTopUp 内
// 「Money 代表…美元数量」的注释），Creem / Waffo 的币种则取决于运营在对应平台配置的
// 产品价，代码无法静态判定。这三家现网确定不会启用；**若日后启用，必须先在调用处把
// 金额折成人民币**，否则营收会按 1:1 记账（Stripe 约少记 7.3 倍）。
func recordCashInflowTx(tx *gorm.DB, userId int, quotaDelta int64, moneyYuan float64,
	source, refType, refId, remark string) error {
	_, err := insertFundEntryTx(tx, &FundEntry{
		UserId:     userId,
		Account:    FundAccountCash,
		Kind:       FundKindPrepay,
		QuotaDelta: quotaDelta,
		CashFen:    YuanToFen(moneyYuan),
		Source:     source,
		RefType:    refType,
		RefId:      refId,
		Remark:     remark,
	})
	return err
}

// insertFundEntryTx 在给定事务内写入一条流水，(RefType, RefId) 冲突时静默跳过。
//
// 必须在与余额变更同一个事务内调用，否则余额改了流水没落（或反之）会让自洽校验
// 长期不平，而那个告警本来是用来发现「有代码绕过流水表」的——噪声会淹掉真信号。
//
// 返回 inserted 表示是否真的写入。false 说明这笔已记过账（支付补单、审批重试等
// 重复触发），调用方通常无需处理，但余额变更那一侧也必须同样具备幂等，
// 否则会出现「钱加了两次、流水只有一条」。
func insertFundEntryTx(tx *gorm.DB, entry *FundEntry) (inserted bool, err error) {
	if entry == nil {
		return false, errors.New("fund entry is nil")
	}
	if entry.RefType == "" || entry.RefId == "" {
		return false, ErrFundEntryRefEmpty
	}
	if entry.CreatedAt == 0 {
		entry.CreatedAt = common.GetTimestamp()
	}
	// OnConflict DoNothing 由 GORM 翻译成各方言的等价写法（PG/SQLite 的
	// ON CONFLICT DO NOTHING、MySQL 的 INSERT IGNORE），三库通吃。
	res := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "ref_type"}, {Name: "ref_id"}},
		DoNothing: true,
	}).Create(entry)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

// InsertFundEntry 非事务入口，用于余额变更不在事务内的调用点（如管理员操作走的是
// IncreaseUserQuota 这类独立函数）。语义同 insertFundEntryTx。
func InsertFundEntry(entry *FundEntry) (inserted bool, err error) {
	return insertFundEntryTx(DB, entry)
}

// RecordFundEntry 是 InsertFundEntry 的 best-effort 包装：失败只记日志不阻断主流程。
//
// 用在「余额已经变更完成、再补记流水」的位置——此时主流程（用户的钱已经到账）
// 不该因为记账失败而回滚或报错给用户。代价是流水可能漏记，由每日自洽校验兜底发现。
//
// 凡是能放进同一事务的调用点，一律用 insertFundEntryTx，不要用这个。
func RecordFundEntry(entry *FundEntry) {
	if _, err := InsertFundEntry(entry); err != nil {
		common.SysLog(fmt.Sprintf("failed to record fund entry (ref=%s/%s): %s",
			entry.RefType, entry.RefId, err.Error()))
	}
}

// FundEntryExists 判断某业务对象是否已记过账，供发放侧做幂等闸门。
//
// 流水表的唯一索引只保证「不会重复记账」，保证不了「不会重复发钱」。若发放侧不设防，
// 重复调用就会出现「钱加了两次、流水只有一条」——流水侧的幂等反而给了假安全感。
func FundEntryExists(refType, refId string) bool {
	if refType == "" || refId == "" {
		return false
	}
	var n int64
	if err := DB.Model(&FundEntry{}).
		Where("ref_type = ? AND ref_id = ?", refType, refId).Count(&n).Error; err != nil {
		// 查询失败时保守放行：漏发可人工补，误挡则用户永远拿不到该得的赠品
		common.SysLog("failed to check fund entry existence: " + err.Error())
		return false
	}
	return n > 0
}
