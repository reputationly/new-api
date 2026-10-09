package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
)

// captureUpscaleRequest 把超分自调用指向假服务端,记录收到的请求头与请求体。
func captureUpscaleRequest(t *testing.T) (headers *http.Header, body *map[string]any) {
	t.Helper()
	var hdr http.Header
	var parsed map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr = r.Header.Clone()
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		_ = common.Unmarshal(buf, &parsed)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"task_id":"pub-2"}`))
	}))
	t.Cleanup(srv.Close)

	origEP, origLookup := videosEndpoint, lookupTaskByPublicID
	t.Cleanup(func() { videosEndpoint, lookupTaskByPublicID = origEP, origLookup })
	videosEndpoint = func() string { return srv.URL }
	lookupTaskByPublicID = func(string) (*model.Task, bool, error) {
		sub := &model.Task{TaskID: "sub-public-1"}
		sub.PrivateData.UpstreamTaskID = "up-stage2"
		return sub, true, nil
	}
	return &hdr, &parsed
}

// 超分段的顶层 size 放交付档位:计费行跟着引擎实际出的画质走;metadata 里的目标短边不变。
func TestUpscaleSubmitBillsDeliveredTierAndCarriesParentHeaders(t *testing.T) {
	hdr, body := captureUpscaleRequest(t)
	agg := &model.TaskAggregateInfo{
		PublicModel:      "h3-2k",
		UpscaleModel:     "seedvr2",
		UpscaleTarget:    "2k",
		UpscaleDelivered: "1080P",
		CallerKey:        "sk-customer",
	}

	_, err := submitUpscaleTask(context.Background(), agg, "/nfs/x.mp4", "task_parent")
	require.NoError(t, err)

	require.Equal(t, "1080P", (*body)["size"], "计费行取顶层 size,必须是交付档位")
	meta := (*body)["metadata"].(map[string]any)
	require.EqualValues(t, 1440, meta["target_short_edge"], "客户要的目标仍照常下发给会读它的引擎")
	require.Equal(t, "task_parent", hdr.Get(AggregateParentTaskHeader))
	require.Equal(t, "h3-2k", hdr.Get(AggregatePublicModelHeader))
	require.Equal(t, LogStageUpscale, hdr.Get(AggregateStageHeader))
}

// 没配交付档位:回落到客户要的目标,与改动前一致。
func TestUpscaleSubmitFallsBackToTargetWhenNoDelivered(t *testing.T) {
	_, body := captureUpscaleRequest(t)
	agg := &model.TaskAggregateInfo{UpscaleModel: "swiftvr", UpscaleTarget: "2k", CallerKey: "sk-customer"}

	_, err := submitUpscaleTask(context.Background(), agg, "/nfs/x.mp4", "task_parent")
	require.NoError(t, err)

	require.Equal(t, "2k", (*body)["size"])
}

// 日志标签的三种来路:生成段(上下文)、超分段(请求头)、两者都没有。
func TestPipelineLogLabelsFromContextAndHeaders(t *testing.T) {
	t.Run("生成段:展开阶段写进上下文,聚合任务 ID 取本任务的公开 ID", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		common.SetContextKey(c, constant.ContextKeyAggregateLabels, map[string]string{
			LogKeyAggregateModel: "h3-2k",
			LogKeyPipelineStage:  LogStageGenerate,
		})
		labels := PipelineLogLabels(c, "task_gen")
		require.Equal(t, map[string]string{
			LogKeyAggregateModel:  "h3-2k",
			LogKeyPipelineStage:   LogStageGenerate,
			LogKeyAggregateTaskID: "task_gen",
		}, labels)
	})

	t.Run("单段聚合:只带聚合名,不标段", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		common.SetContextKey(c, constant.ContextKeyAggregateLabels, map[string]string{
			LogKeyAggregateModel: "h3-768",
		})
		labels := PipelineLogLabels(c, "task_only")
		require.Equal(t, "h3-768", labels[LogKeyAggregateModel])
		require.Equal(t, "task_only", labels[LogKeyAggregateTaskID])
		_, hasStage := labels[LogKeyPipelineStage]
		require.False(t, hasStage)
	})

	t.Run("超分段:自调用的请求头,聚合任务 ID 取父任务", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
		c.Request.Header.Set(AggregateStageHeader, LogStageUpscale)
		c.Request.Header.Set(AggregateParentTaskHeader, "task_parent")
		c.Request.Header.Set(AggregatePublicModelHeader, "h3-2k")
		labels := PipelineLogLabels(c, "task_sub")
		require.Equal(t, map[string]string{
			LogKeyAggregateModel:  "h3-2k",
			LogKeyPipelineStage:   LogStageUpscale,
			LogKeyAggregateTaskID: "task_parent",
		}, labels)
	})

	t.Run("普通请求:不打标签", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
		require.Nil(t, PipelineLogLabels(c, "task_plain"))
	})

	t.Run("超分头缺了父任务 ID:不打标签,不编造归属", func(t *testing.T) {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
		c.Request.Header.Set(AggregateStageHeader, LogStageUpscale)
		require.Nil(t, PipelineLogLabels(c, "task_sub"))
	})
}

// 结算与退款日志照样带上提交时冻结的标签,两段才对得回去。
func TestSettlementLogCarriesPipelineLabels(t *testing.T) {
	task := &model.Task{TaskID: "task_sub"}
	task.PrivateData.PipelineLabels = map[string]string{
		LogKeyAggregateModel:  "h3-2k",
		LogKeyPipelineStage:   LogStageUpscale,
		LogKeyAggregateTaskID: "task_parent",
	}

	other := taskBillingOther(task)

	require.Equal(t, "h3-2k", other[LogKeyAggregateModel])
	require.Equal(t, LogStageUpscale, other[LogKeyPipelineStage])
	require.Equal(t, "task_parent", other[LogKeyAggregateTaskID])
}
