package quotaunitmigration

import "math"

// 单位迁移的「改什么」清单：数值列、JSON 键、options。
//
// 单位换算只有一条规则：旧 quota × 14.6 = 新 quota（因为 QuotaPerUnit 从 500000 变成
// 7300000，而 1 quota 从 ¥0.0000146 变成 ¥0.000001）。名字里带 quota 的字段不一定是
// quota（见 PreConsumedQuota 的教训），所以下面每一列都按「它在计费代码里的单位」挑选。

// numericScope 一批需要 ×14.6 的整数列。
type numericScope struct {
	table   string
	columns []string
	// where 为空表示全表。top_ups.amount 的语义随 provider 而异，必须限定。
	where string
}

// smallNumericScopes 参与小表阶段（单事务）的表。logs 单独分批，见 logsNumericColumns。
var smallNumericScopes = []numericScope{
	{"users", []string{
		"quota", "used_quota", "aff_quota", "aff_history", "points_balance", "points_used",
		"aff_points_earned", "credit_limit", "credit_used", "credit_settled",
	}, ""},
	{"tokens", []string{"remain_quota", "used_quota"}, ""},
	{"channels", []string{"used_quota"}, ""},
	{"tasks", []string{"quota"}, ""},
	{"midjourneys", []string{"quota"}, ""},
	{"quota_data", []string{"quota"}, ""},
	{"redemptions", []string{"quota"}, ""},
	{"checkins", []string{"quota_awarded"}, ""},
	{"fund_entries", []string{"quota_delta"}, ""},
	{"bank_transfer_orders", []string{"quota_granted"}, ""},
	{"subscription_plans", []string{"total_amount", "compute_points_per_period"}, ""},
	{"user_subscriptions", []string{"amount_total", "amount_used"}, ""},
	{"subscription_pre_consume_records", []string{"pre_consumed"}, ""},
	{"compute_point_lots", []string{"points_total", "points_used"}, ""},
	// amount 的单位随 payment_provider 而异：直连渠道（支付宝 / 微信）与 Creem、
	// 对公转账写的是 quota，epay / stripe / waffo / waffo_pancake 写的是金额。
	// 见 model/topup.go ManualCompleteTopUp 的同一判断。
	{"top_ups", []string{"amount"},
		"payment_provider IN ('alipay_direct','wxpay_direct','creem','bank_transfer_manual')"},
}

// logsNumericColumns 使用日志的数值列，走分批阶段。
var logsNumericColumns = []string{"quota", "points_consumed", "credit_consumed"}

// jsonScope 一处需要按键改写的 JSON 文本列。
type jsonScope struct {
	table   string
	jsonCol string
	// keys 是该列需要改写的键清单（日志/报告用；改写本身按 key 精确匹配，不做 SQL 预筛）。
	keys    []string
	rewrite jsonRewriter
}

// logOtherScaledKeys logs.other 里以 quota 为单位的键。
var logOtherScaledKeys = []string{
	"fee_quota", "cost_quota", "compute_points_quota", "pre_consumed_quota", "actual_quota",
	"wallet_quota_deducted", "subscription_pre_consumed", "subscription_post_delta",
	"subscription_consumed", "subscription_remain", "subscription_total", "subscription_used",
}

// logs.other 的改写范围。
var logsJSONScope = jsonScope{
	table:   "logs",
	jsonCol: "other",
	keys:    logOtherScaledKeys,
	rewrite: rewriteLogOther,
}

var smallJSONScopes = []jsonScope{
	{
		table:   "tasks",
		jsonCol: "private_data",
		keys:    []string{"points_consumed", "credit_consumed", "stage1_adaptor_quota", "entitlement_spent[].amount"},
		rewrite: rewriteTaskPrivateData,
	},
	{
		table:   "users",
		jsonCol: "setting",
		keys:    []string{"quota_warning_threshold"},
		rewrite: rewriteUserSetting,
	},
}

// optionMode options 表里一个 key 的改写方式。
type optionMode int

const (
	// optionScaleInt 纯整数串 × 14.6，四舍五入后写成整数串。
	optionScaleInt optionMode = iota
	// optionScaleFloat 按浮点解析 × 14.6，四舍五入后写成整数串（历史值可能是 0.1 这种）。
	optionScaleFloat
	// optionFixed 直接写成固定值。
	optionFixed
)

type optionSpec struct {
	key  string
	mode optionMode
	// fixedValue mode = optionFixed 时写入的值。
	fixedValue string
	// missingValue 库里没有该 key 时写入的值；空 = 不写。
	missingValue string
}

// optionSpecs options 里需要改写的 key。
//
// **PreConsumedQuota 刻意不在表内**：名字像 quota，实际是 ModelPriceHelper 里
// preConsumedTokens 的 token 数下限，倍率路径乘 scale 已经在它之后发生，再把它 ×14.6
// 会把冻结额放大两次。详见 §10.3 第 10 条。
var optionSpecs = []optionSpec{
	{key: "QuotaForNewUser", mode: optionScaleInt},
	{key: "QuotaForInviter", mode: optionScaleInt},
	{key: "QuotaForInvitee", mode: optionScaleInt},
	{key: "QuotaRemindThreshold", mode: optionScaleInt, missingValue: "14600"},
	{key: "checkin_setting.min_quota", mode: optionScaleFloat},
	{key: "checkin_setting.max_quota", mode: optionScaleFloat},
	{key: "points_setting.quota_per_point", mode: optionFixed, fixedValue: "10000", missingValue: "10000"},
	{key: "compute_point_setting.quota_per_compute_point", mode: optionFixed, fixedValue: "10000", missingValue: "10000"},
	{key: "QuotaPerUnit", mode: optionFixed, fixedValue: "7300000", missingValue: "7300000"},
}

// scaleValue 把旧单位的数值换算成新单位（四舍五入到整数）。
func scaleValue(v float64) int64 {
	return int64(math.Round(v * scale))
}
