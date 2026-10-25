package controller

// `/v1/ov/*` —— 非模型能力的新命名空间。
//
// # 为什么单独一个命名空间
//
// `/v1/models`、`/v1/images/generations`、`/v1/videos` 是**模型调用**，走 relay：
// 选渠道、计费、日志、重试全都由它管。而能力目录、文件上传这些东西不是模型调用 ——
// 没有渠道可选，也不计 token。硬塞进 relay 会让每个适配器都要多一条分支，还会让
// "模型调用失败"和"目录读不到"混进同一份错误统计。
//
// 鉴权沿用 relay 的令牌（同一个 key、同一个 TokenAuth），**不另立一套**：客户端只有一个
// base_url 加一个 key。
//
// 路由挂载见 router/ov-router.go。

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/mediastore"
	"github.com/QuantumNous/new-api/service/ovcaps"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

// GetOvCapabilities 处理 `GET /v1/ov/capabilities`。
//
// 返回这个 key **实际能调**的能力清单。客户端按 `version` 缓存：它没变就不重新拉；
// 变了（管理员加模型、换分组、停渠道）才更新选择器。
func GetOvCapabilities(c *gin.Context) {
	capabilities := ovcaps.Build(capabilityScope(c))
	// 目录会随分组/渠道变化，但变化频率很低；客户端自己按 version 判断，
	// 所以这里明确不让中间层缓存，避免"改完配置客户端还是旧的"。
	c.Header("Cache-Control", "no-cache")
	c.JSON(http.StatusOK, gin.H{
		"version":      ovcaps.Version(capabilities),
		"capabilities": capabilities,
	})
}

// capabilityScope 把鉴权上下文翻成目录的可见范围。
//
// # 三个来源，缺一不可
//
//	abilities（有渠道）    这个分组下真能路由到的模型
//	令牌白名单             这个 key 被限定的模型
//	聚合模型的分组白名单    定向发放的编排能力
//
// 只看 abilities 会报出"这个 key 用不了"的模型；只看令牌白名单会把聚合模型漏掉
// （它们没有渠道 ability，见 controller/model.go 的说明）。
func capabilityScope(c *gin.Context) ovcaps.Scope {
	userGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
	userID := c.GetInt("id")
	if userGroup == "" && userID != 0 {
		if group, err := model.GetUserGroup(userID, false); err == nil {
			userGroup = group
		}
	}
	usingGroup := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
	if usingGroup == "" {
		usingGroup = userGroup
	}

	// 令牌生效分组下真有渠道的模型。
	enabled := make(map[string]bool)
	if usingGroup == "auto" {
		for _, autoGroup := range service.GetUserAutoGroup(userGroup) {
			for _, name := range model.GetGroupEnabledModels(autoGroup) {
				enabled[name] = true
			}
		}
	} else {
		for _, name := range model.GetGroupEnabledModels(usingGroup) {
			enabled[name] = true
		}
	}

	// 可见性裁剪：与 `/v1/models` 同一口径 —— 按**用户分组**，与令牌指向哪个路由分组无关。
	visible := make(map[string]bool, len(enabled))
	names := make([]string, 0, len(enabled))
	for name := range enabled {
		names = append(names, name)
	}
	for _, name := range model.FilterModelsByVisibility(names, userGroup) {
		if enabled[name] {
			visible[name] = true
		}
	}

	// 令牌白名单。**打开了却读不到内容时按"全禁"处理**：读不到就当成不限，
	// 会把一个受限的 key 看成全权限，而这个 key 正是发给外部集成方的那个。
	var tokenAllowed map[string]bool
	if c.GetBool("token_model_limit_enabled") {
		tokenAllowed = map[string]bool{}
		if raw, ok := c.Get("token_model_limit"); ok {
			if limits, ok := raw.(map[string]bool); ok {
				tokenAllowed = limits
			}
		}
	}

	return ovcaps.Scope{
		Enabled:      visible,
		TokenAllowed: tokenAllowed,
		UsingGroups:  service.AggregateUsingGroups(usingGroup, userGroup, service.GetUserAutoGroup),
	}
}

