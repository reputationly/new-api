package relay

import (
	"errors"
	"fmt"
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
	// 审核没跑完导致的拒绝不能说成「你的内容违规」——那是服务故障，用户改不了也
	// 申诉不了，503 才表达出「稍后可再试」（§9.2.3）。
	if result.Action == moderation.ActionError {
		return service.TaskErrorWrapperLocal(
			errors.New(result.Reason),
			"moderation_unavailable",
			http.StatusServiceUnavailable,
		)
	}
	// skip-retry 语义：换个渠道再试一次不会让内容变得合规，重试纯属浪费。
	return service.TaskErrorWrapperLocal(
		errors.New(service.SensitiveRefusalTextWithReason(result.Reason)),
		"sensitive_words_detected",
		http.StatusBadRequest,
	)
}

// taskMediaModerationDoneKey 标记本次请求的媒体已审过。与文本各记各的：
// 两者可能一个通过一个被拦，共用一个标记会让重试时漏掉还没审过的那一半。
const taskMediaModerationDoneKey = "moderation_task_media_checked"

// moderateTaskMedia 审核任务请求里上传的图片与视频（挂载点 C-1）。
//
// 与挂载点 A 同一段位置，但**不实现成 mediaResolver**。
// `relay/task_ref_expand.go:104 activeMediaResolvers` 有渠道门禁：GPUStackPlus 直接
// return nil，dataURLResolver 只在 offloadChannelTypes 那八个白名单渠道里装配。
// 挂成 resolver 会让自建渠道与全部非白名单渠道（Gemini / Vertex / Sora / Suno …）的
// 上传图**静默不审**——正是这套系统最不能出的那类错（§7 C-1）。
//
// 所以这里是独立的一相：复用 walkTaskRequestMedia 遍历器，不复用 activeMediaResolvers
// 的装配链。两种形态都要能吃：白名单渠道此时已是 OBS 签名 URL，非白名单仍是原始 data-url。
func moderateTaskMedia(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	if _, done := c.Get(taskMediaModerationDoneKey); done {
		return nil
	}
	if !moderation.MediaActive(info.UsingGroup, info.OriginModelName) {
		// 不设 done 标记：这次没审是因为配置没开，不是审过了。运营在重试窗口内
		// 打开开关时，下一次尝试应当照常审。
		return nil
	}
	c.Set(taskMediaModerationDoneKey, true)

	items := extractTaskMedia(c)
	if len(items) == 0 {
		return nil
	}

	result := moderation.ModerateMedia(c, &moderation.Request{
		UserId:    info.UserId,
		TokenId:   info.TokenId,
		ChannelId: info.ChannelId,
		Username:  common.GetContextKeyString(c, constant.ContextKeyUserName),
		Group:     info.UsingGroup,
		ModelName: info.OriginModelName,
		TaskId:    info.PublicTaskID,
		RequestId: info.RequestId,
		Stage:     moderation.StageInputMedia,
	}, items)
	if !result.Blocked {
		return nil
	}

	logger.LogWarn(c, "content moderation blocked task media: provider="+result.Provider+
		" categories="+strings.Join(result.Categories, ",")+" item="+result.BlockedItem)
	if result.Action == moderation.ActionError {
		return service.TaskErrorWrapperLocal(
			errors.New(result.Reason),
			"moderation_unavailable",
			http.StatusServiceUnavailable,
		)
	}
	return service.TaskErrorWrapperLocal(
		errors.New(service.SensitiveRefusalTextWithReason(result.Reason)),
		"sensitive_words_detected",
		http.StatusBadRequest,
	)
}

// extractTaskMedia 收集请求里所有该送审的媒体。
//
// 用 walkTaskRequestMedia 而不是自己列字段：各平台字段名不统一，它是仓库里唯一
// 覆盖全的遍历方式——顶层 Image/Images/InputReference 之外还会递归进 metadata 的
// 任意深度（doubao 的 content[].video_url.url 就藏在那儿）。
//
// resolve 回调原样返回输入值，所以这次遍历不改写任何东西。注意 req.Images 与
// req.Metadata 是 slice/map，即便 req 是值拷贝，写回也会落到原对象上——正因如此
// 这里必须原样返回，任何改写都会漏进真实请求。
func extractTaskMedia(c *gin.Context) []moderation.MediaItem {
	v, exists := c.Get("task_request")
	if !exists {
		return nil
	}
	var req relaycommon.TaskSubmitReq
	switch r := v.(type) {
	case relaycommon.TaskSubmitReq:
		req = r
	case *relaycommon.TaskSubmitReq:
		req = *r
	default:
		// suno 用自己的 DTO，且没有媒体上传字段。其它类型出现时不出声就是静默漏审。
		if _, isSuno := v.(*dto.SunoSubmitReq); !isSuno {
			if _, isSunoVal := v.(dto.SunoSubmitReq); !isSunoVal {
				logger.LogWarn(c, "content moderation: 未登记的 task_request 类型，该平台媒体未送审 platform="+
					c.GetString("platform"))
			}
		}
		return nil
	}

	var items []moderation.MediaItem
	seen := make(map[string]bool)
	collect := func(value string) (string, error) {
		fileType, ok := moderation.ClassifyMedia(value)
		if !ok {
			return value, nil
		}
		if seen[value] {
			// 同一张图出现在 image 与 metadata.src_ref_images[0] 是常见形态，只审一次。
			return value, nil
		}
		seen[value] = true
		items = append(items, moderation.MediaItem{
			URL:   value,
			Type:  fileType,
			Field: fmt.Sprintf("media[%d]", len(items)),
		})
		return value, nil
	}
	_, _ = walkTaskRequestMedia(&req, collect)
	return items
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
