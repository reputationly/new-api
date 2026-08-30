package middleware

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"

	"github.com/gin-gonic/gin"
)

// 异步图片生成的入口中间件（设计见 docs/image-async-task-design.md §5.1）。
//
// 职责只有一件：识别 async 开关，把 OpenAI 图片请求体改写成任务子系统认识的
// 统一契约（relaycommon.TaskSubmitReq 形状），随后交给 controller.RelayTask。
// 不做渠道能力判定 —— 那要等 Distribute 选完渠道才知道 channel_type，见 relay/image_async.go。
//
// 同步请求（不带开关）在这里原样放行，一个字节都不动。

const (
	// AsyncHeader 副开关。存在的理由是 body 开关在两种场景下不可用：
	//   - 客户端改不了 body（网关/SDK 透传）；
	//   - PassThroughRequestEnabled 打开时 body 被原样转发给上游，
	//     多带一个 async 字段会被上游当作未知参数拒绝。
	AsyncHeader = "X-New-Api-Async"
	// asyncBodyField 主开关。不用 background —— OpenAI Images API 已把它占用为
	// 图片透明背景参数（transparent/opaque/auto），见 dto.ImageRequest.Background。
	asyncBodyField = "async"

	// CtxKeyImageAsync 标记本请求走异步图片链路，供路由入口分流。
	CtxKeyImageAsync = "image_async"
)

// ImageAsyncConvert 识别 async 开关并改写请求体。必须挂在 Distribute 之前
// （gin 的分组中间件先于路由中间件执行，所以图片路由需要独立分组，见 router/relay-router.go）。
func ImageAsyncConvert() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodPost {
			c.Next()
			return
		}
		isMultipart := strings.Contains(c.GetHeader("Content-Type"), gin.MIMEMultipartPOSTForm)

		// multipart 的开关只认 header，**不解析 body 去找 async 字段**。
		//
		// 这不是偷懒，是为了不碰同步请求的 body：OpenAI 的 /v1/images/edits 按规范就是
		// multipart，而解析它的唯一办法是把整个 body 读出来（几 MB 的图会落一次临时盘）。
		// 为了一个罕见用法让**所有**同步 edits 都多付这个代价不划算；更要命的是，
		// 一旦用 c.Request.ParseMultipartForm 去读，body 就被消费了，后续
		// UnmarshalBodyReusable 只能拿到 EOF，同步 edits 直接 400「model name required」。
		// 已用测试固定这两点：TestImageAsyncConvertLeavesSyncMultipartIntact。
		if isMultipart && !asyncHeaderSet(c) {
			c.Next()
			return
		}

		var raw map[string]any
		var form *multipart.Form
		var err error
		if isMultipart {
			// 走到这里说明 header 已明确要求异步。从 body storage 的字节解析，
			// 不碰 c.Request.Body —— 与 common.parseMultipartFormData 同一手法。
			form, err = parseMultipartFromStorage(c)
			if form != nil {
				defer form.RemoveAll()
			}
			if err == nil {
				raw = formValuesToMap(form)
			}
		} else {
			raw, err = imageJSONToMap(c)
		}
		// 解析失败一律放行给同步链路：那边有完整的请求校验与错误信息，
		// 在这里抢着报错只会把「body 格式不对」说成「异步转换失败」。
		if err != nil || raw == nil {
			c.Next()
			return
		}
		if !asyncRequested(c, raw) {
			c.Next()
			return
		}

		taskType, action := "t2i", constant.TaskActionImageGenerate
		if strings.HasSuffix(c.Request.URL.Path, "/edits") {
			taskType, action = "i2i", constant.TaskActionImageEdit
		}
		// 显式指定 task_type，不依赖适配器的 inferTaskType（那条按模型名子串推断的
		// 兜底路径在「模型名里既没有 edit 也没有 t2i」时会推错）。
		raw["task_type"] = taskType
		// async 是本网关的控制字段，不能随 metadata 透传到门面/引擎。
		delete(raw, asyncBodyField)

		images, err := imageInputsFrom(raw, form)
		if err != nil {
			abortImageAsync(c, http.StatusBadRequest, err.Error())
			return
		}

		// group 必须留在顶层，且必须从 metadata 里剥掉。
		//
		// 留顶层：体验区的分组选择由 Distribute 从请求体读（见 distributor.go 的
		// 「统一让请求体里的 group 生效，否则图片生成会忽略用户选择的分组」）。
		// 改写后顶层没有 group，Distribute 解出空值就回落默认分组 —— 同一个模型，
		// 同步走对分组、异步走默认分组，是纯回归。
		//
		// 从 metadata 剥掉：metadata 会被适配器整体透传给门面再转交引擎，而 group
		// 是网关自己的路由概念，引擎不认。同步路径下它落在 dto.ImageRequest.Extra 里，
		// 而 Extra 的 MarshalJSON 不外泄，上游从来收不到它 —— 异步不该比同步多发字段。
		group := stringField(raw, "group")
		delete(raw, "group")

		unified := map[string]any{
			"model":    stringField(raw, "model"),
			"prompt":   stringField(raw, "prompt"),
			"size":     stringField(raw, "size"),
			"metadata": raw,
		}
		if group != "" {
			unified["group"] = group
		}
		if len(images) > 0 {
			unified["images"] = images
		}
		jsonData, err := common.Marshal(unified)
		if err != nil {
			abortImageAsync(c, http.StatusBadRequest, "failed to build async image request: "+err.Error())
			return
		}

		// 必须走 ReplaceRequestBody 而不是只赋值 c.Request.Body：下游的
		// common.UnmarshalBodyReusable 是从 body storage 读的，不看 c.Request.Body。
		// （已用 mutation 验证过：改成只赋值 c.Request.Body 时，下游拿到的是原始
		//  body，转换等于没发生。middleware/kling_adapter.go 至今是那种老写法。）
		if err := common.ReplaceRequestBody(c, jsonData); err != nil {
			abortImageAsync(c, http.StatusInternalServerError, "failed to replace request body: "+err.Error())
			return
		}
		// body storage 按 Content-Type 分流解析，multipart 转 JSON 后必须同步改头，
		// 否则 UnmarshalBodyReusable 会拿 multipart 分支去解一段 JSON。
		c.Request.Header.Set("Content-Type", "application/json")
		c.Request.Header.Del("Content-Length")
		c.Request.ContentLength = int64(len(jsonData))

		c.Set(common.KeyRequestBody, jsonData)
		c.Set(CtxKeyImageAsync, true)
		c.Set("relay_mode", relayconstant.RelayModeImageSubmit)
		c.Set("action", action)
		c.Next()
	}
}

