package minimaxv2

import "testing"

// generate_audio 走 metadata：适配器把 metadata 平铺进上游 body，引擎读顶层。
//
// 三态都要钉住：
//   - 传 false → 写进 metadata（否则关不掉声音，且不报错）
//   - 传 true  → 写 true
//   - 不传     → **不写**，沿用官方默认（有声）
func TestConvertCreateRequestGenerateAudio(t *testing.T) {
	base := `{"model":"MiniMax-H3","duration":5,"resolution":"768P","ratio":"16:9",` +
		`"content":[{"type":"text","text":"a cat"}]`

	cases := []struct {
		name    string
		extra   string
		want    any
		present bool
	}{
		{"显式关闭", `,"generate_audio":false`, false, true},
		{"显式开启", `,"generate_audio":true`, true, true},
		{"不传", ``, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, _, apiErr := ConvertCreateRequest([]byte(base + tc.extra + "}"))
			if apiErr != nil {
				t.Fatalf("转换失败: %s", apiErr.Message)
			}
			meta, ok := body["metadata"].(map[string]any)
			if !ok {
				t.Fatalf("metadata 应为对象: %#v", body["metadata"])
			}
			got, present := meta["generate_audio"]
			if present != tc.present {
				t.Fatalf("generate_audio 存在性 = %v, want %v（漏传时不能写，否则与官方默认脱钩）", present, tc.present)
			}
			if tc.present && got != tc.want {
				t.Fatalf("generate_audio = %#v, want %#v", got, tc.want)
			}
			// 顶层不该出现这个键：统一任务契约里它只走 metadata。
			if _, ok := body["generate_audio"]; ok {
				t.Fatal("不应写到顶层")
			}
		})
	}
}

// 快照要记下**客户端原始**的模型名与解析出的 task_type：/v2 的错误改写要用它们
// 回显"该用哪个名字"，那时 body 已经被换成统一任务契约了。
func TestConvertCreateRequestSnapshotRecordsModelAndTaskType(t *testing.T) {
	raw := `{"model":"minimax-h3-2k","duration":5,"resolution":"768P",` +
		`"content":[{"type":"text","text":"a cat"},` +
		`{"type":"image_url","role":"reference_image","image_url":{"url":"https://x/a.png"}}]}`
	_, snapshot, apiErr := ConvertCreateRequest([]byte(raw))
	if apiErr != nil {
		t.Fatalf("转换失败: %s", apiErr.Message)
	}
	if snapshot.Model != "minimax-h3-2k" {
		t.Errorf("快照记录的是客户端原始模型名，实得 %q", snapshot.Model)
	}
	if snapshot.TaskType != taskTypeR2VA {
		t.Errorf("快照应记解析出的 task_type，实得 %q", snapshot.TaskType)
	}
}
