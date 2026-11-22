package middleware

// 续写(video-extension)的提交侧接线。
//
// 客户端只声明"我要续写"（metadata.image_mode=video-extension）+ 源视频 + 最终总时长；
// 这一段把它换成一次普通的 i2v 提交：抽源片末帧当首帧、duration 改成**生成段**秒数，
// 并把三个数（源时长 / 生成秒数 / 总时长）冻进 context，等任务落库时随任务保存。
//
// 为什么放在路由级、Distribute 之后：
//   - 要在 TokenAuth 之后(task: 引用需要用户归属校验)；
//   - 要在 Distribute 的聚合展开之后（展开可能改 size，但不改 duration/image，互不干扰）；
//   - 必须在 BuildRequestBody 之前 —— 那里才真正把 body 发给门面。
//
// 失败一律 4xx 且不落任务：这一步的错（源片读不出、时长不够、分辨率太小）都是客户端
// 可修的，绝不能先建任务、烧了 GPU 再告诉他。

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/continuation"

	"github.com/gin-gonic/gin"
)

// prepareContinuation 抽出成变量供测试注入：真实实现要下载源片并跑 ffmpeg，
// 而本中间件要测的是"识别 → 改写 → 快照 → 错误映射"这条链。
var prepareContinuation = continuation.PrepareContinuation

// continuationPrepareTimeout 一次预处理的预算（下载 + ffprobe + 抽帧）。
// 源片上限 50 MB、抽帧只解尾部窗口，正常几秒；3 分钟是给慢网/大文件的兜底。
const continuationPrepareTimeout = 3 * time.Minute

// ContinuationConvert 识别续写请求并改写成 i2v 提交。
func ContinuationConvert() gin.HandlerFunc {
	return func(c *gin.Context) {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			c.Next() // 读不出 body 交给下游按原样报错
			return
		}
		raw, err := storage.Bytes()
		if err != nil {
			c.Next()
			return
		}
		// 只处理 JSON：multipart 的视频提交走不了这条路（源片是另一个字段）。
		var body map[string]any
		if err := common.UnmarshalWithNumber(raw, &body); err != nil || body == nil {
			c.Next()
			return
		}
		meta, metaIsString := continuationMetadata(body)
		if meta == nil {
			c.Next()
			return
		}
		// 大小写都收：客户端把 image_mode 写成 "Video-Extension" 时不能漏过去 ——
		// 漏了会一路走到适配器吃到"请走续写入口"的错，而调用方其实已经给了 source_video。
		mode := strings.ToLower(strings.TrimSpace(jsonString(meta["image_mode"])))
		taskType := strings.ToLower(strings.TrimSpace(jsonString(meta["task_type"])))
		if mode != "video-extension" && taskType != "continuation" {
			c.Next()
			return
		}

		source := strings.TrimSpace(jsonString(meta["source_video"]))
		if source == "" {
			abortContinuation(c, "source_missing", "续写需要 source_video(源视频 URL 或 task:<id>)")
			return
		}
		total := continuationTotalSeconds(body)

		ctx, cancel := context.WithTimeout(c.Request.Context(), continuationPrepareTimeout)
		defer cancel()
		result, perr := prepareContinuation(ctx, continuation.ContinuationPrepareOptions{
			Source:       source,
			UserID:       c.GetInt("id"),
			TotalSeconds: total,
		}, nil)
		if perr != nil {
			code := "local_media_failed"
			if cerr, ok := perr.(*continuation.ContinuationError); ok && cerr.Code != "" {
				code = cerr.Code
			}
			abortContinuation(c, code, perr.Error())
			return
		}

		// 改写成一次普通 i2v：首帧 = 源片末帧，时长 = 生成段秒数。
		body["image"] = result.FirstFrameDataURL
		delete(body, "images")
		delete(body, "input_reference")
		body["duration"] = result.GenerateSeconds
		delete(body, "seconds") // 与 duration 双写会让下游二选一,统一走 duration
		meta["task_type"] = "i2v"
		// image_mode / source_video 已被消费：留给下游只会在 taskTypesCompatibleWithInputs
		// 里和真实输入形态打架（它还带着 video-extension 这个词）。
		delete(meta, "image_mode")
		delete(meta, "source_video")
		body["metadata"] = metadataForRewrite(meta, metaIsString)

		if err := common.ReplaceRequestBody(c, mustMarshalJSON(body)); err != nil {
			abortContinuation(c, "local_media_failed", "改写请求体失败: "+err.Error())
			return
		}
		if c.Request != nil {
			payload := mustMarshalJSON(body)
			c.Request.Body = io.NopCloser(bytes.NewReader(payload))
			c.Request.ContentLength = int64(len(payload))
			c.Request.Header.Set("Content-Type", "application/json")
		}

		// 任务 action 与快照：action 让列表/日志能筛出续写；快照让几百秒后的轮询还能回显。
		c.Set("action", constant.TaskActionContinuation)
		common.SetContextKey(c, constant.ContextKeyContinuationSnapshot, &model.ContinuationProperties{
			SourceRef:       truncateSourceRef(source),
			SourceSeconds:   result.Probe.DurationSeconds,
			GenerateSeconds: result.GenerateSeconds,
			TotalSeconds:    total,
			FirstFrameBytes: len(result.FirstFrameDataURL),
		})
		common.SysLog("[continuation] 源片 " + strconv.FormatFloat(result.Probe.DurationSeconds, 'f', 2, 64) +
			"s → 生成段 " + strconv.Itoa(result.GenerateSeconds) +
			"s（总时长 " + strconv.Itoa(total) + "s）")

		c.Next()
	}
}

