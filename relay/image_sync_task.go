package relay

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service/mediastore"

	"github.com/gin-gonic/gin"
)

// 同步生图也进任务日志。
//
// 背景：/v1/images/generations 的同步与异步是同一路径下的两种模式（见
// docs/image-async-task-design.md），但只有异步那条会经 controller.RelayTask 落
// tasks 表。结果是同一个模型、同一个用户，走同步就在任务日志里查无此记录，
// 只在消费日志里有一行——运营与用户都要在两个页面之间对着看。
//
// 这里补的是「出生即终态」的一条记录：状态 SUCCESS、进度 100%、起止时间用真实的
// 请求起止（同步是阻塞的，这一段就是真实耗时，与异步同口径）。生成期间任务日志里
// 看不到它——记录在完成后才写，进度列也就没有中间态。这是刻意的：提交时先写
// 非终态会被轮询器 GetAllUnFinishSyncTasks 捞走，而这种记录没有 upstream_task_id，
// 轮询查不到上游可能误判失败并退款。
//
// 只记成功。同步失败当前不产生任何流水，补一条 quota=0 的 FAILURE 行会让任务日志的
// 计费口径与异步的「预扣 + 退款」对不上，对账时反而要做减法。

// recordSyncImageTask 在同步生图成功结算后补一条任务记录。
//
// best-effort：这时响应早已写回客户端，落库失败只能记日志，不能影响请求结果。
func recordSyncImageTask(c *gin.Context, info *relaycommon.RelayInfo) {
	task := buildSyncImageTask(c, info)
	if task == nil {
		return
	}
	if err := task.Insert(); err != nil {
		logger.LogError(c, "insert sync image task error: "+err.Error())
	}
}

// buildSyncImageTask 组装待落库的同步图片任务记录；非图片端点返回 nil。
// 与落库分开是为了让字段组装能脱离数据库单测。
func buildSyncImageTask(c *gin.Context, info *relaycommon.RelayInfo) *model.Task {
	action := syncImageAction(info.RelayMode)
	if action == "" {
		return nil // 不是图片端点，不该走到这里
	}

	task := model.InitTask(GetTaskPlatform(c), info)
	task.APIProtocol = model.TaskAPIProtocolImage
	task.Properties.SyncMode = true
	task.Action = action
	task.Status = model.TaskStatusSuccess
	task.Progress = "100%"
	task.Quota = common.GetContextKeyInt(c, constant.ContextKeySyncConsumedQuota)
	task.SubmitTime = info.StartTime.Unix()
	task.StartTime = task.SubmitTime
	task.FinishTime = common.GetTimestamp()
	task.PrivateData.TokenId = info.TokenId
	// 与异步同理：体验区用的是内存临时令牌，Id 恒为 0，回查 tokens 表必然落空。
	task.PrivateData.TokenName = c.GetString("token_name")

	// 结果引用。存 obs://<key> 占位符而不是签名 URL（后者有有效期），查询时实时签发，
	// 与异步链路共用 ResolveResultURL 这一个 hook。
	//
	// 多图（n>1）只留第一张：任务表一行只有一个 ResultURL，而任务日志的预览也只展示
	// 一张。要完整支持得给任务表加结果数组，不在本次范围内。
	//
	// 拿不到 key 的情况是常态而非异常——客户端点名要 b64_json、渠道配了结果透传、
	// 或走的是没有接 OBS 落盘的适配器。这时记录照常写，只是没有图可预览。
	if keys := relaycommon.GetSyncImageOBSKeys(c); len(keys) > 0 {
		task.PrivateData.ResultURL = mediastore.WrapKey(keys[0])
	}
	return task
}

// syncImageAction 把同步图片的 relay mode 映射成任务 action。
// 取值必须与异步侧（middleware.ImageAsyncConvert 写入的 constant.TaskActionImage*）
// 一致，否则任务日志的「类型」列会把同一件事显示成两种。
func syncImageAction(relayMode int) string {
	switch relayMode {
	case relayconstant.RelayModeImagesGenerations:
		return constant.TaskActionImageGenerate
	case relayconstant.RelayModeImagesEdits:
		return constant.TaskActionImageEdit
	default:
		return ""
	}
}
