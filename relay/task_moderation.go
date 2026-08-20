package relay

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/moderation"
)

// 任务提交路径的内容审核挂载点（docs/content-moderation-design.md §7 挂载点 A）。
//
// 位置：rewriteTaskMedia 之后、ModelPriceHelperPerCall / PreConsumeBilling 之前。
// 在 rewriteTaskMedia 之后是因为那一步会把 task:<id> 引用展开成实际值；
// 在预扣费之前是因为审核拒绝不该产生扣费/退款往返。

// taskModerationDoneKey 标记本次请求已审过。
//
// RelayTaskSubmit 跑在 controller 的换渠道重试循环里（controller/relay.go:584），
// 上游失败重试会把审核再跑一遍。同一份内容审两次，结论必然相同，但会多出一条
// 审核记录——observe 模式下就是同一请求两条 block 记录，把「拦了多少」直接数错。
// 文本链路的审核挂在循环外，没有这个问题；任务链路只能在这里自己拦。
const taskModerationDoneKey = "moderation_task_checked"

// moderateTaskRequest 审核任务请求里的文本字段。同一请求内只实际执行一次。
func moderateTaskRequest(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	// 首次审核若判拦，会以 skip-retry 的形式直接终止，走不到重试；
	// 所以能再次进到这里的，一定是首次审核放行、之后死在上游的请求。
	// 直接放行即可，不必缓存上次的结论。
	if _, done := c.Get(taskModerationDoneKey); done {
		return nil
	}
	c.Set(taskModerationDoneKey, true)

	texts := extractTaskTexts(c, info)
	if len(texts) == 0 {
		return nil
	}

	result := moderation.Moderate(c, &moderation.Request{
		Texts:     texts,
		UserId:    info.UserId,
		TokenId:   info.TokenId,
		ChannelId: info.ChannelId,
		Username:  common.GetContextKeyString(c, constant.ContextKeyUserName),
		Group:     info.UsingGroup,
		ModelName: info.OriginModelName,
		TaskId:    info.PublicTaskID,
		RequestId: info.RequestId,
		Stage:     moderation.StagePrompt,
	})
	if !result.Blocked {
		return nil
	}

	logger.LogWarn(c, "content moderation blocked task: provider="+result.Provider+
		" categories="+strings.Join(result.Categories, ","))
	// skip-retry 语义：换个渠道再试一次不会让内容变得合规，重试纯属浪费。
	return service.TaskErrorWrapperLocal(
		errors.New(service.SensitiveRefusalTextWithReason(result.Reason)),
		"sensitive_words_detected",
		http.StatusBadRequest,
	)
}

// extractTaskTexts 取出该平台请求里需要送审的文本字段。
//
// 按 task_request 的**类型**分派而不是按 platform：真正的分歧就在类型上——
// 除 suno 外所有平台存的都是 relaycommon.TaskSubmitReq，只有 suno 存 *dto.SunoSubmitReq
// （relay/channel/task/suno/adaptor.go:62）。按 platform 分派要枚举十来个渠道类型字符串，
// 且新增渠道时容易漏掉而静默漏审。
//
// 未覆盖：TaskSubmitReq.Metadata 里的自定义字段（如部分平台的 negative_prompt）。
// 那是个 map[string]interface{}，里面同时混着 data-url 之类的二进制串，
// 无差别送审会把几 MB 的 base64 当文本审。要覆盖需要按平台列白名单键，见 §15。
func extractTaskTexts(c *gin.Context, info *relaycommon.RelayInfo) []string {
	v, exists := c.Get("task_request")
	if !exists {
		return nil
	}

	var texts []string
	switch req := v.(type) {
	case relaycommon.TaskSubmitReq:
		texts = []string{req.Prompt}
	case *relaycommon.TaskSubmitReq:
		texts = []string{req.Prompt}
	case *dto.SunoSubmitReq:
		texts = []string{req.Prompt, req.GptDescriptionPrompt, req.Title, req.Tags}
	case dto.SunoSubmitReq:
		texts = []string{req.Prompt, req.GptDescriptionPrompt, req.Title, req.Tags}
	default:
		// 出现未登记的类型说明有平台用了新的请求结构。这里必须出声：
		// 静默 return nil 就是静默漏审，而漏审是这套系统最不能出的错（§8.6）。
		logger.LogWarn(c, "content moderation: 未登记的 task_request 类型，该平台文本未送审 platform="+
			c.GetString("platform"))
		return nil
	}

	out := make([]string, 0, len(texts))
	for _, t := range texts {
		if strings.TrimSpace(t) != "" {
			out = append(out, t)
		}
	}
	return out
}
