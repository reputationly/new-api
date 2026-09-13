package setting

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/QuantumNous/new-api/dto"
)

// 目录里每个 backend 都必须在官方枚举里。
//
// **写错一个名字的后果是整份目录被拒**（官方 gateway 的 zod 校验），
// 表现是客户端一个模型都没有 —— 而不是少一个模型。这个差别很要命：
// 前者看起来像"功能坏了"，后者才是"少配了一个"。
func TestDefaultCatalogBackendsAreAllOfficial(t *testing.T) {
	c := defaultHiloCatalog()
	for _, group := range [][]HiloCatalogEntry{c.Image, c.Video, c.Audio} {
		for _, e := range group {
			if !dto.HiloBackends[e.Model.Backend] {
				t.Fatalf("%s 的 backend %q 不在官方枚举里，整份目录会被拒",
					e.Model.ID, e.Model.Backend)
			}
		}
	}
}

// 每个 select 参数的默认值必须在它自己的选项里。
//
// 不在的话客户端打开就是"选中了一个不存在的项"，不报错、也选不回来。
func TestEveryDefaultIsOneOfItsOptions(t *testing.T) {
	c := defaultHiloCatalog()
	for _, group := range [][]HiloCatalogEntry{c.Image, c.Video, c.Audio} {
		for _, e := range group {
			for name, p := range e.Model.Params {
				if p.Type != "select" {
					continue
				}
				found := false
				for _, o := range p.Options {
					if o == p.Default {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("%s 的 %s 默认值 %q 不在 options %v 里",
						e.Model.ID, name, p.Default, p.Options)
				}
			}
		}
	}
}

// 参数互斥规则引用的参数必须真的存在。
//
// 引用了一个不存在的参数名，规则静默失效 —— 首尾帧驱动时比例照样能选，
// 而平台会把 size 反推成 `32:57` 然后拒掉整个任务。
func TestParamConstraintsReferenceRealParams(t *testing.T) {
	c := defaultHiloCatalog()
	for _, group := range [][]HiloCatalogEntry{c.Image, c.Video, c.Audio} {
		for _, e := range group {
			for _, pc := range e.Model.ParamConstraints {
				if _, ok := e.Model.Params[pc.If.Param]; !ok {
					t.Fatalf("%s 的约束条件引用了不存在的参数 %q", e.Model.ID, pc.If.Param)
				}
				if _, ok := e.Model.Params[pc.Disable.Param]; !ok {
					t.Fatalf("%s 的约束要禁用不存在的参数 %q", e.Model.ID, pc.Disable.Param)
				}
			}
		}
	}
}

// 序列化出来的字段名必须和官方一致。
//
// 官方**驼峰和下划线混用**（`max_refs` 和 `promptMaxLength` 在同一层），
// 这不是笔误，是他们的历史包袱。Go 的 json tag 写错一个，那个字段就等于
// 没传 —— 而必填字段缺失会让整份目录被拒。
func TestJSONKeysMatchOfficial(t *testing.T) {
	m := dto.HiloMediaModel{
		ID: "x", Name: "x", Backend: "qwen", Type: "image",
		DisplayName: "x", ToolNames: []string{}, Params: map[string]dto.HiloModelParam{},
		PricingID: "p", PromptMaxLength: 1, PromptLabel: "text",
		SupportsLastFrameOnly: true,
		InputMediaLimits:      &dto.HiloInputMediaLimits{ImageMinWidth: 1},
		ParamConstraints:      []dto.HiloParamConstraint{{}},
	}
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	// 取自官方真实响应（reference/hilo/models-config-3.0.14.json）。
	for _, key := range []string{
		"id", "name", "backend", "max_refs", "params", "type",
		"display_name", "description", "tool_names", "visibility",
		"icon_url", "hot",
		"pricingId", "promptMaxLength", "promptLabel",
		"supportsLastFrameOnly", "inputMediaLimits", "paramConstraints",
	} {
		if _, ok := got[key]; !ok {
			t.Errorf("缺少官方字段 %q", key)
		}
	}
	// 这几个一定不能出现 —— 说明 tag 写成了下划线版本。
	for _, wrong := range []string{
		"pricing_id", "prompt_max_length", "prompt_label",
		"supports_last_frame_only", "input_media_limits", "param_constraints",
	} {
		if _, ok := got[wrong]; ok {
			t.Errorf("出现了错误的字段名 %q（官方用驼峰）", wrong)
		}
	}
}

// 四个数组即使为空也必须是 `[]` 而不是 `null`。
//
// zod 要的是数组，`null` 会让整份目录被判 invalid schema。Go 里
// `var x []T` 序列化出来正是 `null` —— 必须 `make`。
func TestEmptyCatalogSerializesAsArraysNotNull(t *testing.T) {
	out := dto.HiloModelsConfig{
		ImageModels: make([]dto.HiloMediaModel, 0),
		VideoModels: make([]dto.HiloMediaModel, 0),
		AudioModels: make([]dto.HiloMediaModel, 0),
		TextModels:  make([]dto.HiloTextModel, 0),
	}
	raw, _ := json.Marshal(out)
	if string(raw) != `{"imageModels":[],"videoModels":[],"audioModels":[],"textModels":[]}` {
		t.Fatalf("空目录序列化不对：%s", raw)
	}
}

// 保存时就要拦下坏配置，而不是等客户端那头整份拒绝。
//
// 客户端 zod 的失败粒度是**整份目录** —— 一条漏写 `params`，所有模型
// 一起消失。管理员看到的是"保存成功但客户端什么都没有"，隔着一次重启,
// 根本联系不起来。
func TestSaveTimeValidationCatchesBrokenEntries(t *testing.T) {
	cases := []struct {
		name string
		json string
	}{
		{"backend 不在枚举里", `{"image":[{"platform_model":"m","model":{"id":"x","backend":"qwn","params":{}}}]}`},
		{"缺 id", `{"image":[{"platform_model":"m","model":{"backend":"qwen","params":{}}}]}`},
		{"缺 platform_model", `{"image":[{"model":{"id":"x","backend":"qwen","params":{}}}]}`},
		{"下拉框默认值不在选项里", `{"image":[{"platform_model":"m","model":{"id":"x","backend":"qwen",` +
			`"params":{"r":{"type":"select","label":"r","default":"4K","options":["1K","2K"]}}}}]}`},
		{"约束引用了不存在的参数", `{"image":[{"platform_model":"m","model":{"id":"x","backend":"qwen",` +
			`"params":{},"paramConstraints":[{"if":{"param":"nope","eq":"a"},"disable":{"param":"nope"}}]}}]}`},
	}
	for _, c := range cases {
		if err := UpdateHiloCatalogByJsonString(c.json); err == nil {
			t.Errorf("%s：应该保存失败，却通过了", c.name)
		}
	}
}

// nil 的 params / tool_names 要补成空对象和空数组。
//
// Go 把 nil map/slice 序列化成 `null`，而 zod 要的是 `z.record(...)`
// 和 `z.array(...)` —— `null` 会让整份目录被拒。管理员写 JSON 时省略
// 这两个键是很自然的事，不该因此报错，补上就行。
func TestNilMapsAreNormalizedNotRejected(t *testing.T) {
	const j = `{"image":[{"platform_model":"m","model":{"id":"x","name":"x","backend":"qwen","type":"image","display_name":"x"}}]}`
	if err := UpdateHiloCatalogByJsonString(j); err != nil {
		t.Fatalf("省略 params/tool_names 不该报错：%v", err)
	}
	got := GetHiloCatalog().Image[0].Model
	if got.Params == nil {
		t.Error("params 没补成空对象，序列化会是 null")
	}
	if got.ToolNames == nil {
		t.Error("tool_names 没补成空数组，序列化会是 null")
	}
	raw, _ := json.Marshal(got)
	if bytes.Contains(raw, []byte(`"params":null`)) || bytes.Contains(raw, []byte(`"tool_names":null`)) {
		t.Errorf("序列化里还有 null：%s", raw)
	}
	// 恢复默认，别影响同包里其他测试。
	_ = UpdateHiloCatalogByJsonString("")
}

// 配置解析失败时**保持原样**，不清空。
//
// 管理员手滑写坏一个逗号就让所有客户端的模型选择器变空，代价太大。
func TestBadJSONKeepsPreviousCatalog(t *testing.T) {
	before := len(GetHiloCatalog().Video)
	if err := UpdateHiloCatalogByJsonString("{ 这不是 json"); err == nil {
		t.Fatal("坏 JSON 应该报错")
	}
	if got := len(GetHiloCatalog().Video); got != before {
		t.Fatalf("解析失败后目录被改了：%d → %d", before, got)
	}
}
