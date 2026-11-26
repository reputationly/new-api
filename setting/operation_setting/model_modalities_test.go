package operation_setting

import "testing"

func TestValidateModelModalities(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{name: "空串表示不配置", raw: ""},
		{name: "空对象", raw: "{}"},
		{name: "合法值", raw: `{"qwen3.8-flash":["image","video"]}`},
		{name: "空数组允许", raw: `{"glm-5":[]}`},
		{name: "非法 JSON", raw: `{"qwen3.8-flash":["image"`, wantErr: true},
		{name: "值不是数组", raw: `{"qwen3.8-flash":"image"}`, wantErr: true},
		{name: "未知模态", raw: `{"qwen3.8-flash":["text"]}`, wantErr: true},
		{name: "空模型名", raw: `{"  ":["image"]}`, wantErr: true},
		{name: "trim 后重复的模型名", raw: `{"glm-5":["image"]," glm-5 ":["video"]}`, wantErr: true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := ValidateModelModalities(testCase.raw)
			if testCase.wantErr && err == nil {
				t.Fatalf("%q 应该被拒绝", testCase.raw)
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("%q 不应报错: %v", testCase.raw, err)
			}
		})
	}
}

func TestGetModelModalitiesNormalizesKeysAndValues(t *testing.T) {
	previous := GetGeneralSetting().ModelModalities
	t.Cleanup(func() { GetGeneralSetting().ModelModalities = previous })

	// 带空格的管理员 key 必须覆盖出厂条目；值做小写去重排序。
	GetGeneralSetting().ModelModalities = `{" qwen3.8-flash ":["VIDEO","image","image"]}`
	modalities := GetModelModalities()
	got := modalities["qwen3.8-flash"]
	if len(got) != 2 || got[0] != "image" || got[1] != "video" {
		t.Fatalf("qwen3.8-flash modalities = %v, want [image video]", got)
	}
	if _, exists := modalities[" qwen3.8-flash "]; exists {
		t.Fatal("带空格的 key 不应留下幽灵条目")
	}
	if _, exists := modalities["glm-4v-flash"]; !exists {
		t.Fatal("未被覆盖的出厂条目应保留")
	}
}