// GetContinuationSnapshot 取本次请求的续写快照；非续写返回 nil。
func GetContinuationSnapshot(c *gin.Context) *model.ContinuationProperties {
	v, ok := common.GetContextKey(c, constant.ContextKeyContinuationSnapshot)
	if !ok {
		return nil
	}
	snap, _ := v.(*model.ContinuationProperties)
	return snap
}

// continuationMetadata 取 metadata 的 map 形态（对象或 JSON 字符串两种都收），
// 并告诉调用方它原来是字符串形态，改写时要按原形态写回。
func continuationMetadata(body map[string]any) (map[string]any, bool) {
	raw, ok := body["metadata"]
	if !ok || raw == nil {
		return nil, false
	}
	switch v := raw.(type) {
	case map[string]any:
		return v, false
	case string:
		if strings.TrimSpace(v) == "" {
			return nil, false
		}
		var meta map[string]any
		if err := common.UnmarshalWithNumber([]byte(v), &meta); err != nil || meta == nil {
			return nil, false
		}
		return meta, true
	}
	return nil, false
}

func metadataForRewrite(meta map[string]any, asString bool) any {
	if !asString {
		return meta
	}
	// 原样保持字符串形态：有些客户端按字符串解析 metadata，形态一变就报错。
	return string(mustMarshalJSON(meta))
}

// continuationTotalSeconds 读客户要的最终总时长（顶层 duration，兼容 seconds 字符串）。
func continuationTotalSeconds(body map[string]any) int {
	if v, ok := body["duration"]; ok {
		if n, ok := jsonInt(v); ok {
			return n
		}
	}
	if v, ok := body["seconds"]; ok {
		// 数字形态的 seconds 也要认（jsonIntValue 覆盖 string / float64 / json.Number）。
		if n, ok := jsonIntValue(v); ok {
			return n
		}
	}
	return 0
}

func jsonString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func jsonInt(v any) (int, bool) {
	switch n := v.(type) {
	// 请求体是 UnmarshalWithNumber 解的：整数在 duration 字段上是 json.Number，
	// 不认它会静默拿到 0，续写就会报"缺 duration"（实测踩过）。
	case json.Number:
		i, err := n.Int64()
		if err != nil {
			f, ferr := n.Float64()
			if ferr != nil {
				return 0, false
			}
			return int(f), true
		}
		return int(i), true
	case float64:
		return int(n), true
	case int:
		return n, true
	case int64:
		return int(n), true
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(n, "s")))
		if err != nil {
			return 0, false
		}
		return i, true
	}
	return 0, false
}

func mustMarshalJSON(v any) []byte {
	data, err := common.Marshal(v)
	if err != nil {
		return []byte("{}")
	}
	return data
}

// truncateSourceRef 源片引用可能是一整段 data URI 或超长签名 URL，落库只留可辨认的开头。
func truncateSourceRef(raw string) string {
	const max = 200
	if len(raw) <= max {
		return raw
	}
	return raw[:max] + "…"
}

func abortContinuation(c *gin.Context, code, message string) {
	c.JSON(http.StatusBadRequest, dto.TaskError{
		Code:       code,
		Message:    message,
		StatusCode: http.StatusBadRequest,
	})
	c.Abort()
}
