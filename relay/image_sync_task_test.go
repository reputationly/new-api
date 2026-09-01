package relay

import (
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"

	"github.com/gin-gonic/gin"
)

func syncImageCtx(channelType int) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/generations", nil)
	c.Set("channel_type", channelType)
	c.Set("token_name", "my-key")
	return c
}

func syncImageInfo(relayMode int) *relaycommon.RelayInfo {
	info := &relaycommon.RelayInfo{
		RelayMode:   relayMode,
		ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeGPUStackPlus},
		StartTime:   time.Unix(1700000000, 0),
	}
	info.OriginModelName = "z-image"
	info.UpstreamModelName = "z-image-turbo"
	info.UserId = 42
	info.TokenId = 7
	info.UsingGroup = "vip"
	return info
}

// 同步记录必须带上 image 协议标记。这是查询/取消端点的守卫（relay.IsImageTask），
// 也是任务列表区分图片与视频的唯一依据 —— 两者共用同一个 platform 列。
// 不打这个标记，记录进了库也查不出来。
func TestBuildSyncImageTaskMarksImageProtocol(t *testing.T) {
	task := buildSyncImageTask(syncImageCtx(constant.ChannelTypeGPUStackPlus), syncImageInfo(relayconstant.RelayModeImagesGenerations))
	if task == nil {
		t.Fatal("expected a task record for /v1/images/generations")
	}
	if task.APIProtocol != model.TaskAPIProtocolImage {
		t.Errorf("APIProtocol = %q, want %q", task.APIProtocol, model.TaskAPIProtocolImage)
	}
	if !task.Properties.SyncMode {
		t.Error("Properties.SyncMode must be set so sync records are distinguishable when debugging")
	}
}

// 出生即终态。写成非终态会被轮询器 GetAllUnFinishSyncTasks 捞走（它的判据正是
// progress != 100% 且 status 不是 SUCCESS/FAILURE），而同步记录没有 upstream_task_id，
// 轮询查不到上游，可能误判失败并退款。
func TestBuildSyncImageTaskIsTerminalOnInsert(t *testing.T) {
	task := buildSyncImageTask(syncImageCtx(constant.ChannelTypeGPUStackPlus), syncImageInfo(relayconstant.RelayModeImagesGenerations))
	if task.Status != model.TaskStatusSuccess {
		t.Errorf("Status = %q, want %q", task.Status, model.TaskStatusSuccess)
	}
	if task.Progress != "100%" {
		t.Errorf("Progress = %q, want 100%%", task.Progress)
	}
}

// action 必须与异步侧（middleware.ImageAsyncConvert 写的 constant.TaskActionImage*）
// 取同一个值，否则任务日志的「类型」列会把同一件事显示成两种，筛选也对不上。
func TestBuildSyncImageTaskActionMatchesAsync(t *testing.T) {
	cases := []struct {
		relayMode int
		want      string
	}{
		{relayconstant.RelayModeImagesGenerations, constant.TaskActionImageGenerate},
		{relayconstant.RelayModeImagesEdits, constant.TaskActionImageEdit},
	}
	for _, tc := range cases {
		task := buildSyncImageTask(syncImageCtx(constant.ChannelTypeGPUStackPlus), syncImageInfo(tc.relayMode))
		if task == nil {
			t.Fatalf("relayMode %d: expected a task record", tc.relayMode)
		}
		if task.Action != tc.want {
			t.Errorf("relayMode %d: Action = %q, want %q", tc.relayMode, task.Action, tc.want)
		}
	}
}

// 非图片端点不该产生任务记录。ImageHelper 只服务图片，但守卫失效的代价是
// 每个 relay 请求都往 tasks 表里灌一行。
func TestBuildSyncImageTaskSkipsNonImageRelayModes(t *testing.T) {
	for _, mode := range []int{relayconstant.RelayModeChatCompletions, relayconstant.RelayModeEmbeddings, relayconstant.RelayModeUnknown} {
		if task := buildSyncImageTask(syncImageCtx(constant.ChannelTypeGPUStackPlus), syncImageInfo(mode)); task != nil {
			t.Errorf("relayMode %d produced a task record, want none", mode)
		}
	}
}

// 结果引用必须落成 obs://<key> 占位符，不能是签名 URL —— 后者有有效期，存进库
// 过期就成了一条打不开的死链。签名由 ResolveResultURL 在读取时实时生成。
func TestBuildSyncImageTaskStoresOBSPlaceholderNotSignedURL(t *testing.T) {
	c := syncImageCtx(constant.ChannelTypeGPUStackPlus)
	relaycommon.AppendSyncImageOBSKeys(c, "t2i/z-image/42/img_abc.png")

	task := buildSyncImageTask(c, syncImageInfo(relayconstant.RelayModeImagesGenerations))
	if got, want := task.PrivateData.ResultURL, "obs://t2i/z-image/42/img_abc.png"; got != want {
		t.Errorf("ResultURL = %q, want %q", got, want)
	}
}

