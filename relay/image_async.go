package relay

import (
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
)

// 异步图片链路的渠道能力判定（docs/image-async-task-design.md §4、§6）。

// asyncImageChannels 支持异步图片任务的渠道类型。
//
// 加一项之前必须确认三件事，缺一不可：
//  1. 上游有真实的异步任务接口（提交返回 ID、可查询、可取消）；
//  2. 本仓该渠道的 TaskAdaptor 能提交**图片**任务 —— relay/channel/task/ 下多数适配器
//     是**视频语义**写的（提交体、状态解析都按视频），仅凭「上游文档说支持异步」不够；
//  3. FetchTask / ParseTaskResult 能解析图片结果，尤其是产物 URL 的取法。
//
// 反例存档：阿里 DashScope 的 wanx 图片上游确实是异步的，但 relay/channel/task/ali
// 是视频语义，而同步的 relay/channel/ali 走的是 DashScope 同步图片接口。接它需要单独
// 改适配器，不是往这张表里加一行就行。
var asyncImageChannels = map[int]bool{
	constant.ChannelTypeGPUStackPlus: true,
}

// IsAsyncImageSubmit 判断当前请求是否为异步图片提交。
// 判据是 relay_mode —— 它由 middleware.ImageAsyncConvert 在识别到 async 开关后设置。
func IsAsyncImageSubmit(info *relaycommon.RelayInfo) bool {
	return info != nil && info.RelayMode == relayconstant.RelayModeImageSubmit
}

// IsImageTask 判断一条任务记录是否经异步图片协议提交。
//
// 查询与取消端点必须拿它当守卫:task_id 在**所有**任务类型间共用同一个 task_xxxx 格式,
// 图片与视频还共用同一个 platform 列。不校验的话,用户拿自己的视频 task_id 打到
// DELETE /v1/images/generations/{id} 就能把那条视频任务标成失败并退款 —— 一次端点用错
// 就毁掉一个正在跑的任务;打到 GET 则会拿到一个 image.generation.job 外壳,
// data[].url 后面很可能是段视频。
//
// 这与 MiniMax v2 兼容层的 minimaxv2.IsV2Task 是同一条规矩,理由见 relay_task.go:440
// 的注释:「非本协议提交的任务,在本协议下就是不存在」。
func IsImageTask(task *model.Task) bool {
	return task != nil && task.APIProtocol == model.TaskAPIProtocolImage
}

// checkAsyncImageSupported 在选定渠道后校验它是否支持异步图片。
//
// 判定必须放在 Distribute 之后（那时才有 channel_type），且要在预扣费之前 ——
// 拒绝不该产生扣费/退款往返。
//
// 标为 LocalError（skip-retry）:能力缺失不是瞬时故障，跨渠道重试是碰运气。
//
// ⚠️ 已知局限：若同一个模型名同时挂在 GPUStackPlus 和别的渠道上，Distribute 选到后者
// 就会在这里 400，而重试本可能选到前者。当前自建图片模型（z-image / qwen-image /
// qwen-image-edit / hunyuan-image-3 / ernie-image-turbo）只挂 GPUStackPlus，不触发。
// **若将来出现混挂，要把这里改成可重试**（去掉 Local 标记），让 relay 继续找支持的渠道。
func checkAsyncImageSupported(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	if !IsAsyncImageSubmit(info) {
		return nil
	}
	if asyncImageChannels[info.ChannelType] {
		return nil
	}
	modelName := info.OriginModelName
	if modelName == "" {
		modelName = c.GetString("original_model")
	}
	return service.TaskErrorWrapperLocal(
		fmt.Errorf("模型 %s 所在渠道不支持异步生成，请去掉 async 参数使用同步模式", modelName),
		"async_not_supported", http.StatusBadRequest)
}
