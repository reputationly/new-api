package controller

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/watermark"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

type watermarkConfigRequest struct {
	Enabled        bool                                       `json:"enabled"`
	Templates      []system_setting.WatermarkTemplate         `json:"templates"`
	DefaultBinding system_setting.WatermarkBinding            `json:"default_binding"`
	GroupBindings  map[string]system_setting.WatermarkBinding `json:"group_bindings"`
}

func SaveWatermarkConfig(c *gin.Context) {
	var req watermarkConfigRequest
	if err := common.UnmarshalBodyReusable(c, &req); err != nil {
		common.ApiErrorMsg(c, "请求参数有误")
		return
	}
	for i := range req.Templates {
		req.Templates[i].Name = strings.TrimSpace(req.Templates[i].Name)
	}
	req.DefaultBinding.Image = strings.TrimSpace(req.DefaultBinding.Image)
	req.DefaultBinding.Video = strings.TrimSpace(req.DefaultBinding.Video)
	for group, binding := range req.GroupBindings {
		binding.Image = strings.TrimSpace(binding.Image)
		binding.Video = strings.TrimSpace(binding.Video)
		req.GroupBindings[group] = binding
	}
	if req.GroupBindings == nil {
		req.GroupBindings = map[string]system_setting.WatermarkBinding{}
	}
	if err := system_setting.ValidateWatermarkConfig(req.Templates, req.DefaultBinding, req.GroupBindings); err != nil {
		common.ApiError(c, err)
		return
	}
	templates, err := common.Marshal(req.Templates)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	defaultBinding, err := common.Marshal(req.DefaultBinding)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	groupBindings, err := common.Marshal(req.GroupBindings)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	updates := []struct{ key, value string }{
		{"watermark.enabled", strconv.FormatBool(req.Enabled)},
		{"watermark.templates", string(templates)},
		{"watermark.default_binding", string(defaultBinding)},
		{"watermark.group_bindings", string(groupBindings)},
	}
	for _, update := range updates {
		if err := model.UpdateOption(update.key, update.value); err != nil {
			common.ApiError(c, err)
			return
		}
	}
	common.ApiSuccess(c, nil)
}

func GetWatermarkStatus(c *gin.Context) {
	now := time.Now()
	failures24h, err := model.CountWatermarkFailuresSince(now.Add(-24 * time.Hour).Unix())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	failures7d, err := model.CountWatermarkFailuresSince(now.Add(-7 * 24 * time.Hour).Unix())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	recent, err := model.RecentWatermarkFailures(20)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	_, ffmpegErr := exec.LookPath("ffmpeg")
	_, ffprobeErr := exec.LookPath("ffprobe")
	fontPath := os.Getenv("WATERMARK_FONT_FILE")
	if fontPath == "" {
		fontPath = "/usr/share/fonts/truetype/wqy/wqy-microhei.ttc"
	}
	fontInfo, fontErr := os.Stat(fontPath)
	fontReady := fontErr == nil && !fontInfo.IsDir()
	ready, _ := watermark.Available()
	common.ApiSuccess(c, gin.H{
		"ffmpeg_ready": ffmpegErr == nil && ffprobeErr == nil,
		"font_ready":   fontReady,
		"available":    ready,
		"failures_24h": failures24h,
		"failures_7d":  failures7d,
		"recent":       recent,
	})
}

func PreviewWatermark(c *gin.Context) {
	var tpl system_setting.WatermarkTemplate
	if err := common.UnmarshalBodyReusable(c, &tpl); err != nil {
		common.ApiErrorMsg(c, "请求参数有误")
		return
	}
	tpl.Name = strings.TrimSpace(tpl.Name)
	if err := system_setting.ValidateWatermarkTemplates([]system_setting.WatermarkTemplate{tpl}); err != nil {
		common.ApiError(c, err)
		return
	}
	dir, err := os.MkdirTemp("", "watermark-preview-*")
	if err != nil {
		common.ApiError(c, err)
		return
	}
	defer os.RemoveAll(dir)
	input := filepath.Join(dir, "sample.png")
	output := filepath.Join(dir, "watermarked.png")
	cmd := exec.CommandContext(c.Request.Context(), "ffmpeg", "-nostdin", "-y", "-hide_banner", "-loglevel", "error",
		"-protocol_whitelist", "file", "-f", "lavfi", "-i", "color=c=0x808080:s=1024x576", "-frames:v", "1", "-c:v", "png", input)
	if out, err := cmd.CombinedOutput(); err != nil {
		common.ApiError(c, fmt.Errorf("生成水印预览样图失败: %w: %s", err, strings.TrimSpace(string(out))))
		return
	}
	if err := watermark.Apply(c.Request.Context(), input, output, system_setting.MediaKindImage, "png", tpl); err != nil {
		common.ApiError(c, err)
		return
	}
	image, err := os.ReadFile(output)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{"data_url": "data:image/png;base64," + base64.StdEncoding.EncodeToString(image)})
}