// PostOvFiles 处理 `POST /v1/ov/files`。
//
// multipart 上传一个参考素材，返回一个**平台可下载的签名 URL**。任务提交时把这个 URL
// 放进 metadata（`images` / `metadata.video` / `metadata.audio` …）即可 —— 平台的输入
// 物化会识别自家对象存储的 URL（`relay/channel/gpustackplus/nfsinput` 的
// resolveOwnOBSURL），不出网重拉。
//
// # 为什么要单独一条
//
// 客户端此前的做法是把参考素材整个转成 base64 塞进请求体。一个 100MB 的参考视频转完是
// 133MB，直接顶到请求体上限，而 base64 那 33% 的膨胀是纯浪费。
//
// # 按 key 隔离
//
// 对象键里带用户 id 段（`mediastore.BuildKey`），签名 URL 只对这一个对象有效，
// 有效期由系统设置的 `SignedURLTTLHours` 决定，返回值里明确告诉客户端。
func PostOvFiles(c *gin.Context) {
	if !mediastore.Enabled() {
		// 没配对象存储不是"服务器坏了"：客户端该退回内联（base64），而不是报错给用户。
		abortOv(c, http.StatusServiceUnavailable, types.ErrorCodeCapabilityUnavailable,
			"这个部署没有配置文件存储，请改用内联（base64）上传")
		return
	}
	fileHeader, err := c.FormFile("file")
	if err != nil {
		abortOv(c, http.StatusBadRequest, types.ErrorCodeInvalidRequest, "缺少 file 字段（multipart/form-data）")
		return
	}
	limit := mediastore.MaxUploadBytes()
	if limit > 0 && fileHeader.Size > limit {
		abortOv(c, http.StatusRequestEntityTooLarge, types.ErrorCodeInvalidRequest,
			"文件超过大小上限 "+strconv.FormatInt(limit/1024/1024, 10)+"MB")
		return
	}
	opened, err := fileHeader.Open()
	if err != nil {
		abortOv(c, http.StatusBadRequest, types.ErrorCodeInvalidRequest, "读取上传内容失败")
		return
	}
	defer opened.Close()
	// 多读 1 字节用来识别"声明的大小在撒谎"（客户端可以不报真实 Size）。
	body, err := io.ReadAll(io.LimitReader(opened, limit+1))
	if err != nil {
		abortOv(c, http.StatusBadRequest, types.ErrorCodeInvalidRequest, "读取上传内容失败")
		return
	}
	if limit > 0 && int64(len(body)) > limit {
		abortOv(c, http.StatusRequestEntityTooLarge, types.ErrorCodeInvalidRequest,
			"文件超过大小上限 "+strconv.FormatInt(limit/1024/1024, 10)+"MB")
		return
	}

	contentType := strings.TrimSpace(fileHeader.Header.Get("Content-Type"))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	key := mediastore.BuildKey("ov-upload", "", c.GetInt("id"), "u"+common.GetUUID()[:16], mediastore.ExtForMIME(contentType), time.Now())
	if err := mediastore.Persist(c.Request.Context(), key, mediastore.PersistSource{Data: body, ContentType: contentType}, map[string]string{
		"origin": "ov-files",
		"name":   sanitizeUploadName(fileHeader.Filename),
	}); err != nil {
		common.SysLog("ov files persist failed: " + err.Error())
		abortOv(c, http.StatusBadGateway, types.ErrorCodeCapabilityUnavailable, "对象存储写入失败，请改用内联（base64）上传")
		return
	}
	url, err := mediastore.Sign(c.Request.Context(), key)
	if err != nil {
		common.SysLog("ov files sign failed: " + err.Error())
		abortOv(c, http.StatusBadGateway, types.ErrorCodeCapabilityUnavailable, "对象存储签名失败，请改用内联（base64）上传")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"url":          url,
		"key":          key,
		"bytes":        len(body),
		"content_type": contentType,
		"expires_at":   time.Now().Add(mediastore.SignedURLTTL()).UTC().Format(time.RFC3339),
	})
}

// abortOv 统一的错误信封。形状与 relay 的 OpenAI 错误一致，客户端一套解析就够。
func abortOv(c *gin.Context, status int, code types.ErrorCode, message string) {
	c.JSON(status, gin.H{
		"error": gin.H{
			"message": common.MessageWithRequestId(message, c.GetString(common.RequestIdKey)),
			"type":    "new_api_error",
			"code":    string(code),
		},
	})
	c.Abort()
}

func sanitizeUploadName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "upload"
	}
	if len(trimmed) > 120 {
		trimmed = trimmed[:120]
	}
	return trimmed
}
