package system_setting

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

type MediaKind string

const (
	MediaKindImage MediaKind = "image"
	MediaKindVideo MediaKind = "video"
)

type WatermarkTemplate struct {
	Name      string  `json:"name"`
	Text      string  `json:"text"`
	Position  string  `json:"position"`
	FontScale float64 `json:"font_scale"`
	Opacity   float64 `json:"opacity"`
	Color     string  `json:"color"`
}

type WatermarkBinding struct {
	Image string `json:"image"`
	Video string `json:"video"`
}

type WatermarkSettings struct {
	Enabled        bool                        `json:"enabled"`
	Templates      []WatermarkTemplate         `json:"templates"`
	DefaultBinding WatermarkBinding            `json:"default_binding"`
	GroupBindings  map[string]WatermarkBinding `json:"group_bindings"`
}

var watermarkSettings = WatermarkSettings{
	Templates: []WatermarkTemplate{{
		Name: "默认", Text: "AI生成", Position: "bottom_right",
		FontScale: 0.04, Opacity: 0.6, Color: "#FFFFFF",
	}},
	DefaultBinding: WatermarkBinding{Image: "默认", Video: "默认"},
	GroupBindings:  map[string]WatermarkBinding{},
}

var watermarkColorPattern = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

func init() {
	config.GlobalConfig.Register("watermark", &watermarkSettings)
}

func GetWatermarkSettings() *WatermarkSettings {
	return &watermarkSettings
}

func (s *WatermarkSettings) ResolveTemplate(group string, kind MediaKind) *WatermarkTemplate {
	if s == nil || !s.Enabled {
		return nil
	}
	binding := s.DefaultBinding
	if groupBinding, ok := s.GroupBindings[group]; ok {
		var selected string
		if kind == MediaKindImage {
			selected = groupBinding.Image
		} else if kind == MediaKindVideo {
			selected = groupBinding.Video
		}
		if selected != "" {
			if kind == MediaKindImage {
				binding.Image = selected
			} else {
				binding.Video = selected
			}
		}
	}
	name := binding.Image
	if kind == MediaKindVideo {
		name = binding.Video
	} else if kind != MediaKindImage {
		return nil
	}
	if name == "" || name == "none" {
		return nil
	}
	for i := range s.Templates {
		if s.Templates[i].Name == name {
			tpl := s.Templates[i]
			return &tpl
		}
	}
	return nil
}

func ValidateWatermarkTemplates(templates []WatermarkTemplate) error {
	if len(templates) == 0 {
		return errors.New("至少需要一个水印模板")
	}
	seen := make(map[string]bool, len(templates))
	positions := map[string]bool{
		"top_left": true, "top_right": true, "bottom_left": true,
		"bottom_right": true, "center": true,
	}
	for i, tpl := range templates {
		name := strings.TrimSpace(tpl.Name)
		if name != tpl.Name || name == "" || len([]rune(name)) > 32 || name == "none" {
			return fmt.Errorf("第 %d 个模板名称无效（名称需非空、不超过 32 字符且不能为 none）", i+1)
		}
		if seen[name] {
			return fmt.Errorf("水印模板名称重复：%s", name)
		}
		seen[name] = true
		text := strings.TrimSpace(tpl.Text)
		if text == "" || len([]rune(text)) > 50 {
			return fmt.Errorf("模板「%s」文案需非空且不超过 50 字符", name)
		}
		for _, r := range tpl.Text {
			if unicode.IsControl(r) {
				return fmt.Errorf("模板「%s」文案不能包含控制字符", name)
			}
		}
		if !positions[tpl.Position] {
			return fmt.Errorf("模板「%s」位置非法：%s", name, tpl.Position)
		}
		if tpl.FontScale < 0.01 || tpl.FontScale > 0.2 {
			return fmt.Errorf("模板「%s」字号比例需在 0.01 到 0.2 之间", name)
		}
		if tpl.Opacity < 0.05 || tpl.Opacity > 1 {
			return fmt.Errorf("模板「%s」透明度需在 0.05 到 1 之间", name)
		}
		if !watermarkColorPattern.MatchString(tpl.Color) {
			return fmt.Errorf("模板「%s」颜色必须为 #RRGGBB", name)
		}
	}
	return nil
}

func validateWatermarkBindings(defaultBinding WatermarkBinding, groupBindings map[string]WatermarkBinding, templates []WatermarkTemplate) error {
	names := make(map[string]bool, len(templates))
	for _, tpl := range templates {
		names[strings.TrimSpace(tpl.Name)] = true
	}
	check := func(label, value string, allowInherit bool) error {
		if value == "" && allowInherit || value == "none" {
			return nil
		}
		if !names[value] {
			return fmt.Errorf("%s引用了不存在的水印模板「%s」", label, value)
		}
		return nil
	}
	for _, item := range []struct {
		label string
		value string
	}{
		{"默认图片绑定", defaultBinding.Image}, {"默认视频绑定", defaultBinding.Video},
	} {
		if err := check(item.label, item.value, true); err != nil {
			return err
		}
	}
	for group, binding := range groupBindings {
		if err := check(fmt.Sprintf("分组「%s」图片绑定", group), binding.Image, true); err != nil {
			return err
		}
		if err := check(fmt.Sprintf("分组「%s」视频绑定", group), binding.Video, true); err != nil {
			return err
		}
	}
	return nil
}

func ValidateWatermarkConfig(templates []WatermarkTemplate, defaultBinding WatermarkBinding, groupBindings map[string]WatermarkBinding) error {
	if err := ValidateWatermarkTemplates(templates); err != nil {
		return err
	}
	return validateWatermarkBindings(defaultBinding, groupBindings, templates)
}

func ValidateWatermarkTemplatesJSON(raw string) error {
	var templates []WatermarkTemplate
	if err := common.UnmarshalJsonStr(raw, &templates); err != nil {
		return fmt.Errorf("模板配置不是合法的 JSON：%w", err)
	}
	current := GetWatermarkSettings()
	return ValidateWatermarkConfig(templates, current.DefaultBinding, current.GroupBindings)
}

func ValidateWatermarkDefaultBindingJSON(raw string) error {
	var binding WatermarkBinding
	if err := common.UnmarshalJsonStr(raw, &binding); err != nil {
		return fmt.Errorf("默认绑定不是合法的 JSON：%w", err)
	}
	current := GetWatermarkSettings()
	return ValidateWatermarkConfig(current.Templates, binding, current.GroupBindings)
}

func ValidateWatermarkGroupBindingsJSON(raw string) error {
	var bindings map[string]WatermarkBinding
	if err := common.UnmarshalJsonStr(raw, &bindings); err != nil {
		return fmt.Errorf("分组绑定不是合法的 JSON：%w", err)
	}
	current := GetWatermarkSettings()
	return ValidateWatermarkConfig(current.Templates, current.DefaultBinding, bindings)
}
