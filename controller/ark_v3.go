package controller

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/arkv3"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// 火山方舟 v3 协议兼容层里「任务管理」那两个端点（列表 / 取消或删除）。
// 提交与按 ID 查询复用既有的 RelayTask / RelayTaskFetch 链路，不在这里。

// arkV3ScanLimit 是**需要在 Go 里复筛时**的扫描窗口。
//
// 三个维度逼出这条路径：filter.model 与 filter.service_tier 存在 Properties 这个
// JSON 列里，failed/cancelled/expired 三态则要读 PrivateData 与 Task.Data —— 三种
// 数据库的 JSON 查询语法互不兼容，只能取回来在 Go 里筛。候选集已被 api_protocol
// 收窄到方舟自己的任务，取满这个数很难发生；真取满了就**明确报错**，不返回一份
// 看不出被截断的列表。
const arkV3ScanLimit = 5000

// ArkV3GetTask 实现 GET /api/v3/contents/generations/tasks/{task_id}。
//
// 不复用 controller.RelayTaskFetch：那条链路是给统一契约 / OpenAI 兼容端点用的，
// 会先尝试 Gemini/Vertex 的实时拉取、再按渠道适配器渲染 —— 对方舟任务两条都用不上，
// 而它「任务不存在」回的是 400，本协议该回 404。绕开它反而更短也更准。
func ArkV3GetTask(c *gin.Context) {
	taskId := strings.TrimSpace(c.Param("task_id"))
	if taskId == "" {
		arkv3.AbortWithError(c, arkv3.NewBadRequest("task_id is required"))
		return
	}
	// 必须带 user_id 查：否则任何人拿到 task_id 就能读他人的任务。
	task, exist, err := model.GetByTaskId(c.GetInt("id"), taskId)
	if err != nil {
		arkv3.AbortWithError(c, arkv3.NewServerError("failed to query task: "+err.Error()))
		return
	}
	// 非方舟端点提交的任务在本协议下就是不存在。放行的话会给一个从没走过这条协议的
	// 任务编出 content.video_url 与各种回显字段 —— 而同一个 platform 底下还跑着 TTS、
	// 音乐、超分，那条 url 后面很可能根本不是视频。
	if !exist || !arkv3.IsArkTask(task) {
		arkv3.AbortWithError(c, arkv3.NewNotFound("task not found: "+taskId))
		return
	}
	body, err := arkv3.BuildQueryBody(task)
	if err != nil {
		arkv3.AbortWithError(c, arkv3.NewServerError("failed to render task: "+err.Error()))
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", body)
}

// ArkV3ListTasks 实现 GET /api/v3/contents/generations/tasks。
//
// 与官方的一处差异：官方只保留最近 7 天，我们不设时间窗 —— 记录在我们这儿不过期，
// 对调用方只会更好。这条差异成立的前提是枚举本身精确，所以下面三条路径都不允许静默截断。
func ArkV3ListTasks(c *gin.Context) {
	filter := arkv3.ListFilter{
		Status:      strings.ToLower(strings.TrimSpace(c.Query("filter.status"))),
		TaskIDs:     parseArkV3TaskIDs(c),
		Model:       strings.TrimSpace(c.Query("filter.model")),
		ServiceTier: strings.ToLower(strings.TrimSpace(c.Query("filter.service_tier"))),
	}

	var apiErr *arkv3.APIError
	if filter.PageNum, apiErr = parseArkV3Int(c.Query("page_num"), "page_num"); apiErr != nil {
		arkv3.AbortWithError(c, apiErr)
		return
	}
	if filter.PageSize, apiErr = parseArkV3Int(c.Query("page_size"), "page_size"); apiErr != nil {
		arkv3.AbortWithError(c, apiErr)
		return
	}
	if apiErr = arkv3.ValidateListFilter(&filter); apiErr != nil {
		arkv3.AbortWithError(c, apiErr)
		return
	}

	userId := c.GetInt("id")

	// 路径一：给了精确 task_ids 就走索引查询（task_id 有索引），结果集天然完整。
	// 这条必须与常规列表分开 —— 合流的话，任务多的调用方指名查一个更早的 ID 会拿到
	// 空结果，而这恰恰是 filter.task_ids 最典型的用法（拿一批 ID 批量对状态）。
	if len(filter.TaskIDs) > 0 {
		ids := make([]any, 0, len(filter.TaskIDs))
		for _, id := range filter.TaskIDs {
			ids = append(ids, id)
		}
		tasks, err := model.GetByTaskIds(userId, ids)
		if err != nil {
			arkv3.AbortWithError(c, arkv3.NewServerError("failed to query tasks: "+err.Error()))
			return
		}
		// GetByTaskIds 不带 Order（Suno 的 fetch 也在用它，不关心顺序，不去改它的行为）。
		sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID > tasks[j].ID })
		c.JSON(http.StatusOK, arkv3.FilterAndPage(tasks, filter))
		return
	}

	statuses, needsGoFilter := arkv3.InternalStatusesFor(filter.Status)

	// 路径二：还有 SQL 筛不了的维度。取回该协议下的候选集，在 Go 里完成筛选与切页。
	if needsGoFilter || filter.Model != "" || filter.ServiceTier != "" {
		tasks, err := model.ListAllTasksByProtocol(userId, model.TaskAPIProtocolArkV3, statuses, arkV3ScanLimit)
		if err != nil {
			arkv3.AbortWithError(c, arkv3.NewServerError("failed to query tasks: "+err.Error()))
			return
		}
		if len(tasks) >= arkV3ScanLimit {
			// 宁可报错也不返回一份看不出被截断的列表。
			common.SysLog(fmt.Sprintf("[ark-v3] user %d 的方舟任务数已达扫描窗口 %d", userId, arkV3ScanLimit))
			arkv3.AbortWithError(c, arkv3.NewBadRequest(fmt.Sprintf(
				"too many tasks to enumerate with this filter (over %d); narrow the query with filter.task_ids",
				arkV3ScanLimit)))
			return
		}
		c.JSON(http.StatusOK, arkv3.FilterAndPage(tasks, filter))
		return
	}

	// 路径三：常规列表。筛选与分页全部在 SQL 里完成，total 精确，没有扫描窗口。
	tasks, total, err := model.ListTasksByProtocol(userId, model.TaskAPIProtocolArkV3,
		statuses, (filter.PageNum-1)*filter.PageSize, filter.PageSize)
	if err != nil {
		arkv3.AbortWithError(c, arkv3.NewServerError("failed to query tasks: "+err.Error()))
		return
	}
	c.JSON(http.StatusOK, arkv3.BuildListPage(tasks, total))
}