// 重试循环复用同一个 gin.Context，而落盘发生在返回可重试错误之前
// （gpustackplus 落完 OBS 才组响应，那一步失败会重试）。不清上一次尝试的 key，
// 任务记录取 keys[0] 就指向了上一次尝试的图——跨渠道重试时那甚至是另一个渠道的产物，
// 于是任务日志预览到的和客户端实际拿到的不是同一张。
func TestResetSyncImageOBSKeysDropsPreviousAttempt(t *testing.T) {
	c := syncImageCtx(constant.ChannelTypeGPUStackPlus)
	relaycommon.AppendSyncImageOBSKeys(c, "attempt1/stale.png") // 第一次尝试落了盘，随后失败重试

	relaycommon.ResetSyncImageOBSKeys(c) // 第二次尝试开始（ImageHelper 开头）
	relaycommon.AppendSyncImageOBSKeys(c, "attempt2/delivered.png")

	task := buildSyncImageTask(c, syncImageInfo(relayconstant.RelayModeImagesGenerations))
	if got, want := task.PrivateData.ResultURL, "obs://attempt2/delivered.png"; got != want {
		t.Errorf("ResultURL = %q, want %q — 记录必须指向实际交付给客户端的那张图", got, want)
	}
}

// 重置后没有新的 key，就该是「无图记录」，而不是回退到上一次尝试的图。
func TestResetSyncImageOBSKeysLeavesNoStaleResult(t *testing.T) {
	c := syncImageCtx(constant.ChannelTypeGPUStackPlus)
	relaycommon.AppendSyncImageOBSKeys(c, "attempt1/stale.png")
	relaycommon.ResetSyncImageOBSKeys(c)

	if keys := relaycommon.GetSyncImageOBSKeys(c); len(keys) != 0 {
		t.Fatalf("keys = %v, want none after reset", keys)
	}
	task := buildSyncImageTask(c, syncImageInfo(relayconstant.RelayModeImagesGenerations))
	if task.PrivateData.ResultURL != "" {
		t.Errorf("ResultURL = %q, want empty", task.PrivateData.ResultURL)
	}
}

// 拿不到 key 是常态（客户端要 b64_json、渠道配了透传、适配器没接 OBS 落盘），
// 这时记录照常写，只是没有图 —— 不能因为没有图就丢掉整条记录。
func TestBuildSyncImageTaskWithoutOBSKeyStillRecords(t *testing.T) {
	task := buildSyncImageTask(syncImageCtx(constant.ChannelTypeGPUStackPlus), syncImageInfo(relayconstant.RelayModeImagesGenerations))
	if task == nil {
		t.Fatal("a task must be recorded even when no image was persisted")
	}
	if task.PrivateData.ResultURL != "" {
		t.Errorf("ResultURL = %q, want empty when nothing was persisted", task.PrivateData.ResultURL)
	}
}

// quota 只能从结算后的 context 读：PostTextConsumeQuota 自己算完就扣了，不返回值。
// 漏读的话任务日志里每条同步记录的费用都是 0，与消费日志对不上。
func TestBuildSyncImageTaskReadsSettledQuota(t *testing.T) {
	c := syncImageCtx(constant.ChannelTypeGPUStackPlus)
	common.SetContextKey(c, constant.ContextKeySyncConsumedQuota, 1234)

	task := buildSyncImageTask(c, syncImageInfo(relayconstant.RelayModeImagesGenerations))
	if task.Quota != 1234 {
		t.Errorf("Quota = %d, want 1234", task.Quota)
	}
}

// 起止时间要用请求的真实起止：同步是阻塞的，这一段就是真实耗时，任务日志的
// 「花费时间」列靠它。用落库时刻当提交时间会把耗时抹成 0。
func TestBuildSyncImageTaskUsesRealElapsedWindow(t *testing.T) {
	info := syncImageInfo(relayconstant.RelayModeImagesGenerations)
	task := buildSyncImageTask(syncImageCtx(constant.ChannelTypeGPUStackPlus), info)

	if task.SubmitTime != info.StartTime.Unix() {
		t.Errorf("SubmitTime = %d, want request start %d", task.SubmitTime, info.StartTime.Unix())
	}
	if task.FinishTime <= task.SubmitTime {
		t.Errorf("FinishTime = %d must be after SubmitTime = %d", task.FinishTime, task.SubmitTime)
	}
}

// platform 取渠道类型数字，与异步侧 controller.RelayTask 走的 GetTaskPlatform 同源。
// 不一致的话任务列表按平台筛选会漏掉同步记录。
func TestBuildSyncImageTaskPlatformMatchesChannelType(t *testing.T) {
	task := buildSyncImageTask(syncImageCtx(constant.ChannelTypeGPUStackPlus), syncImageInfo(relayconstant.RelayModeImagesGenerations))
	if want := strconv.Itoa(constant.ChannelTypeGPUStackPlus); string(task.Platform) != want {
		t.Errorf("Platform = %q, want %q", task.Platform, want)
	}
}

// 令牌信息要在提交时冻结。体验区用的是内存临时令牌（Id 恒为 0、从未入库），
// 靠 TokenId 回查 tokens 表必然落空，任务日志的令牌列就空着。
func TestBuildSyncImageTaskFreezesTokenIdentity(t *testing.T) {
	task := buildSyncImageTask(syncImageCtx(constant.ChannelTypeGPUStackPlus), syncImageInfo(relayconstant.RelayModeImagesGenerations))
	if task.PrivateData.TokenId != 7 {
		t.Errorf("TokenId = %d, want 7", task.PrivateData.TokenId)
	}
	if task.PrivateData.TokenName != "my-key" {
		t.Errorf("TokenName = %q, want my-key", task.PrivateData.TokenName)
	}
}