// asyncRequested 判断本请求是否要求异步。header 优先于 body：header 由基础设施注入，
// 在 passthrough 场景下是唯一可用的开关。
func asyncRequested(c *gin.Context, raw map[string]any) bool {
	if h := strings.TrimSpace(c.GetHeader(AsyncHeader)); h != "" {
		v, err := strconv.ParseBool(h)
		return err == nil && v
	}
	switch v := raw[asyncBodyField].(type) {
	case bool:
		return v
	case string:
		b, err := strconv.ParseBool(strings.TrimSpace(v))
		return err == nil && b
	}
	return false
}

// asyncHeaderSet 只看 header 的快速判定，用于在**不碰 body** 的前提下决定
// multipart 请求要不要往下走。
func asyncHeaderSet(c *gin.Context) bool {
	h := strings.TrimSpace(c.GetHeader(AsyncHeader))
	if h == "" {
		return false
	}
	v, err := strconv.ParseBool(h)
	return err == nil && v
}

// parseMultipartFromStorage 从 body storage 的字节解析 multipart，绝不读 c.Request.Body。
//
// 这是本仓处理「multipart + body 需要被下游复用」的既有手法
// （见 common.parseMultipartFormData）。用 c.Request.ParseMultipartForm 的话 body 会被
// 消费掉，而 body storage 是懒创建的 —— 后续第一次 GetBodyStorage 会从已经耗尽的
// c.Request.Body 读，拿到空内容，下游全线失灵。
//
// 调用方负责 form.RemoveAll()：ReadForm 会把超过内存阈值的部分落临时文件。
func parseMultipartFromStorage(c *gin.Context) (*multipart.Form, error) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return nil, err
	}
	data, err := storage.Bytes()
	if err != nil {
		return nil, err
	}
	_, params, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil {
		return nil, err
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, fmt.Errorf("multipart boundary not found")
	}
	return multipart.NewReader(bytes.NewReader(data), boundary).ReadForm(multipartMemoryLimit)
}

// multipartMemoryLimit ReadForm 的内存阈值，超出部分落临时文件（由 RemoveAll 清理）。
const multipartMemoryLimit = 32 << 20

