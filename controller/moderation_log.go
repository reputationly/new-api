package controller

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

// 审核记录的管理端接口。见 docs/content-moderation-design.md §10.1。

// GetModerationLogs 分页查审核记录（管理员）。
//
// 响应里没有 ContentEnc——ModerationLog 的 json tag 是 "-"，列表接口和导出
// 都拿不到密文。要看原文只能走 GetModerationLogContent，那条路带鉴权和留痕。
func GetModerationLogs(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	userId, _ := strconv.Atoi(c.Query("user_id"))
	channelId, _ := strconv.Atoi(c.Query("channel_id"))
	startTimestamp, _ := strconv.ParseInt(c.Query("start_timestamp"), 10, 64)
	endTimestamp, _ := strconv.ParseInt(c.Query("end_timestamp"), 10, 64)

	logs, total, err := model.GetModerationLogs(model.ModerationLogQuery{
		StartTimestamp: startTimestamp,
		EndTimestamp:   endTimestamp,
		UserId:         userId,
		Username:       c.Query("username"),
		Group:          c.Query("group"),
		ChannelId:      channelId,
		ModelName:      c.Query("model_name"),
		Action:         c.Query("action"),
		Source:         c.Query("source"),
		Category:       c.Query("category"),
		Word:           c.Query("word"),
		RequestId:      c.Query("request_id"),
		StartIdx:       pageInfo.GetStartIdx(),
		PageSize:       pageInfo.GetPageSize(),
	})
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(logs)
	common.ApiSuccess(c, pageInfo)
}

// GetModerationStatus 审核运行态（§8.4）。
//
// 存在的理由是「为什么我看不到原文」这类问题在配置页上答不了：
// 原文留存取决于一个只在环境变量里的密钥，配没配、配错没配错，
// 从任何界面都看不出来，只能去翻服务日志。
func GetModerationStatus(c *gin.Context) {
	common.ApiSuccess(c, gin.H{
		// 分三态而不是一个布尔：「没配」和「配错了」的处置完全不同，
		// 前者是没启用这个能力，后者是有人以为启用了但其实没有。
		"encrypt_key_ready":         common.ModerationKeyReady(),
		"encrypt_key_misconfigured": common.ModerationKeyMisconfigured(),
		// 队列满时丢的审核记录数。不展示的话，审核记录里的空洞无法解释——
		// 而「记录里没有」和「没发生过」在事后排查时是分不清的（§9.2）。
		"dropped_logs": model.ModerationDroppedCount(),
	})
}

// GetModerationLogContent 解密查看被拦内容的原文（管理员）。
//
// 这个动作本身要留痕：谁、什么时候、看了哪条记录。不留痕的话「管理员能看原文」
// 就是一个没有任何约束的权限，出了事连是谁看的都查不到（§10.1 访问控制第 2 条）。
func GetModerationLogContent(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的记录 ID")
		return
	}

	content, err := model.GetModerationLogContent(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if content == "" {
		// 记录在、原文不在。两种成因：这条不是 block 记录（按 §10.1 只有 block 留原文），
		// 或者写入时没配 MODERATION_ENCRYPT_KEY。不写审计——什么都没泄露出去。
		common.ApiErrorMsg(c, "该记录未留存原文：只有拦截记录才加密留存，且需在写入时已配置 MODERATION_ENCRYPT_KEY")
		return
	}

	// 审计先写再返回，且写失败就不返回。
	//
	// 「先返回后写」会让写失败变成一次无痕访问；「写了不看返回值」是同一个问题的
	// 温和版本——日志库故障期间照样无痕。管理员能看原文这件事的全部约束就是这条痕，
	// 痕留不下就不该给内容，宁可这个功能在日志库故障时不可用。
	adminId := c.GetInt("id")
	if err := model.RecordAuditLogWithAdminInfo(adminId, model.LogTypeManage,
		"查看审核记录原文 #"+strconv.Itoa(id),
		map[string]interface{}{
			"action":              "moderation_log_view_content",
			"moderation_log_id":   id,
			"operator_id":         adminId,
			"operator_username":   common.GetContextKeyString(c, constant.ContextKeyUserName),
			"operator_ip":         c.ClientIP(),
			"operator_user_agent": c.Request.UserAgent(),
		}); err != nil {
		common.SysError("moderation_log 查看原文审计写入失败，已拒绝返回原文: " + err.Error())
		common.ApiErrorMsg(c, "审计日志写入失败，出于留痕要求已拒绝返回原文，请稍后重试")
		return
	}

	common.ApiSuccess(c, gin.H{"content": content})
}
