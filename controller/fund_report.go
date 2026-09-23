package controller

import (
	"strconv"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// 收入对账报表接口。设计见 docs/revenue-reconciliation-design.md §七。
//
// 与现有的成本对账（跟供应商账单比对）平级，二者在前端合成「对账管理」的两个 tab：
// 一个对收入（收客户多少），一个对成本（付供应商多少）。

// fundReportMaxRangeDays 单次查询的时间跨度上限。
// 消耗侧要扫 logs 表，无界范围会拖垮库——与成本对账的 31 天上限同口径。
const fundReportMaxRangeDays = 92

// parseFundRange 解析时间范围，缺省为最近 30 天。
func parseFundRange(c *gin.Context) (start, end int64, ok bool) {
	now := common.GetTimestamp()
	start, _ = strconv.ParseInt(c.Query("start"), 10, 64)
	end, _ = strconv.ParseInt(c.Query("end"), 10, 64)
	if end <= 0 {
		end = now
	}
	if start <= 0 {
		start = end - 30*24*3600
	}
	if start > end {
		common.ApiErrorMsg(c, "开始时间不能晚于结束时间")
		return 0, 0, false
	}
	if end-start > int64(fundReportMaxRangeDays)*24*3600 {
		common.ApiErrorMsg(c, "查询跨度不得超过 "+strconv.Itoa(fundReportMaxRangeDays)+" 天")
		return 0, 0, false
	}
	return start, end, true
}

// AdminFundSummary GET /api/reconcile/admin/fund/summary
func AdminFundSummary(c *gin.Context) {
	start, end, ok := parseFundRange(c)
	if !ok {
		return
	}
	summary, err := model.GetFundSummary(start, end)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, summary)
}

// AdminPlanFulfillment GET /api/reconcile/admin/plan/fulfillment
//
// 套餐履约率报表（设计文档 §8.4、P7）。与收入对账同一个时间窗口径与跨度上限：
// 同样要扫 logs 表。
func AdminPlanFulfillment(c *gin.Context) {
	start, end, ok := parseFundRange(c)
	if !ok {
		return
	}
	report, err := service.BuildPlanFulfillmentReport(start, end)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, report)
}

// AdminFundConsistency GET /api/reconcile/admin/fund/consistency
//
// 不平几乎不是算错，而是有代码绕过流水表直接改了余额——这是发现此类 bug 最早的信号。
func AdminFundConsistency(c *gin.Context) {
	report, err := model.CheckFundConsistency()
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, report)
}

// AdminFundEntries GET /api/reconcile/admin/fund/entries
func AdminFundEntries(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	q := model.FundEntryQuery{
		Account: c.Query("account"),
		Kind:    c.Query("kind"),
		Source:  c.Query("source"),
	}
	q.Start, _ = strconv.ParseInt(c.Query("start"), 10, 64)
	q.End, _ = strconv.ParseInt(c.Query("end"), 10, 64)
	q.UserId, _ = strconv.Atoi(c.Query("user_id"))
	q.OperatorId, _ = strconv.Atoi(c.Query("operator_id"))

	list, total, err := model.ListFundEntries(q, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(list)
	common.ApiSuccess(c, pageInfo)
}

// AdminInitFundBaseline POST /api/reconcile/admin/fund/baseline
//
// 把当前所有用户余额记成期初流水，作为自洽校验的起点。流水表是后加的，没有基线时
// 历史余额无从解释、校验恒不平，那个告警也就废了。
//
// 幂等：同一用户的期初 ref 唯一，重复调用不会重复记账。
func AdminInitFundBaseline(c *gin.Context) {
	operatorId := c.GetInt("id")
	created, err := model.InitFundBaseline(operatorId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	model.RecordLog(operatorId, model.LogTypeManage,
		"初始化资金对账基线，写入期初流水 "+strconv.Itoa(created)+" 条")
	common.ApiSuccess(c, gin.H{"created": created})
}

// AdminFundExportCSV GET /api/reconcile/admin/fund/export
// 导出流水明细。财务侧要拿进 Excel 核对，页面上的分页表格代替不了。
func AdminFundExportCSV(c *gin.Context) {
	start, end, ok := parseFundRange(c)
	if !ok {
		return
	}
	q := model.FundEntryQuery{
		Start:   start,
		End:     end,
		Account: c.Query("account"),
		Kind:    c.Query("kind"),
		Source:  c.Query("source"),
	}
	// 导出不分页但设硬上限，避免一次拉穿内存
	list, _, err := model.ListFundEntries(q, 0, 50000)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", "attachment; filename=fund_entries.csv")
	// UTF-8 BOM：不加的话 Excel 打开中文列名是乱码
	_, _ = c.Writer.WriteString("\xEF\xBB\xBF")
	_, _ = c.Writer.WriteString("时间,用户ID,账户,性质,来源,额度变动,实收(元),操作人,单号,备注\n")
	for _, e := range list {
		_, _ = c.Writer.WriteString(
			time.Unix(e.CreatedAt, 0).Format("2006-01-02 15:04:05") + "," +
				strconv.Itoa(e.UserId) + "," +
				e.Account + "," + e.Kind + "," + e.Source + "," +
				strconv.FormatInt(e.QuotaDelta, 10) + "," +
				strconv.FormatFloat(float64(e.CashFen)/100, 'f', 2, 64) + "," +
				strconv.Itoa(e.OperatorId) + "," +
				e.RefType + "/" + e.RefId + "," +
				csvEscape(e.Remark) + "\n")
	}
}

// csvEscape 处理备注里的逗号与引号——凭证号备注里带逗号会把列冲散。
func csvEscape(s string) string {
	needQuote := false
	for _, r := range s {
		if r == ',' || r == '"' || r == '\n' {
			needQuote = true
			break
		}
	}
	if !needQuote {
		return s
	}
	out := `"`
	for _, r := range s {
		if r == '"' {
			out += `""`
		} else {
			out += string(r)
		}
	}
	return out + `"`
}
