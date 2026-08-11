package controller

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/minimaxv2"

	"github.com/gin-gonic/gin"
)

// MiniMax v2 官方协议兼容层里「任务管理」那两个端点(列表 / 删除)。
// 提交与按 ID 查询复用既有的 RelayTask / RelayTaskFetch 链路,不在这里。

// miniMaxV2ModelFilterScanLimit 是**带 filter.model 时**的扫描窗口。
//
// 只有这条路径需要窗口:模型名存在 Properties 这个 JSON 列里,三种数据库的 JSON 查询
// 语法互不兼容,只能取回来在 Go 里筛。候选集已被 api_protocol 收窄到 v2 自己的任务,
// 取满这个数是很难发生的;真取满了就**明确报错**,不返回一份不完整的列表。
//
// 其余路径(常规列表、按精确 task_ids)都是精确查询,不经过窗口。
const miniMaxV2ModelFilterScanLimit = 5000

// MiniMaxV2ListTasks 实现 GET /v2/query/video_generation。
//
// 与官方的一处差异:官方只保留最近 7 天,我们不设时间窗 —— 记录在我们这儿不过期,
// 对用户只会更好。这条差异成立的前提是枚举本身是精确的,所以下面三条路径都不允许
// 静默截断。
func MiniMaxV2ListTasks(c *gin.Context) {
	filter := minimaxv2.ListFilter{
		Status:   strings.ToLower(strings.TrimSpace(c.Query("filter.status"))),
		TaskIDs:  parseMiniMaxV2TaskIDs(c),
		Model:    strings.TrimSpace(c.Query("filter.model")),
		TaskType: strings.ToLower(strings.TrimSpace(c.Query("filter.task_type"))),
	}

	var apiErr *minimaxv2.APIError
	if filter.PageNum, apiErr = parseMiniMaxV2PositiveInt(c.Query("page_num"), "page_num"); apiErr != nil {
		minimaxv2.AbortWithError(c, apiErr)
		return
	}
	if filter.PageSize, apiErr = parseMiniMaxV2PositiveInt(c.Query("page_size"), "page_size"); apiErr != nil {
		minimaxv2.AbortWithError(c, apiErr)
		return
	}
	if apiErr = minimaxv2.ValidateListFilter(&filter); apiErr != nil {
		minimaxv2.AbortWithError(c, apiErr)
		return
	}

	userId := c.GetInt("id")

	// 路径一:给了精确 task_ids 就走索引查询(task_id 有索引),结果集天然完整。
	// 这条必须与常规列表分开——合流的话,任务多的用户指名查一个更早的 ID 会拿到空结果,
	// 而这恰恰是 filter.task_ids 最典型的用法(拿一批 ID 批量对状态)。
	if len(filter.TaskIDs) > 0 {
		ids := make([]any, 0, len(filter.TaskIDs))
		for _, id := range filter.TaskIDs {
			ids = append(ids, id)
		}
		tasks, err := model.GetByTaskIds(userId, ids)
		if err != nil {
			minimaxv2.AbortWithError(c, minimaxv2.NewServerError("failed to query tasks: "+err.Error()))
			return
		}
		// GetByTaskIds 不带 Order(Suno 的 fetch 也在用它,不关心顺序,不去改它的行为)。
		sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID > tasks[j].ID })
		c.JSON(http.StatusOK, minimaxv2.FilterAndPage(tasks, filter))
		return
	}

	// 官方枚举里有、我们永远产生不了的取值一律早退成空集。**不能当成「不限」传下去** ——
	// 那会把全部任务都返回,正好是筛选语义的反面。
	//   status=cancelled  :任务一提交就下发引擎,没有可取消的窗口;
	//   task_type≠generation:另外两个门类的端点是如实 501 的。
	statuses, statusMatchable := minimaxv2.InternalStatusesFor(filter.Status)
	if !statusMatchable || !minimaxv2.MatchesOurTaskType(filter.TaskType) {
		c.JSON(http.StatusOK, minimaxv2.BuildListPage(nil, 0))
		return
	}

	// 路径二:还要按模型名筛。模型名在 Properties 这个 JSON 列里,跨库筛不了,
	// 只能取回来在 Go 里筛。候选集已被 api_protocol 收窄到 v2 自己的任务。
	if filter.Model != "" {
		tasks, err := model.ListAllTasksByProtocol(userId,
			model.TaskAPIProtocolMiniMaxV2, statuses, miniMaxV2ModelFilterScanLimit)
		if err != nil {
			minimaxv2.AbortWithError(c, minimaxv2.NewServerError("failed to query tasks: "+err.Error()))
			return
		}
		if len(tasks) >= miniMaxV2ModelFilterScanLimit {
			// 宁可报错也不返回一份看不出被截断的列表。
			common.SysLog(fmt.Sprintf("[minimax-v2] user %d 的 v2 任务数已达 filter.model 扫描窗口 %d",
				userId, miniMaxV2ModelFilterScanLimit))
			minimaxv2.AbortWithError(c, minimaxv2.NewBadRequest(fmt.Sprintf(
				"too many tasks to enumerate with filter.model (over %d); narrow the query with filter.status or filter.task_ids",
				miniMaxV2ModelFilterScanLimit)))
			return
		}
		c.JSON(http.StatusOK, minimaxv2.FilterAndPage(tasks, filter))
		return
	}

	// 路径三:常规列表。筛选与分页全部在 SQL 里完成,total 精确,没有扫描窗口。
	tasks, total, err := model.ListTasksByProtocol(userId, model.TaskAPIProtocolMiniMaxV2,
		statuses, (filter.PageNum-1)*filter.PageSize, filter.PageSize)
	if err != nil {
		minimaxv2.AbortWithError(c, minimaxv2.NewServerError("failed to query tasks: "+err.Error()))
		return
	}
	c.JSON(http.StatusOK, minimaxv2.BuildListPage(tasks, total))
}

