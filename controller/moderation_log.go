package controller

import (
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/moderation"
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
		// 降级态必须显式暴露，不能伪装健康（§6.5 四）：拦截模式下审核服务挂掉会
		// fail-close 拒绝全部请求，而这在管理端看起来和「用户都在违规」一模一样。
		// 冻结的节点名单回答「是不是节点挂了」，fail-close 计数回答「拒了多少」。
		"frozen_endpoints": frozenEndpointNames(),
		"fail_close_count": moderation.FailCloseCount(),
		// 因审核不可用而**放行**的次数。比 fail_close 更需要被看见：
		// fail-close 会被用户投诉推到台前，fail-open 是彻底静默的——
		// 审核挂了一整天业务毫无异常，只有这个数能说明有多少请求其实没审。
		"fail_open_count": moderation.FailOpenCount(),
		// 视频审核依赖外部 ffmpeg。缺了不会拒绝请求（那会把部署问题变成事故），
		// 而是**跳过**视频——所以它必须在这里可见：不然「视频都审过了」和
		// 「视频一个都没审」在管理端长得一模一样。
		"ffmpeg_ready":        ffmpegReady(),
		"video_skipped_count": moderation.VideoSkippedCount(),
	})
}

// GetModerationCategoryStats 近 7 天各风险类别的命中数（管理员）。
//
// 给策略编辑器用：把数字放在类别处置的开关旁边，运营才能看着真实数据调，
// 而不是凭感觉。observe 期攒记录的全部意义就在这里。
func GetModerationCategoryStats(c *gin.Context) {
	days, _ := strconv.Atoi(c.DefaultQuery("days", "7"))
	stats, err := model.ModerationCategoryStats(days)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, stats)
}

// ffmpegReady 视频抽帧能力是否可用。
func ffmpegReady() bool {
	ok, _ := moderation.FFmpegAvailable()
	return ok
}

// frozenEndpointNames 当前处于冻结状态的节点名及其恢复时间（秒级时间戳）。
func frozenEndpointNames() map[string]int64 {
	frozen := moderation.FrozenEndpoints()
	out := make(map[string]int64, len(frozen))
	for name, until := range frozen {
		out[name] = until.Unix()
	}
	return out
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

// GetModerationLogMedia 签发被拦媒体的短期查看链接（管理员）。
//
// 与查看文本原文同一套约束：媒体取证材料就是原文，只是换了个模态，没有理由更宽松。
// 所以这里同样是「审计先写，写失败就不返回」——管理员能看违规图这件事的全部约束
// 就是这条痕，痕留不下就不该给内容。
//
// 返回的是签名 URL 而不是把字节代理出来：让浏览器直接去 OBS 取，网关不碰这些字节。
// 链接是短期的（取证桶默认 1 小时），签发即长期有效等于把取证材料变成一条可随手
// 转发的公开链接。
func GetModerationLogMedia(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil || id <= 0 {
		common.ApiErrorMsg(c, "无效的记录 ID")
		return
	}

	objectKey, err := model.GetModerationLogObjectKey(id)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	if objectKey == "" {
		// 三种成因：这条不是媒体记录；是媒体但没判 block（只有 block 才留存）；
		// 或者留存时媒体存储没配好。都不写审计——什么都没泄露出去。
		common.ApiErrorMsg(c, "该记录未留存可复核的媒体：只有被判违规的图片/视频才会留存，且需要已配置媒体存储")
		return
	}

	adminId := c.GetInt("id")
	if err := model.RecordAuditLogWithAdminInfo(adminId, model.LogTypeManage,
		"查看审核记录媒体 #"+strconv.Itoa(id),
		map[string]interface{}{
			"action":              "moderation_log_view_media",
			"moderation_log_id":   id,
			"operator_id":         adminId,
			"operator_username":   common.GetContextKeyString(c, constant.ContextKeyUserName),
			"operator_ip":         c.ClientIP(),
			"operator_user_agent": c.Request.UserAgent(),
		}); err != nil {
		common.SysError("moderation_log 查看媒体审计写入失败，已拒绝签发链接: " + err.Error())
		common.ApiErrorMsg(c, "审计日志写入失败，出于留痕要求已拒绝返回，请稍后重试")
		return
	}

	url, err := moderation.SignEvidenceURL(c, objectKey)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"url": url})
}
