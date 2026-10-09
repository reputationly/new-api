package service

import (
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

// 聚合流水线两段的使用日志标签键。两段都带上它们,运营与客户才能对回同一个聚合请求。
const (
	LogKeyAggregateModel  = "aggregate_model"
	LogKeyPipelineStage   = "pipeline_stage"
	LogKeyAggregateTaskID = "aggregate_task_id"

	LogStageGenerate = "generate"
	LogStageUpscale  = "upscale"
)

// 超分段走的是自调用(父任务以客户身份提交),父任务把归属放进请求头带过去。
// 这些头只用于日志展示归类,不参与计费,也不影响引擎出片。
const (
	AggregateParentTaskHeader  = "X-Aggregate-Parent-Task"
	AggregatePublicModelHeader = "X-Aggregate-Public-Model"
	AggregateStageHeader       = "X-Aggregate-Stage"
)

// PipelineLogLabels 本次提交属于聚合流水线的哪一段,以及它归属哪个聚合请求。
//
// 两种来路:
//   - 客户调的是聚合名:展开阶段写进上下文的标签。ownTaskID 是本任务的公开 ID,
//     作为聚合任务 ID(单段聚合时本任务就是聚合任务本身)。
//   - 超分段:父任务自调用时带的请求头,聚合任务 ID 取父任务的公开 ID。
//
// 都不是的返回 nil。标签只用于使用日志的展示归类,不参与任何金额计算。
func PipelineLogLabels(c *gin.Context, ownTaskID string) map[string]string {
	if c == nil {
		return nil
	}
	if v, ok := common.GetContextKey(c, constant.ContextKeyAggregateLabels); ok {
		if base, ok := v.(map[string]string); ok && len(base) > 0 {
			out := make(map[string]string, len(base)+1)
			for k, val := range base {
				out[k] = val
			}
			if ownTaskID != "" {
				out[LogKeyAggregateTaskID] = ownTaskID
			}
			return out
		}
	}
	if c.GetHeader(AggregateStageHeader) == LogStageUpscale {
		parent := strings.TrimSpace(c.GetHeader(AggregateParentTaskHeader))
		if parent == "" {
			return nil
		}
		out := map[string]string{
			LogKeyPipelineStage:   LogStageUpscale,
			LogKeyAggregateTaskID: parent,
		}
		if m := strings.TrimSpace(c.GetHeader(AggregatePublicModelHeader)); m != "" {
			out[LogKeyAggregateModel] = m
		}
		return out
	}
	return nil
}
