package arkv3

// 方舟图片协议（POST /api/v3/images/generations，Seedream 语义）的兼容层。
//
// 与视频那条（/api/v3/contents/generations/tasks）最大的差别：**它是同步的** ——
// 没有任务 id、没有轮询，产物直接在响应的 data[].url 里。所以这里只做请求转换，
// 成功响应原样透出（我们的同步生图响应本来就是 {data:[{url}], created, usage}，
// 方舟的 data[].url 就在同一层，客户端按 url 取件）。
//
// 转发目标是我们自己的 /v1/images/generations（OpenAI 形状）：MiniMax Design 的
// 内置 volcengine-ark 适配器就是按这组字段发请求的，改 baseUrl 到我们这里即可。

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// ImageCreateRequest 是方舟图片生成请求的字段子集。
//
// 只列我们真的会转发的字段：方舟文档里还有一批（optimize_prompt_options、
// sequential_image_generation 的组合选项等）我们做不到，见 ConvertImageRequest 的拒绝分支。
type ImageCreateRequest struct {
	Model          string          `json:"model"`
	Prompt         string          `json:"prompt"`
	Image          json.RawMessage `json:"image,omitempty"`
	Size           string          `json:"size,omitempty"`
	ResponseFormat string          `json:"response_format,omitempty"`
	Watermark      *bool           `json:"watermark,omitempty"`
	// Sequential 是方舟的"一次出多张"组图能力；我们一次一张，显式拒绝而不是忽略 ——
	// 忽略会让客户端以为拿到了整组，实际只有一张，而且不报错。
	Sequential     string          `json:"sequential_image_generation,omitempty"`
	SequentialOpts json.RawMessage `json:"sequential_image_generation_options,omitempty"`
}

// maxArkImageRefs 方舟图片参考图上限（Seedream 官方 10 张）。
const maxArkImageRefs = 10

// ConvertImageRequest 把方舟图片请求转成我们 /v1/images/generations 的 body。
//
// 明确拒绝（而不是静默丢弃）的项，理由都是"我们做不到，假装支持会让客户端拿到错的产物"：
//   - watermark=true：我们没有水印能力，忽略它等于承诺不加水印却可能加（或反之）；
//   - sequential_image_generation=enabled：一次一张，组图不透传；
//   - response_format 非 url / b64_json：方舟只支持这两种，其余是拼写错误。
func ConvertImageRequest(raw []byte) (map[string]any, *APIError) {
	var req ImageCreateRequest
	if err := common.Unmarshal(raw, &req); err != nil {
		return nil, NewBadRequest("invalid params: request body is not valid JSON: " + err.Error())
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		return nil, NewBadRequest("invalid params: model is required")
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return nil, NewBadRequest("invalid params: prompt is required")
	}
	if req.Watermark != nil && *req.Watermark {
		return nil, NewBadRequest("invalid params: watermark=true is not supported by this gateway")
	}
	if strings.EqualFold(strings.TrimSpace(req.Sequential), "enabled") {
		return nil, NewBadRequest("invalid params: sequential_image_generation=enabled is not supported by this gateway (one image per request)")
	}

	responseFormat := strings.ToLower(strings.TrimSpace(req.ResponseFormat))
	switch responseFormat {
	case "":
		// 方舟缺省就是 url；显式写死，免得下游按 OpenAI 的缺省（b64_json）走。
		responseFormat = "url"
	case "url", "b64_json":
	default:
		return nil, NewBadRequest(fmt.Sprintf(
			"invalid params: response_format=%q is not supported (expected url or b64_json)", req.ResponseFormat))
	}

	body := map[string]any{
		"model":           model,
		"prompt":          prompt,
		"response_format": responseFormat,
	}
	if size := strings.TrimSpace(req.Size); size != "" {
		// 尺寸原样透传：方舟给的是像素串（如 2048x2048）或档位词，两种我们自己的
		// resolve_size 都认；在这里做映射反而会和引擎的真实档位漂移。
		body["size"] = size
	}

	image, apiErr := arkImageReferences(req.Image)
	if apiErr != nil {
		return nil, apiErr
	}
	if image != nil {
		// 我们自己的形状是"一张图走 image、多张走 images"（既有适配器两种都认）。
		// 单张仍按方舟的 image 字段下发，多张才升级成 images，避免改变单图请求的语义。
		if len(image) == 1 {
			body["image"] = image[0]
		} else {
			body["images"] = image
		}
	}
	return body, nil
}

// arkImageReferences 解析方舟的 image 字段：既接受单个字符串，也接受字符串数组。
func arkImageReferences(raw json.RawMessage) ([]string, *APIError) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var single string
	if err := common.Unmarshal(raw, &single); err == nil {
		single = strings.TrimSpace(single)
		if single == "" {
			return nil, nil
		}
		return []string{single}, nil
	}
	var multi []string
	if err := common.Unmarshal(raw, &multi); err != nil {
		return nil, NewBadRequest("invalid params: image must be a string or an array of strings")
	}
	out := make([]string, 0, len(multi))
	for _, item := range multi {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	if len(out) > maxArkImageRefs {
		return nil, NewBadRequest(fmt.Sprintf(
			"invalid params: at most %d reference images are allowed, got %d", maxArkImageRefs, len(out)))
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}
