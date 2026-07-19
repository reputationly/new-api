package relay

// task:<task_id> 产物引用的第三方渠道兼容层(docs/canvas-orchestration-design.md §3.8)。
//
// gpustackplus 在输入物化层(nfsinput.AddString)原生解析 task: 引用并享受 NFS 同盘
// 直读;第三方渠道适配器只认 base64/URL,task: 字符串会被当普通值透传给上游导致报错。
// 因此在渠道确定之后、构建请求体之前,非 gpustackplus 渠道统一把请求中的 task: 引用
// 展开为 base64 data-url(体验区第三方链路本就以 base64 传媒体,适配器无需感知)。
//
// 重试语义天然安全:每次尝试都由 ValidateRequestAndSetAction 从原始 body 重建
// task_request——gpustackplus 尝试保留 task: 原样,切到第三方渠道的下一次尝试重新展开。

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/relay/channel/gpustackplus/nfsinput"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/mediastore"

	"github.com/gin-gonic/gin"
)

// expandTaskRefsForChannel 非 gpustackplus 渠道把 task_request 里的 task: 引用展开为
// data-url 并回写 context。解析失败按 400 skip-retry(引用坏了换渠道也没用)。
func expandTaskRefsForChannel(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	if info.ChannelType == constant.ChannelTypeGPUStackPlus {
		return nil
	}
	// memo:同一 task: 引用在 task_request 与原始 body 中可能各出现一次,缓存解析结果
	// 避免重复查库 + 重复下载。
	memo := map[string]string{}
	resolve := func(value string) (string, error) {
		if !nfsinput.IsTaskRef(value) {
			return value, nil
		}
		if cached, ok := memo[value]; ok {
			return cached, nil
		}
		data, ext, rErr := nfsinput.ResolveTaskRefBytes(c.Request.Context(), info.UserId, value, 0)
		if rErr != nil {
			return "", rErr
		}
		dataURL := "data:" + mediastore.InferContentType("ref"+ext) + ";base64," + base64.StdEncoding.EncodeToString(data)
		memo[value] = dataURL
		return dataURL, nil
	}

	// 1) 改写 task_request(供从 c.Get("task_request") 重建请求的适配器:doubao/jimeng/kling/vidu 等)。
	if req, err := relaycommon.GetTaskRequest(c); err == nil {
		fields := []*string{&req.Image, &req.InputReference}
		for _, field := range fields {
			next, rErr := resolve(*field)
			if rErr != nil {
				return taskRefExpandError(rErr)
			}
			*field = next
		}
		for i, image := range req.Images {
			next, rErr := resolve(image)
			if rErr != nil {
				return taskRefExpandError(rErr)
			}
			req.Images[i] = next
		}
		// metadata 可含任意深度的嵌套结构(如 doubao 的 content[].video_url.url),
		// 递归遍历所有 string 叶子,不止数组里的直接 string。
		if _, rErr := expandTaskRefsInValue(req.Metadata, resolve); rErr != nil {
			return taskRefExpandError(rErr)
		}
		c.Set("task_request", req)
	}

	// 2) 改写缓存的原始 JSON body(供从 common.GetBodyStorage 重建请求的适配器:Sora/OpenAI 视频)。
	// 在原始 JSON map 上定点替换,保留 task_request 未建模的全部字段;multipart 上传不含 task:,跳过。
	if taskErr := expandTaskRefsInBody(c, resolve); taskErr != nil {
		return taskErr
	}
	return nil
}

// expandTaskRefsInBody 读取缓存的原始 JSON body,递归展开其中的 task: 引用后写回 body 存储。
func expandTaskRefsInBody(c *gin.Context, resolve func(string) (string, error)) *dto.TaskError {
	if !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
		return nil
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil // body 不可读(如已被消费),task_request 改写已兜底
	}
	raw, err := storage.Bytes()
	if err != nil || len(raw) == 0 {
		return nil
	}
	if !strings.Contains(string(raw), nfsinput.TaskRefScheme) {
		return nil // 无 task: 引用,免解析
	}
	var bodyMap map[string]any
	// UseNumber:数字保留为 json.Number 原始文本,避免大整数(seed/provider id)经
	// float64 round-trip 丢精度;json.Number 是命名类型,不匹配 expandTaskRefsInValue
	// 的 case string,只有真正的 string 叶子被展开。
	if err := common.UnmarshalWithNumber(raw, &bodyMap); err != nil {
		return nil // 非对象 JSON,task_request 路径已覆盖标量场景
	}
	// 只改写已知媒体/输入字段(与 task_request 路径 Image/Images/InputReference/Metadata
	// 一致),不扫 prompt/model 等自由文本——否则正常 prompt 以 "task:" 开头会被误判为
	// 引用而查库失败(400)。metadata 内可含嵌套媒体(doubao content[].video_url.url),深入递归。
	changed := false
	wrap := func(value string) (string, error) {
		next, err := resolve(value)
		if err == nil && next != value {
			changed = true
		}
		return next, err
	}
	for _, key := range []string{"image", "images", "input_reference", "metadata"} {
		v, ok := bodyMap[key]
		if !ok {
			continue
		}
		next, rErr := expandTaskRefsInValue(v, wrap)
		if rErr != nil {
			return taskRefExpandError(rErr)
		}
		bodyMap[key] = next
	}
	if !changed {
		return nil // 白名单字段无 task: 引用,不动 body(如 task: 只出现在 prompt 文本里)
	}
	newBody, err := common.Marshal(bodyMap)
	if err != nil {
		return taskRefExpandError(err)
	}
	if err := common.ReplaceRequestBody(c, newBody); err != nil {
		return taskRefExpandError(err)
	}
	return nil
}

// expandTaskRefsInValue 递归展开任意 JSON 值(string/map/slice)中的 task: 引用,原地
// 改写 map value / slice 元素。JSON 反序列化产生的是 map[string]any 与 []any,同时兼容
// 手工构造的 []string / map[string]string。返回值仅用于把 string 叶子替换回上层容器。
func expandTaskRefsInValue(value any, resolve func(string) (string, error)) (any, error) {
	switch v := value.(type) {
	case string:
		return resolve(v)
	case map[string]any:
		for key, item := range v {
			next, err := expandTaskRefsInValue(item, resolve)
			if err != nil {
				return nil, err
			}
			v[key] = next
		}
		return v, nil
	case []any:
		for i, item := range v {
			next, err := expandTaskRefsInValue(item, resolve)
			if err != nil {
				return nil, err
			}
			v[i] = next
		}
		return v, nil
	case map[string]string:
		for key, item := range v {
			next, err := resolve(item)
			if err != nil {
				return nil, err
			}
			v[key] = next
		}
		return v, nil
	case []string:
		for i, item := range v {
			next, err := resolve(item)
			if err != nil {
				return nil, err
			}
			v[i] = next
		}
		return v, nil
	default:
		return value, nil
	}
}

func taskRefExpandError(err error) *dto.TaskError {
	taskErr := service.TaskErrorWrapperLocal(fmt.Errorf("任务产物引用解析失败: %w", err), "task_ref_expand_failed", http.StatusBadRequest)
	return taskErr
}