// ArkV3DeleteTask 实现 DELETE /api/v3/contents/generations/tasks/{task_id}：
// 排队中的任务取消并退款，已结束的任务删记录。判定见 arkv3.DeleteAction。
//
// 官方规定操作成功不返回业务响应体，所以两条路径都是 204。
func ArkV3DeleteTask(c *gin.Context) {
	taskId := strings.TrimSpace(c.Param("task_id"))
	if taskId == "" {
		arkv3.AbortWithError(c, arkv3.NewBadRequest("task_id is required"))
		return
	}
	userId := c.GetInt("id")
	// 必须带 user_id 查：否则任何人拿到 task_id 就能取消或删除他人的任务。
	task, exist, err := model.GetByTaskId(userId, taskId)
	if err != nil {
		arkv3.AbortWithError(c, arkv3.NewServerError("failed to query task: "+err.Error()))
		return
	}
	// 非方舟端点提交的任务在本协议下**就是不存在**，与查不到共用同一句文案、刻意不可区分。
	//
	// 这道闸不是洁癖：任务表里还躺着体验区、Suno、MJ 的记录，不校验的话这个兼容层就成了
	// 把任意历史任务标成失败并退款的入口。
	if !exist || task == nil || !arkv3.IsArkTask(task) {
		arkv3.AbortWithError(c, arkv3.NewNotFound("task not found: "+taskId))
		return
	}

	action, apiErr := arkv3.DeleteAction(task)
	if apiErr != nil {
		arkv3.AbortWithError(c, apiErr)
		return
	}

	if action == arkv3.ActionCancel {
		if apiErr = cancelArkV3Task(c, task); apiErr != nil {
			arkv3.AbortWithError(c, apiErr)
			return
		}
		c.Status(http.StatusNoContent)
		return
	}

	// 协议侧软删：任务从方舟的查询与列表里消失（与官方 DELETE 的可观测行为一致），
	// 但行保留。BeforeSave 会据此把 api_protocol 清空，SQL 侧的列表也就查不到它了。
	task.Properties.ArkV3.Deleted = true
	if err := task.Update(); err != nil {
		arkv3.AbortWithError(c, arkv3.NewServerError("failed to delete task: "+err.Error()))
		return
	}
	c.Status(http.StatusNoContent)
}

