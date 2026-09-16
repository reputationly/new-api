package relay

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
)

// applyCancelledEcho 把被取消的任务的对外状态改写成 cancelled。
//
// 为什么是后处理而不是在源头改：OpenAI 形态的视频响应由各渠道适配器的
// ConvertToOpenAIVideo 各自渲染，而它们只拿得到 model.Task 里的 Status —— 任务表没有
// CANCELLED 这一态，取消复用的是 FAILURE + PrivateData.Cancelled（与异步图片同一套
// 做法，见 relay.BuildImageJob 的说明；加第八态要动状态机、所有映射和前端展示）。
// 在这里统一补一刀，比让十几个适配器各记一遍这条规则可靠。
//
// 只改 status，不动 error：调用方需要知道它是被取消的（error.message = "用户取消"）。
func applyCancelledEcho(task *model.Task, data []byte) []byte {
	if task == nil || !task.PrivateData.Cancelled || len(data) == 0 {
		return data
	}
	var payload map[string]any
	if err := common.Unmarshal(data, &payload); err != nil || payload == nil {
		return data
	}
	// 只在响应确实带 status 字段时改写，不凭空加一个（与 applyAggregateModelEcho 同理）。
	if _, ok := payload["status"]; !ok {
		return data
	}
	payload["status"] = dto.VideoStatusCancelled
	out, err := common.Marshal(payload)
	if err != nil {
		return data
	}
	return out
}
