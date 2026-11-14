package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

// 数据看板（/api/data 系列）的时间窗口上限。
//
// 四个接口共用同一个上限：看板的搜索弹窗是一份，界面上却按角色走不同接口
// （管理员走 /api/data，普通用户走 /api/data/self），上限不一致会出现「弹窗能选、
// 提交后报错」。90 天是对齐「近30天 / 上个月 / 自定义」这些预设后能覆盖的最长区间，
// 同时也把无上限的大范围聚合挡在库外。
const dataDashboardMaxRangeSeconds int64 = 90 * 86400

// dataDashboardRangeError 返回超限提示，未超限返回空串。
func dataDashboardRangeError(start, end int64) string {
	if start > 0 && end > 0 && end-start > dataDashboardMaxRangeSeconds {
		return "时间跨度不能超过 90 天"
	}
	return ""
}

func GetAllQuotaDates(c *gin.Context) {
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if msg := dataDashboardRangeError(startTimestamp, endTimestamp); msg != "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": msg,
		})
		return
	}
	username := c.Query("username")
	dates, err := model.GetAllQuotaDates(startTimestamp, endTimestamp, username)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
	return
}

func GetQuotaDatesByUser(c *gin.Context) {
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	if msg := dataDashboardRangeError(startTimestamp, endTimestamp); msg != "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": msg,
		})
		return
	}
	dates, err := model.GetQuotaDataGroupByUser(startTimestamp, endTimestamp)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
}

func GetUserQuotaDates(c *gin.Context) {
	scope, err := resolveSelfDataScope(c)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	// 上限与管理员侧一致（90 天）。这里曾经是「不能超过 1 个月」，而看板的「上个月」
	// 预设刚好会撞上 31 天的月份（10 月/1 月），普通用户点一下就被拒。
	if msg := dataDashboardRangeError(startTimestamp, endTimestamp); msg != "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": msg,
		})
		return
	}
	var dates []*model.QuotaData
	if scope.isSubAccount {
		// 子账户：无绑定返回空；否则从 logs 按绑定 token 集合实时聚合（不动 quota_data，设计 §4.5）。
		if scope.emptyForSubAccount() {
			dates = []*model.QuotaData{}
		} else {
			dates, err = model.GetQuotaDataFromLogsByTokenIds(scope.userId, scope.tokenIds, startTimestamp, endTimestamp)
		}
	} else {
		dates, err = model.GetQuotaDataByUserId(scope.userId, startTimestamp, endTimestamp)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    dates,
	})
	return
}

// groupQuotaRow 看板「分组消耗」的一行。
//
// Group 是**线路（使用分组）**，不是用户档：日志里逐请求记录的分组只有这一个
// （logs.group，auto 已被解析成真实线路）。Known 用于前端标记「该线路当前已不在
// 分组配置里」——历史消耗不能因为分组被删就从表里消失。
type groupQuotaRow struct {
	Group      string `json:"group"`
	Known      bool   `json:"known"`
	Count      int64  `json:"count"`
	Tokens     int64  `json:"tokens"`
	TotalQuota int64  `json:"total_quota"`
	CashQuota  int64  `json:"cash_quota"`
}

// GetGroupQuotaDates 管理端「分组消耗」：按线路分组的 tokens 与金额汇总。
func GetGroupQuotaDates(c *gin.Context) {
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)
	username := c.Query("username")

	if startTimestamp <= 0 {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "起始时间不能为空",
		})
		return
	}
	if endTimestamp <= startTimestamp {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": "结束时间必须晚于起始时间",
		})
		return
	}
	if msg := dataDashboardRangeError(startTimestamp, endTimestamp); msg != "" {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"message": msg,
		})
		return
	}

	stats, err := model.GetGroupQuotaStats(startTimestamp, endTimestamp, username)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	// 只用来标「已下线」，不参与筛选：已删除线路的历史消耗仍要出现在表里。
	knownGroups := ratio_setting.GetGroupRatioCopy()
	rows := make([]groupQuotaRow, 0, len(stats))
	for _, stat := range stats {
		_, known := knownGroups[stat.Group]
		rows = append(rows, groupQuotaRow{
			Group:      stat.Group,
			Known:      known,
			Count:      stat.Count,
			Tokens:     stat.Tokens,
			TotalQuota: stat.TotalQuota,
			CashQuota:  stat.CashQuota,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data":    rows,
	})
}
