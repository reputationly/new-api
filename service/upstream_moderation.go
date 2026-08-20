package service

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// 上游渠道判违规的回收（docs/content-moderation-design.md §9.3）。
//
// 各渠道适配器已经把上游的拒绝理由写进 ContextKeyAdminRejectReason
// （gemini_block_reason=…、claude_stop_reason=refusal、openai_finish_reason=content_filter），
// 但它此前只落到消费日志 other 里的一个 JSON 字段——按「哪些用户被上游拒过」
// 这类问题去查，等于要全表扫 JSON。这里把它同时收进 moderation_log，
// 让自审和上游拒绝在同一张表里可比、可筛。
//
// 放在 service 而不是 service/moderation：后者 import service（用 AcSearch），
// 反向依赖会成环。这里只需要 model.RecordModerationLog，不需要审核链。

// upstreamRejectionRecordedKey 标记本次请求的上游拒绝已记过。
//
// 收口有两处：成功结算走 PostTextConsumeQuota，失败返回走 relay 的错误 defer。
// 正常情况下两者互斥，但重试链路上「前一次被拒、后一次成功」会让 reason 残留在
// context 里被成功路径再读一次。没这个标记就会出现同一请求两条 upstream 记录，
// 而「上游拒了多少次」正是这张表要回答的问题，多算一次就直接错了。
const upstreamRejectionRecordedKey = "moderation_upstream_rejection_recorded"

// upstreamRejectStage 判断一个拒绝理由是不是真的违规判定，以及它属于哪个阶段。
//
// 两件事都不能靠「reason 非空」一刀切：
//
//   - gemini_empty_candidates 是上游返回了空响应且没给 block reason，
//     那是供应商故障，不是用户违规。记成 block 等于把上游抽风算进违规率。
//   - OpenAI 的 content_filter 和 Claude 的 refusal 描述的是**模型输出**被过滤，
//     Gemini 的 PromptFeedback 才是输入侧。全记成 prompt 会让按 stage 做的分析
//     把输出过滤算到用户输入头上。
//
// 认不出来的理由一律不记：宁可少一条记录，也不要一条说不清是什么的记录。
// 新增渠道信号时在这里登记，忘了登记的表现是「这个渠道没有记录」，
// 比「有记录但分类是猜的」容易发现。
func upstreamRejectStage(reason string) (stage string, ok bool) {
	switch {
	case strings.HasPrefix(reason, "gemini_block_reason="):
		// PromptFeedback.BlockReason —— 输入被 Gemini 判违规。
		return "prompt", true
	case reason == "openai_finish_reason=content_filter",
		reason == "claude_stop_reason=refusal":
		// 模型已经开始生成才被掐断/拒答，判的是输出。
		return "output", true
	default:
		// gemini_empty_candidates 等技术性失败落在这里。
		return "", false
	}
}

// RecordUpstreamRejection 把上游的违规判定记成一条 source=upstream 的审核记录。
// reason 为空或不是已登记的违规信号时什么都不做；同一请求内幂等。
func RecordUpstreamRejection(c *gin.Context, relayInfo *relaycommon.RelayInfo, reason string) {
	if reason == "" || relayInfo == nil || c == nil {
		return
	}
	stage, ok := upstreamRejectStage(reason)
	if !ok {
		return
	}
	if _, done := c.Get(upstreamRejectionRecordedKey); done {
		return
	}
	c.Set(upstreamRejectionRecordedKey, true)

	detail := ""
	if b, err := common.Marshal(map[string]string{"reject_reason": reason}); err == nil {
		detail = string(b)
	}

	// ChannelMeta 是内嵌指针，InitChannelMeta 之前为 nil（relay_info.go:184）。
	// 这个函数现在也从 relay 的错误 defer 里调用，那里覆盖的错误态比成功结算宽得多，
	// 直接读 relayInfo.ChannelId 会 panic。渠道号没有就记 0，比带走整个请求强。
	channelId := 0
	if relayInfo.ChannelMeta != nil {
		channelId = relayInfo.ChannelId
	}

	// 不留内容：到这一步请求已经发给上游了，原始 prompt 不在手边，
	// 硬去重建只会得到一份和实际送审内容对不上的东西。ContentHash 留空即可。
	model.RecordModerationLog(&model.ModerationLog{
		UserId:    relayInfo.UserId,
		TokenId:   relayInfo.TokenId,
		ChannelId: channelId,
		Username:  common.GetContextKeyString(c, constant.ContextKeyUserName),
		Group:     relayInfo.UsingGroup,
		RequestId: relayInfo.RequestId,
		ModelName: relayInfo.OriginModelName,
		Source:    model.ModerationSourceUpstream,
		Stage:     stage,
		Modality:  "text",
		Action:    model.ModerationActionBlock,
		// 上游真的把请求拒了，不是我们的观察模式，所以恒为已执行。
		Enforced: true,
		// Categories 留空：上游给的是各家自己的理由码（SAFETY / content_filter），
		// 硬映射到我们那九类只会造出一个看着精确、实则猜的字段。原始码在 Detail 里。
		Provider: "upstream",
		Detail:   detail,
	})
}
