package controller

import (
	"encoding/base64"
	"fmt"
	"io"
	"mime/multipart"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/service/moderation"
)

// /v1/images/edits 的上传原图提取（挂载点 C-2，docs/content-moderation-design.md §7 C-2）。
//
// 为什么不能靠 C-3 顺带覆盖：`ImageRequest.GetTokenCountMeta()` 只填 CombineText，
// 不填 Files（dto/openai_image.go:158）。而这条路是最直接的高危面——拿一张违规图去 edit。
//
// 两种到达形态都要吃：
//   - JSON / multipart 的**字符串**值：`image` 与 `images` 字段，URL 或 data-url。
//     multipart 分支把 formData.Get("image") 塞进了 ImageRequest.Image
//     （relay/helper/valid_request.go:157），所以两种传法在这里长得一样。
//   - multipart 的**文件**：留在 c.Request.MultipartForm.File 里，ImageRequest 完全看不到。

// maxMultipartModerationBytes 单个上传文件送审时读入内存的上限。
//
// 要有上限是因为这段字节要 base64 之后进 HTTP 请求体。超限的文件跳过送审而不是
// 报错——它接下来多半会被上游或大小校验拒掉，在审核这一层把它变成 500 只会
// 掩盖真正的原因。跳过会记一条日志。
const maxMultipartModerationBytes = 32 << 20

// imageEditMediaItems 收集 /v1/images/edits 请求里待审的图片。
func imageEditMediaItems(c *gin.Context, request dto.Request) []moderation.MediaItem {
	req, ok := request.(*dto.ImageRequest)
	if !ok {
		return nil
	}

	var items []moderation.MediaItem
	seen := make(map[string]bool)
	add := func(value, field string) {
		if value == "" || seen[value] {
			return
		}
		fileType, ok := moderation.ClassifyMedia(value)
		if !ok {
			return
		}
		seen[value] = true
		items = append(items, moderation.MediaItem{URL: value, Type: fileType, Field: field})
	}

	for _, v := range rawMessageStrings(req.Image) {
		add(v, "image")
	}
	for i, v := range rawMessageStrings(req.Images) {
		add(v, fmt.Sprintf("images[%d]", i))
	}
	items = append(items, multipartImageItems(c, seen)...)
	return items
}

// rawMessageStrings 把 json.RawMessage 里的字符串叶子取出来。
//
// `image` / `images` 两个字段都是 RawMessage，客户端可以传单个字符串也可以传数组
// （dto/openai_image.go:30,37 的注释与 gpustackplus 的 collectEditImages 都按两种形态处理）。
// 只认这两种：其它形态说明客户端传了我们不认识的结构，交给下游报错，
// 在审核层猜它的意思只会猜错。
func rawMessageStrings(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	switch common.GetJsonType(raw) {
	case "string":
		var s string
		if err := common.Unmarshal(raw, &s); err != nil {
			return nil
		}
		return []string{s}
	case "array":
		var arr []string
		if err := common.Unmarshal(raw, &arr); err != nil {
			return nil
		}
		return arr
	}
	return nil
}

// multipartImageItems 把 multipart 上传的图片文件读成 data-url。
//
// 跳过 mask：蒙版是几何形状（黑白区域），送去判色情暴力只会产生噪音，而它的内容
// 也不会出现在产物里——它决定的是「改哪块区域」，不是「画什么」。
func multipartImageItems(c *gin.Context, seen map[string]bool) []moderation.MediaItem {
	if c.Request == nil || c.Request.MultipartForm == nil {
		return nil
	}
	var items []moderation.MediaItem
	for field, headers := range c.Request.MultipartForm.File {
		if strings.HasPrefix(strings.ToLower(field), "mask") {
			continue
		}
		for i, fh := range headers {
			dataURL, err := fileHeaderToDataURL(fh)
			if err != nil {
				logger.LogWarn(c, fmt.Sprintf(
					"content moderation: 上传文件读取失败，该文件未送审 field=%s name=%s err=%v",
					field, fh.Filename, err))
				continue
			}
			if dataURL == "" || seen[dataURL] {
				continue
			}
			fileType, ok := moderation.ClassifyMedia(dataURL)
			if !ok {
				continue
			}
			seen[dataURL] = true
			items = append(items, moderation.MediaItem{
				URL:   dataURL,
				Type:  fileType,
				Field: fmt.Sprintf("%s[%d]", field, i),
			})
		}
	}
	return items
}

// fileHeaderToDataURL 读出上传文件并拼成 data-url。
//
// 不消费原始文件：FileHeader.Open 每次返回一个独立的 reader，读完不影响后续
// adaptor 再次打开它。
func fileHeaderToDataURL(fh *multipart.FileHeader) (string, error) {
	if fh.Size > maxMultipartModerationBytes {
		return "", fmt.Errorf("文件 %d 字节，超过送审上限 %d", fh.Size, maxMultipartModerationBytes)
	}
	f, err := fh.Open()
	if err != nil {
		return "", err
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, maxMultipartModerationBytes))
	if err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", nil
	}

	mime := fh.Header.Get("Content-Type")
	if mime == "" || mime == "application/octet-stream" {
		// 客户端没声明或声明成通用二进制时按扩展名兜底。认不出就返回空——
		// ClassifyMedia 会跳过它，而不是拼一个必然解码失败的串去触发 fail-close。
		mime = mimeByExt(fh.Filename)
	}
	if mime == "" {
		return "", nil
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data), nil
}

func mimeByExt(name string) string {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")) {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "webp":
		return "image/webp"
	case "gif":
		return "image/gif"
	case "bmp":
		return "image/bmp"
	case "mp4":
		return "video/mp4"
	case "mov":
		return "video/quicktime"
	case "webm":
		return "video/webm"
	}
	return ""
}
