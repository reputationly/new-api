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
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/mediastore"

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
	// Metadata 官方协议里的扩展位。早先没有它:展开阶段写进去的东西(尤其是增强后的
	// 描述)会被 buildMusicTaskBody 重建 body 时丢掉 —— 记录显示已增强,引擎收到的
	// 是原文,且没有任何报错。
	Metadata map[string]any `json:"metadata"`
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

	if format := strings.ToLower(strings.TrimSpace(req.OutputFormat)); format != "" && format != "url" {
		minimaxMusicError(c, "invalid params: output_format="+req.OutputFormat+
			" is not supported by this gateway (only output_format=url)")
		return
	}

	taskBody, message := buildMusicTaskBody(modelName, prompt, req.Lyrics, req.IsInstrumental, req.Metadata)
	if message != "" {
		minimaxMusicError(c, message)
		return
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
				// 自建链路的 ResultURL 存的是 obs:// 内部占位符，必须实时签名后再给出去；
				// 直接回 GetResultURL() 会让客户端拿到一个不可用的 URI（全仓其它出口都过
				// ResolveResultURL，这里曾漏了）。
				url := mediastore.ResolveResultURL(c.Request.Context(), task.GetResultURL())
				if strings.TrimSpace(url) == "" {
					minimaxMusicError(c, "任务成功但没有产物 URL（任务 "+taskID+"）")
					return
				}
				resetResponseHeaders(c)
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
		select {
		case <-c.Request.Context().Done():
			// 客户端断开就别再空转到 10 分钟：任务已在库里，他可以随时用任务接口查。
			c.AbortWithStatus(http.StatusRequestTimeout)
			return
		case <-time.After(musicPollInterval):
		}
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

// buildMusicTaskBody 把 MiniMax 音乐请求映射成我们的任务契约。
//
// **两个模型族的字段语义是反的**，这是这个端点最容易写错的地方：
//
//	官方的 prompt   = 风格描述   lyrics = 歌词
//	ACE-Step(t2m)   : 顶层 prompt = 描述、metadata.lyrics = 歌词      → 直接对应
//	Music3          : 顶层 prompt = **歌词**（门面映射到引擎 input）、
//	                  风格描述走 metadata.instructions；
//	                  metadata.lyrics 引擎不认，到了就丢
//
// 无条件按 ACE-Step 那套映射会让 Music3 把风格描述当歌词唱、真歌词被丢弃，
// 而且不报错 —— 静默产出错误的歌。
func buildMusicTaskBody(modelName, prompt, lyrics string, instrumental *bool, extra map[string]any) (map[string]any, string) {
	prompt = strings.TrimSpace(prompt)
	lyrics = strings.TrimSpace(lyrics)
	if prompt == "" {
		return nil, "invalid params: prompt is required"
	}
	// 聚合名（minimax-music3-enhanced 等）要先解析到生成段模型，再判模型族。
	generateModel := modelName
	if agg := common.GetAggregateModel(modelName); agg != nil && strings.TrimSpace(agg.Generate.Model) != "" {
		generateModel = strings.TrimSpace(agg.Generate.Model)
	}

	body := map[string]any{"model": modelName}
	// 先透传客户端 metadata(官方协议的扩展位:task_type / sample_query / reference_audio 等),
	// 再写下面三个由 prompt/lyrics 映射而来的键 —— 后者必须赢,否则会出现两套说法。
	metadata := map[string]any{}
	for k, v := range extra {
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "instructions", "lyrics", "is_instrumental":
			continue
		}
		metadata[k] = v
	}
	if service.IsMusic3GeneratingModel(generateModel) {
		// 我们的链路不会替客户写词（官方 lyrics_optimizer 那种），所以无词时直接说清楚，
		// 而不是把风格描述当歌词送进引擎。
		//
		// **纯器乐在这套部署上不可用**,is_instrumental=true 也不是出路:这个字段引擎不读
		// (带着它 + 歌词仍会出人声),而无歌词时长度由段落结构决定 —— 只给少量段落标签时只有
		// 数秒到二三十秒(实测 9.8s / 22.7s 都是"只有段落标签、没有唱词"的产物),不是可交付的曲子。与其给客户一段残片,
		// 不如明确拒绝。
		// 口径与展开阶段一致:is_instrumental=true 一律拒。
		unusableInstrumental := instrumental != nil && *instrumental
		if lyrics == "" || unusableInstrumental {
			return nil, "invalid params: MiniMax-Music3 does not support instrumental generation on this gateway; " +
				"lyrics are required (this gateway does not write lyrics for you)"
		}
		body["prompt"] = lyrics
		metadata["instructions"] = prompt
	} else {
		body["prompt"] = prompt
		if lyrics != "" {
			metadata["lyrics"] = lyrics
		}
	}
	if instrumental != nil {
		metadata["is_instrumental"] = *instrumental
	}
	if len(metadata) > 0 {
		body["metadata"] = metadata
	}
	return body, ""
}
