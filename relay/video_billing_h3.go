package relay

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
)

// H3 的"请求秒数 → 交付秒数"换算在计费侧的接线。
//
// H3 的出片帧数按 17n+5 栅格向上吸附(见 common/h3_duration.go),所以按秒计价的模型
// 存在一个固定偏差:请求 1 s 实际出 1.625 s。两种口径都能自圆其说,但必须选一个并
// 只在一个地方实现:
//
//   - 按**交付**计价:与实际烧掉的算力一致(1 s 的单成本是 1.625 s 的 GPU 时间);
//   - 按**请求**计价:与客户看到的数字一致(他填的就是 1)。
//
// 这里选交付,理由是 per_second 的语义就是"每秒多少钱",而秒是产物属性;请求值只
// 决定档位(per_call 的列名是整数秒,继续用请求值,见 applyVideoPricing)。偏差最大
// 0.625 s(仅 1 s 档),最小 0.042 s(20 s 档),不会出现"1 s 收 2 s 钱"那种跳档。
//
// 判据是引擎族声明而不是模型名 substring —— 与 common.IsH3VideoModel 同一口径。
func h3DeliveredBillingSeconds(info *relaycommon.RelayInfo, requestedSeconds int) float64 {
	if info == nil || requestedSeconds <= 0 {
		return 0
	}
	if !isH3VideoInfo(info) {
		return 0
	}
	_, delivered := common.H3DeliveredDuration(requestedSeconds)
	return delivered
}

// isH3VideoInfo 判这次请求是否走 H3 引擎族。
//
// 候选名要从**指针安全**的位置取:RelayInfo 里 OriginModelName 是结构体字段,
// 而 UpstreamModelName 挂在 *ChannelMeta 上 —— 后者在单测/非渠道上下文里是 nil,
// 直接写 info.UpstreamModelName 会 nil deref(不是编译错误,是线上/测试时的 panic)。
func isH3VideoInfo(info *relaycommon.RelayInfo) bool {
	if info == nil {
		return false
	}
	names := make([]string, 0, 2)
	if n := strings.TrimSpace(info.OriginModelName); n != "" {
		names = append(names, n)
	}
	if info.ChannelMeta != nil {
		if n := strings.TrimSpace(info.ChannelMeta.UpstreamModelName); n != "" {
			names = append(names, n)
		}
	}
	return common.IsH3VideoModel(names...)
}

// effectiveVideoBillingSeconds 计价时真正乘上去的秒数:有交付口径就用它,否则用请求值。
func effectiveVideoBillingSeconds(frozenSeconds int, deliveredSeconds float64) float64 {
	if deliveredSeconds > 0 {
		return deliveredSeconds
	}
	return float64(frozenSeconds)
}

// ApplyH3DurationSnapshot 在任务落库前冻结 H3 的时长快照。
//
// 为什么必须落库而不是查询时算:查询发生在几百秒后的轮询之后,那时请求体早已不在,
// 而任务的 Properties 里也没有请求时长(其它协议各有自己的快照结构,通用链路没有)。
//
// 只在 H3 引擎族上写;非 H3 任务是 nil,老行反序列化也是 nil。
func ApplyH3DurationSnapshot(c *gin.Context, info *relaycommon.RelayInfo, task *model.Task) {
	if c == nil || info == nil || task == nil {
		return
	}
	if !isH3VideoInfo(info) {
		return
	}
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return
	}
	_, seconds, _ := relaycommon.ResolveVideoDims(&req)
	seconds = videoBillingSeconds(c, &req, seconds)
	if seconds <= 0 {
		// 没指定时长 → 引擎按默认帧数出片,网关猜不准,宁可不写。
		return
	}
	frames, delivered := common.H3DeliveredDuration(seconds)
	task.Properties.H3 = &model.H3Properties{
		RequestedSeconds: seconds,
		DeliveredSeconds: delivered,
		Frames:           frames,
	}
}
