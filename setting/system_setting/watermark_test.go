package system_setting

import (
	"strings"
	"testing"
)

func TestWatermarkDefaultSettings(t *testing.T) {
	settings := GetWatermarkSettings()
	if settings.Enabled {
		t.Fatal("watermark should be disabled by default")
	}
	if len(settings.Templates) != 1 {
		t.Fatalf("default templates = %d, want 1", len(settings.Templates))
	}
	template := settings.Templates[0]
	if template.Name != "默认" || template.Text != "AI生成" || template.Position != "bottom_right" ||
		template.FontScale != 0.04 || template.Opacity != 0.6 || template.Color != "#FFFFFF" {
		t.Fatalf("unexpected default template: %+v", template)
	}
	if settings.DefaultBinding.Image != "默认" || settings.DefaultBinding.Video != "默认" {
		t.Fatalf("unexpected default binding: %+v", settings.DefaultBinding)
	}
	if len(settings.GroupBindings) != 0 {
		t.Fatalf("default group bindings = %+v, want empty", settings.GroupBindings)
	}
}

func TestWatermarkResolveTemplate(t *testing.T) {
	settings := &WatermarkSettings{
		Enabled: true,
		Templates: []WatermarkTemplate{
			{Name: "default", Text: "AI"},
			{Name: "group", Text: "group"},
		},
		DefaultBinding: WatermarkBinding{Image: "default", Video: "default"},
		GroupBindings: map[string]WatermarkBinding{
			"inherited": {},
			"none":      {Image: "none", Video: "none"},
			"explicit":  {Image: "group", Video: "group"},
			"missing":   {Image: "deleted", Video: "deleted"},
		},
	}
	tests := []struct {
		name         string
		group        string
		kind         MediaKind
		defaultImage string
		want         string
	}{
		{name: "disabled", group: "explicit", kind: MediaKindImage, want: ""},
		{name: "group inherits default", group: "inherited", kind: MediaKindImage, want: "default"},
		{name: "group none", group: "none", kind: MediaKindImage, want: ""},
		{name: "group explicit", group: "explicit", kind: MediaKindImage, want: "group"},
		{name: "unconfigured group uses default", group: "new", kind: MediaKindVideo, want: "default"},
		{name: "default none", group: "new", kind: MediaKindImage, defaultImage: "none", want: ""},
		{name: "template missing", group: "missing", kind: MediaKindImage, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			enabled := settings.Enabled
			defaultImage := settings.DefaultBinding.Image
			if test.name == "disabled" {
				settings.Enabled = false
			}
			if test.defaultImage != "" {
				settings.DefaultBinding.Image = test.defaultImage
			}
			got := settings.ResolveTemplate(test.group, test.kind)
			settings.Enabled = enabled
			settings.DefaultBinding.Image = defaultImage
			if test.want == "" {
				if got != nil {
					t.Fatalf("ResolveTemplate() = %+v, want nil", got)
				}
				return
			}
			if got == nil || got.Name != test.want {
				t.Fatalf("ResolveTemplate() = %+v, want template %q", got, test.want)
			}
		})
	}

}

func TestValidateWatermarkConfig(t *testing.T) {
	valid := WatermarkTemplate{
		Name: "default", Text: "AI生成", Position: "bottom_right",
		FontScale: 0.04, Opacity: 0.6, Color: "#FFFFFF",
	}
	tests := []struct {
		name      string
		templates []WatermarkTemplate
		binding   WatermarkBinding
		groups    map[string]WatermarkBinding
	}{
		{name: "duplicate names", templates: []WatermarkTemplate{valid, valid}, binding: WatermarkBinding{Image: "default"}},
		{name: "reserved none name", templates: []WatermarkTemplate{{Name: "none", Text: "x", Position: "center", FontScale: .04, Opacity: .6, Color: "#FFFFFF"}}},
		{name: "empty text", templates: []WatermarkTemplate{{Name: "default", Text: " ", Position: "center", FontScale: .04, Opacity: .6, Color: "#FFFFFF"}}},
		{name: "text too long", templates: []WatermarkTemplate{{Name: "default", Text: strings.Repeat("x", 51), Position: "center", FontScale: .04, Opacity: .6, Color: "#FFFFFF"}}},
		{name: "control character", templates: []WatermarkTemplate{{Name: "default", Text: "x\n", Position: "center", FontScale: .04, Opacity: .6, Color: "#FFFFFF"}}},
		{name: "invalid position", templates: []WatermarkTemplate{{Name: "default", Text: "x", Position: "middle", FontScale: .04, Opacity: .6, Color: "#FFFFFF"}}},
		{name: "invalid color", templates: []WatermarkTemplate{{Name: "default", Text: "x", Position: "center", FontScale: .04, Opacity: .6, Color: "white"}}},
		{name: "font scale too small", templates: []WatermarkTemplate{{Name: "default", Text: "x", Position: "center", FontScale: .009, Opacity: .6, Color: "#FFFFFF"}}},
		{name: "opacity too large", templates: []WatermarkTemplate{{Name: "default", Text: "x", Position: "center", FontScale: .04, Opacity: 1.01, Color: "#FFFFFF"}}},
		{name: "default dangling reference", templates: []WatermarkTemplate{valid}, binding: WatermarkBinding{Image: "deleted"}},
		{name: "group dangling reference", templates: []WatermarkTemplate{valid}, groups: map[string]WatermarkBinding{"g": {Video: "deleted"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateWatermarkConfig(test.templates, test.binding, test.groups); err == nil {
				t.Fatal("ValidateWatermarkConfig() succeeded, want error")
			}
		})
	}
}
