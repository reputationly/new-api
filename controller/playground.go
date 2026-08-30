package controller

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func Playground(c *gin.Context) {
	playgroundRelay(c, types.RelayFormatOpenAI)
}

// PlaygroundImage relays an image generation request on behalf of the logged-in
// user (session auth), mirroring Playground but targeting the OpenAI image format.
func PlaygroundImage(c *gin.Context) {
	// 异步图片走任务子系统，与 /v1 的 imageEntry 同一条分流。
	// 上下文要按 RelayFormatTask 建：那条路径读的是 TaskRelayInfo，
	// 按 OpenAIImage 建会让 PublicTaskID 等字段缺席。
	if c.GetBool(middleware.CtxKeyImageAsync) {
		if apiErr := playgroundSetupContext(c, types.RelayFormatTask); apiErr != nil {
			c.JSON(apiErr.StatusCode, gin.H{"error": apiErr.ToOpenAIError()})
			return
		}
		RelayTask(c)
		return
	}
	playgroundRelay(c, types.RelayFormatOpenAIImage)
}

// PlaygroundResponses relays an OpenAI Responses request (canvas assistant,
// streaming) on behalf of the logged-in user (session auth).
func PlaygroundResponses(c *gin.Context) {
	playgroundRelay(c, types.RelayFormatOpenAIResponses)
}

// PlaygroundAudioSpeech relays a TTS request on behalf of the logged-in user (session auth).
func PlaygroundAudioSpeech(c *gin.Context) {
	playgroundRelay(c, types.RelayFormatOpenAIAudio)
}

func playgroundRelay(c *gin.Context, relayFormat types.RelayFormat) {
	if apiErr := playgroundSetupContext(c, relayFormat); apiErr != nil {
		c.JSON(apiErr.StatusCode, gin.H{"error": apiErr.ToOpenAIError()})
		return
	}
	Relay(c, relayFormat)
}

// maxUserConcurrentVideoTasks 单个用户在体验区同时能有几个视频任务在跑。
//
// 只卡体验区这一个入口（/pg/videos），**不卡 /v1 的 API 用户**：那边是按 key 计费的
// 集成方，批量提交是正常用法，拦下来等于把人家的流水线掐断。
var maxUserConcurrentVideoTasks = common.GetEnvOrDefault("PLAYGROUND_MAX_CONCURRENT_VIDEO_TASKS", 3)

// PlaygroundVideo submits a video generation task on behalf of the logged-in
// user (session auth), mirroring Playground but delegating to the async task relay.
func PlaygroundVideo(c *gin.Context) {
	// 并发闸要在 playgroundSetupContext 之前：那一步会签临时 token、建 relay 上下文，
	// 注定要被拒的请求没必要走这些。查库失败时放行——一次 DB 抖动不该让人发不出任务，
	// 上限是产品护栏不是安全边界，宁可漏放一个。
	if maxUserConcurrentVideoTasks > 0 {
		if n, err := model.CountUserUnfinishedVideoTasks(c.GetInt("id")); err == nil &&
			n >= int64(maxUserConcurrentVideoTasks) {
			c.JSON(http.StatusTooManyRequests, gin.H{"error": gin.H{
				"message": fmt.Sprintf("最多同时进行 %d 个视频任务，请等其中一个完成后再发", maxUserConcurrentVideoTasks),
				"type":    "too_many_concurrent_tasks",
			}})
			return
		}
	}
	if apiErr := playgroundSetupContext(c, types.RelayFormatTask); apiErr != nil {
		c.JSON(apiErr.StatusCode, gin.H{"error": apiErr.ToOpenAIError()})
		return
	}
	RelayTask(c)
}

// PlaygroundVideoFetch polls a video generation task for the logged-in user.
func PlaygroundVideoFetch(c *gin.Context) {
	if apiErr := playgroundSetupContext(c, types.RelayFormatTask); apiErr != nil {
		c.JSON(apiErr.StatusCode, gin.H{"error": apiErr.ToOpenAIError()})
		return
	}
	RelayTaskFetch(c)
}

// PlaygroundImageFetch 查询体验区提交的异步图片任务（session 鉴权）。
// 与 /v1/images/generations/{id} 走同一套响应构建，只是鉴权方式不同。
func PlaygroundImageFetch(c *gin.Context) {
	if apiErr := playgroundSetupContext(c, types.RelayFormatTask); apiErr != nil {
		c.JSON(apiErr.StatusCode, gin.H{"error": apiErr.ToOpenAIError()})
		return
	}
	RelayTaskFetch(c)
}

// PlaygroundImageCancel 取消体验区提交的异步图片任务（session 鉴权）。
func PlaygroundImageCancel(c *gin.Context) {
	if apiErr := playgroundSetupContext(c, types.RelayFormatTask); apiErr != nil {
		c.JSON(apiErr.StatusCode, gin.H{"error": apiErr.ToOpenAIError()})
		return
	}
	RelayTaskCancel(c)
}

// playgroundSetupContext 为登录用户签发临时 token 并写入用户上下文，供后续 relay 使用。
func playgroundSetupContext(c *gin.Context, relayFormat types.RelayFormat) *types.NewAPIError {
	if c.GetBool("use_access_token") {
		return types.NewError(errors.New("暂不支持使用 access token"), types.ErrorCodeAccessDenied, types.ErrOptionWithSkipRetry())
	}

	relayInfo, err := relaycommon.GenRelayInfo(c, relayFormat, nil, nil)
	if err != nil {
		return types.NewError(err, types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	userId := c.GetInt("id")

	// Write user context to ensure acceptUnsetRatio is available
	userCache, err := model.GetUserCache(userId)
	if err != nil {
		return types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
	}
	userCache.WriteContext(c)

	tempToken := &model.Token{
		UserId: userId,
		Name:   fmt.Sprintf("playground-%s", relayInfo.UsingGroup),
		Group:  relayInfo.UsingGroup,
	}
	_ = middleware.SetupContextForToken(c, tempToken)
	return nil
}