// cancelArkV3Task 取消一个尚未开跑的任务。
//
// 先抢本地终态，**赢了才动上游**（与 RelayVideoCancel 同一套理由，见那边的长注释）：
// 方舟的 DELETE 对终态任务是「删记录」而不是「取消」，抢在 CAS 之前发，就会出现
// 「对调用方回 400 说取消不了、却已经把上游记录删掉」这种对外对内不一致的结果。
func cancelArkV3Task(c *gin.Context, task *model.Task) *arkv3.APIError {
	// 与轮询循环存在竞态：它可能正好把同一条任务推到 SUCCESS/FAILURE。CAS 输了说明
	// 轮询已经处理完并会自己结算/退款 —— 这里就不能再退一次，也不该再去碰上游。
	oldStatus := task.Status
	task.Status = model.TaskStatusFailure
	task.Progress = "100%"
	task.FinishTime = common.GetTimestamp()
	task.FailReason = "用户取消"
	task.PrivateData.Cancelled = true

	won, err := task.UpdateWithStatus(oldStatus)
	if err != nil {
		return arkv3.NewServerError("failed to cancel task: " + err.Error())
	}
	if !won {
		// 轮询抢先把它推到了终态。官方对 running / 终态的 DELETE 是拒绝的，这里如实
		// 报错而不是假装取消成功 —— 任务已经在跑或已经出片，费用照收。
		logger.LogInfo(c, "ark v3: task "+task.TaskID+" already transitioned before cancel, skip refund")
		return arkv3.NewBadRequest(fmt.Sprintf(
			"task %s already left the queue before the cancellation took effect and can no longer be cancelled", task.TaskID))
	}

	// CAS 赢了 = 取消这件事已经定下（不再轮询、不交付、马上退款）。此刻无论上游处于
	// 什么状态，那条记录我们都不会再用，可以放心通知它停下。
	cancelUpstreamTask(c, task)

	// 全额退款。任务还没开跑，上游没有产生任何用量。RefundTaskQuota 本身不幂等，
	// 靠上面那次 CAS 保证只调用一次。
	if task.Quota != 0 {
		service.RefundTaskQuota(c.Request.Context(), task, "用户取消")
	}
	return nil
}

// parseArkV3TaskIDs 收 filter.task_ids。官方写的是「多个 ID 需要重复传递参数名」，
// 这里额外兼容逗号分隔 —— 两种写法都是常见误用，认下来不会产生歧义。
func parseArkV3TaskIDs(c *gin.Context) []string {
	var out []string
	for _, raw := range c.QueryArray("filter.task_ids") {
		for _, part := range strings.Split(raw, ",") {
			if v := strings.TrimSpace(part); v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

func parseArkV3Int(raw, field string) (int, *arkv3.APIError) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		// 官方把两个分页参数的范围都写死在 1–500，显式传 0 或负数就是非法。
		// 空串（没传）走上面那条，由 ValidateListFilter 填默认值。
		return 0, arkv3.NewBadRequest(fmt.Sprintf("%s must be a positive integer, got %q", field, raw))
	}
	return v, nil
}