// MiniMaxV2DeleteTask 实现 DELETE /v2/video_generation/{task_id}。
// 只能删记录,不能取消 —— 原因见 minimaxv2.DeleteAction。
func MiniMaxV2DeleteTask(c *gin.Context) {
	taskId := strings.TrimSpace(c.Param("task_id"))
	if taskId == "" {
		minimaxv2.AbortWithError(c, minimaxv2.NewBadRequest("invalid params: task_id is required"))
		return
	}
	userId := c.GetInt("id")
	task, exist, err := model.GetByTaskId(userId, taskId)
	if err != nil {
		minimaxv2.AbortWithError(c, minimaxv2.NewServerError("failed to query task: "+err.Error()))
		return
	}
	// 非 v2 提交的任务在本协议下**就是不存在**,与查不到共用同一句文案、刻意不可区分。
	//
	// 这道闸不是洁癖:任务表里还躺着体验区、Suno、MJ 的记录,而全仓在此之前没有任何
	// 用户可达的删除接口(api-router 用户侧只有查询/下载/分享)。不校验的话,这个
	// 「MiniMax 视频兼容层」就成了删除任意历史记录的唯一入口 —— 删掉的是 GetUserTask
	// 读的同一张表,OBS 上的成品还会变成孤儿。
	if !exist || task == nil || !minimaxv2.IsV2Task(task) {
		minimaxv2.AbortWithError(c, minimaxv2.NewBadRequest("invalid task_id: "+taskId))
		return
	}
	action, apiErr := minimaxv2.DeleteAction(task)
	if apiErr != nil {
		minimaxv2.AbortWithError(c, apiErr)
		return
	}
	// 协议侧软删：任务从 v2 的查询与列表里消失（与官方 DELETE 的可观测行为一致），
	// 但行保留 —— 同一行还背着 content.url、下载、分享链接、remix 原任务、
	// `task:<task_id>` 产物引用五六个与本协议无关的功能。
	// BeforeSave 会据此把 api_protocol 清空，SQL 侧的列表也就查不到它了。
	task.Properties.MiniMaxV2.Deleted = true
	if err := task.Update(); err != nil {
		minimaxv2.AbortWithError(c, minimaxv2.NewServerError("failed to delete task: "+err.Error()))
		return
	}
	c.JSON(http.StatusOK, minimaxv2.DeleteResponse{TaskID: taskId, Action: action, Status: action})
}

// MiniMaxV2NotImplemented 用于官方有、我们做不到的端点。写清楚为什么做不到,
// 不要装作支持。
func MiniMaxV2NotImplemented(message string) gin.HandlerFunc {
	return func(c *gin.Context) {
		minimaxv2.AbortWithError(c, minimaxv2.NewNotImplemented(message))
	}
}

// parseMiniMaxV2TaskIDs 收 filter.task_ids。官方写的是「可以给多个」,既兼容重复参数
// 也兼容逗号分隔。
func parseMiniMaxV2TaskIDs(c *gin.Context) []string {
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

func parseMiniMaxV2PositiveInt(raw, field string) (int, *minimaxv2.APIError) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return 0, minimaxv2.NewBadRequest(fmt.Sprintf("invalid params: %s must be a positive integer, got %q", field, raw))
	}
	return v, nil
}