// formValuesToMap 收集 multipart 表单的**标量字段**。文件字段不在这里处理，
// 由 imageInputsFrom 从同一个 form 读 —— 全流程只解析一次 multipart。
func formValuesToMap(form *multipart.Form) map[string]any {
	raw := make(map[string]any, len(form.Value))
	for k, v := range form.Value {
		if len(v) > 0 {
			raw[k] = v[0]
		}
	}
	return raw
}

func imageJSONToMap(c *gin.Context) (map[string]any, error) {
	if !strings.HasPrefix(c.GetHeader("Content-Type"), "application/json") {
		return nil, nil
	}
	var raw map[string]any
	if err := common.UnmarshalBodyReusable(c, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// imageInputsFrom 收集底图，统一成 []string（URL 或 data-uri），供适配器物化落 NFS。
//
// 三种到达形态：JSON 的 image（字符串或数组）、JSON 的 images（数组）、
// multipart 的 image/image[] 文件。与同步链路的 collectEditImages 保持同一套认法，
// 否则同一份请求在两种模式下会有不同的「底图找不到」行为。
//
// form 为 nil 表示非 multipart 请求。
func imageInputsFrom(raw map[string]any, form *multipart.Form) ([]string, error) {
	var out []string
	appendVal := func(v any) {
		switch t := v.(type) {
		case string:
			if s := strings.TrimSpace(t); s != "" {
				out = append(out, s)
			}
		case []any:
			for _, item := range t {
				if s, ok := item.(string); ok {
					if s = strings.TrimSpace(s); s != "" {
						out = append(out, s)
					}
				}
			}
		}
	}
	appendVal(raw["image"])
	appendVal(raw["images"])

	if form != nil {
		for _, fh := range multipartFiles(form, "image", "image[]") {
			dataURL, err := fileHeaderToDataURL(fh)
			if err != nil {
				return nil, fmt.Errorf("读取上传图片 %s 失败: %w", fh.Filename, err)
			}
			out = append(out, dataURL)
		}
		// 蒙版走 metadata.mask，与 JSON 形态同键 —— 适配器只认这一个键。
		// 不处理的话蒙版会被静默丢弃、出图看起来「成功」却没生效（同步链路
		// gpustackplus/adaptor.go:302 专门警告过这个坑）。
		if masks := multipartFiles(form, "mask", "mask[]"); len(masks) > 0 {
			dataURL, err := fileHeaderToDataURL(masks[0])
			if err != nil {
				return nil, fmt.Errorf("读取上传蒙版 %s 失败: %w", masks[0].Filename, err)
			}
			raw["mask"] = dataURL
		}
	}
	// 原始 image/images 键留在 metadata 里会被门面当作「检测到原始输入字段」整单 400
	// （适配器的 legacyInputKeys 只在 BuildRequestBody 里剥一次 metadata，
	//  但这两个键的值可能是几 MB 的 base64，早删早省内存）。
	delete(raw, "image")
	delete(raw, "images")
	return out, nil
}

func multipartFiles(form *multipart.Form, keys ...string) []*multipart.FileHeader {
	if form == nil || form.File == nil {
		return nil
	}
	var out []*multipart.FileHeader
	for _, k := range keys {
		out = append(out, form.File[k]...)
	}
	return out
}

// fileHeaderToDataURL 把上传文件转成 data-uri。
//
// 为什么要转：任务子系统的统一契约 TaskSubmitReq.Images 是 []string，
// 适配器的 NFS 物化只从字符串读输入。multipart 的 *FileHeader 到不了那里
// （它绑在原始请求上，而任务提交后请求就结束了）。
// base64 会让体积膨胀 ~33%，这是既有做法 —— 体验区上传的媒体本来就是 data-uri。
func fileHeaderToDataURL(fh *multipart.FileHeader) (string, error) {
	f, err := fh.Open()
	if err != nil {
		return "", err
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", fmt.Errorf("文件内容为空")
	}
	mime := strings.TrimSpace(fh.Header.Get("Content-Type"))
	if mime == "" || mime == "application/octet-stream" {
		mime = http.DetectContentType(data)
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func stringField(raw map[string]any, key string) string {
	if s, ok := raw[key].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// abortImageAsync 用 OpenAI 错误形状回请求。走到这里的都是「已确认要异步、但请求本身
// 有问题」，不能放行给同步链路 —— 客户端在等 job 对象，同步链路会回一个形状完全不同的
// ImageResponse，解析必崩。
func abortImageAsync(c *gin.Context, status int, message string) {
	c.JSON(status, gin.H{"error": gin.H{
		"message": message,
		"type":    "invalid_request_error",
		"code":    "invalid_async_image_request",
	}})
	c.Abort()
}
