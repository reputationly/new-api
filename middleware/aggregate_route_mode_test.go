package middleware

import (
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/setting"
)

// routeByMode：按 image_mode / task_type 用目录的 mode_models 分派 checkpoint。
// 真机故障：聚合 minimax-h3-2k（帧族 fl2va）收到 image_mode=reference 时仍发帧族，
// 上游报 "task_type 'r2va' needs the Ref2VA checkpoint"。
func TestRouteByMode(t *testing.T) {
	catalog := `{"video":[{"platform_model":"h3-fl2va","model":{"id":"MiniMax-H3","name":"MiniMax H3","backend":"minimax_v3","model_name":"minimax-h3-2k","type":"video","params":{}},"mode_models":{"first-last-frame":"h3-fl2va","reference":"h3-ref2va"}}]}`
	if err := setting.UpdateHiloCatalogByJsonString(catalog); err != nil {
		t.Fatalf("set catalog: %v", err)
	}
	defer func() { _ = setting.UpdateHiloCatalogByJsonString("") }()

	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"image_mode=reference", map[string]any{"image_mode": "reference"}, "h3-ref2va"},
		{"image_mode=first-last-frame → 帧族 checkpoint", map[string]any{"image_mode": "first-last-frame"}, "h3-fl2va"},
		{"metadata.task_type=r2va", map[string]any{"metadata": map[string]any{"task_type": "r2va"}}, "h3-ref2va"},
		{"无信息不猜", map[string]any{}, ""},
	}
	if e, ok := setting.FindHiloEntry("minimax-h3-2k"); !ok {
		t.Fatalf("catalog entry 未找到")
	} else {
		t.Logf("entry ok: platform=%s mode_models=%v", e.PlatformModel, e.ModeModels)
	}
	// 真实配置里 model_name 是给客户端看的名字（MiniMax-H3），平台模型名才是 minimax-h3-2k ——
	// 按平台名必须也能找到，否则 mode_models 路由静默失效。
	if e, ok := setting.FindHiloEntryByPlatformModel("h3-fl2va"); !ok || len(e.ModeModels) == 0 {
		t.Fatalf("按平台模型名没找到目录项")
	}
	for _, tc := range cases {
		got, err := routeByMode("minimax-h3-2k", tc.body)
		if err != nil {
			t.Fatalf("%s: unexpected err %v", tc.name, err)
		}
		if got != tc.want {
			t.Fatalf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}

var _ = json.Marshal
