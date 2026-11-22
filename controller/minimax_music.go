package controller

// MiniMax 官方音乐协议（POST /v1/music_generation）的兼容层。
//
// 语义冲突点：官方音乐是**同步**的（一次请求直接返回 data.audio），而我们的音乐是
// **任务制**（提交 → 轮询 → 产物 URL）。这里用"提交 + 阻塞轮询"把它伪装成同步：
//
//	1) 把官方 body 转成我们的任务契约（model/prompt/metadata.lyrics/is_instrumental）
//	2) 复用 controller.RelayTask 提交（计费、渠道选择、任务落库都走既有链路）
//	3) 在本地轮询任务直到终态，再把产物 URL 放进 {data:{audio}}
//
// 为什么返回 URL 而不是 hex：音乐动辄 5–10 MB，hex 会翻倍成十几 MB 的 JSON；
// 官方本来就支持 output_format=url，适配器也认 URL。小体积的语音才用 hex（见 minimax_t2a.go）。

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// musicPollInterval / musicPollTimeout 是同步桥的轮询节奏与上限。
//
// 官方音乐通常 1–3 分钟出片；给 10 分钟上限是为了不在慢队列上误判失败——超时后返回
// 明确的错误（任务 id 一并给出），客户端可以自行用任务接口继续查，而不是干等。
const (
	musicPollInterval = 3 * time.Second
	musicPollTimeout  = 10 * time.Minute
)

// minimaxMusicRequest 是 MiniMax 音乐请求的字段子集。
type minimaxMusicRequest struct {
	Model          string `json:"model"`
	Prompt         string `json:"prompt"`
	Lyrics         string `json:"lyrics"`
	IsInstrumental *bool  `json:"is_instrumental"`
	Stream         *bool  `json:"stream"`
	OutputFormat   string `json:"output_format"`
}

// MiniMaxMusicGeneration 处理 POST /v1/music_generation。
//
// 路由上必须已跑过 TokenAuth + Distribute。
func MiniMaxMusicGeneration(c *gin.Context) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		minimaxMusicError(c, "failed to read request body: "+err.Error())
		return
	}
	raw, err := storage.Bytes()
	if err != nil {
		minimaxMusicError(c, "failed to read request body: "+err.Error())
		return
	}
	var req minimaxMusicRequest
	if err := common.Unmarshal(raw, &req); err != nil {
		minimaxMusicError(c, "invalid params: request body is not valid JSON: "+err.Error())
		return
	}
	modelName := strings.TrimSpace(req.Model)
	if modelName == "" {
		minimaxMusicError(c, "invalid params: model is required")
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		minimaxMusicError(c, "invalid params: prompt is required")
		return
	}
	if req.Stream != nil && *req.Stream {
		minimaxMusicError(c, "invalid params: stream=true is not supported by this gateway; set stream=false")
		return
	}

	// 官方把"纯器乐"与歌词分两个字段；我们这边歌词走 metadata.lyrics（Music3 的门面
	// 还会把它映到引擎 input，ACE-Step 直接读 metadata）。
	metadata := map[string]any{}
	if lyrics := strings.TrimSpace(req.Lyrics); lyrics != "" {
		metadata["lyrics"] = lyrics
	}
	if req.IsInstrumental != nil {
		metadata["is_instrumental"] = *req.IsInstrumental
	}
	taskBody := map[string]any{"model": modelName, "prompt": prompt}
	if len(metadata) > 0 {
		taskBody["metadata"] = metadata
	}
	body, err := common.Marshal(taskBody)
	if err != nil {
		minimaxMusicError(c, "failed to build upstream request: "+err.Error())
		return
	}
	if err := common.ReplaceRequestBody(c, body); err != nil {
		minimaxMusicError(c, "failed to replace request body: "+err.Error())
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(body))
	c.Request.ContentLength = int64(len(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.URL.Path = "/v1/videos"

	// 提交：捕获既有链路写出的任务对象，从中取 task id。
	capture := &binaryCaptureWriter{ResponseWriter: c.Writer}
	c.Writer = capture
	func() {
		defer func() { c.Writer = capture.ResponseWriter }()
		RelayTask(c)
	}()
	if capture.Status() < 200 || capture.Status() >= 300 {
		capture.flush(c)
		return
	}
	taskID := extractTaskID(capture.buf.Bytes())
	if taskID == "" {
		minimaxMusicError(c, "上游没有返回任务 id")
		return
	}

	// 阻塞轮询到终态。任务已落库、计费已在提交时完成，这里只等产物。
	userID := c.GetInt("id")
	deadline := time.Now().Add(musicPollTimeout)
	for time.Now().Before(deadline) {
		task, exist, err := model.GetByTaskId(userID, taskID)
		if err != nil {
			minimaxMusicError(c, "读取任务失败: "+err.Error())
			return
		}
		if exist {
			switch task.Status {
			case model.TaskStatusSuccess:
				url := task.GetResultURL()
				if strings.TrimSpace(url) == "" {
					minimaxMusicError(c, "任务成功但没有产物 URL（任务 "+taskID+"）")
					return
				}
				c.JSON(http.StatusOK, gin.H{
					"data":      gin.H{"audio": url, "status": 0},
					"base_resp": gin.H{"status_code": 0, "status_msg": "success"},
				})
				return
			case model.TaskStatusFailure:
				message := task.FailReason
				if strings.TrimSpace(message) == "" {
					message = "音乐生成失败"
				}
				minimaxMusicError(c, message)
				return
			}
		}
		time.Sleep(musicPollInterval)
	}
	minimaxMusicError(c, "音乐生成超时（任务 "+taskID+" 仍可继续查询）")
}

// extractTaskID 从任务提交响应里取 id（我们的任务对象用 id 字段）。
func extractTaskID(raw []byte) string {
	var payload struct {
		ID     string `json:"id"`
		TaskID string `json:"task_id"`
	}
	if err := common.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	if strings.TrimSpace(payload.ID) != "" {
		return strings.TrimSpace(payload.ID)
	}
	return strings.TrimSpace(payload.TaskID)
}

// minimaxMusicError 用 MiniMax 错误信封回错。
func minimaxMusicError(c *gin.Context, message string) {
	c.JSON(http.StatusBadRequest, gin.H{
		"base_resp": gin.H{"status_code": 1004, "status_msg": message},
	})
	c.Abort()
}
