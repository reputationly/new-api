package relay

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

// applyAggregateModelEcho 把视频任务响应里的 model 字段换回客户实际调用的聚合模型名。
//
// 任务本身记的是**展开后的生成段模型**(计费与日志都按它走,这是分段计费的要求),
// 但客户调的是聚合模型名 —— 响应里回显一个他没调过的名字既不符合"请求什么返回什么"
// 的常规语义,也把内部编排暴露了出去。
//
// 只改这一个字段。stage / 内部各段的模型名一概不外泄:客户要的是"一个模型、一个任务",
// 我们内部跑几段是实现细节。同理,增强后的提示词也不回传(见 TaskAggregateInfo)。
//
// 任何一步出错都返回原始字节 —— 回显是锦上添花,不该因为它让一个本来正常的响应损坏。
func applyAggregateModelEcho(task *model.Task, data []byte) []byte {
	if task == nil || task.PrivateData.Aggregate == nil {
		return data
	}
	publicModel := task.PrivateData.Aggregate.PublicModel
	if publicModel == "" || len(data) == 0 {
		return data
	}
	var payload map[string]any
	if err := common.Unmarshal(data, &payload); err != nil || payload == nil {
		return data
	}
	// 只在响应确实带 model 字段时改写,不凭空加一个 —— 不同端点的响应形状不同,
	// 凭空注入可能让客户端的严格解析失败。
	if _, ok := payload["model"]; !ok {
		return data
	}
	payload["model"] = publicModel
	out, err := common.Marshal(payload)
	if err != nil {
		return data
	}
	return out
}
