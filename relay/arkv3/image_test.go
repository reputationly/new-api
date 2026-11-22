package arkv3

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
)

// 方舟图片请求 → 我们 /v1/images/generations 的 body。内置 volcengine-ark 适配器发的
// 就是这组字段（model/prompt/image/size/response_format/watermark），任何一项映射错了，
// app 里出图就失败在第一步。
func TestConvertImageRequestMapsArkFields(t *testing.T) {
	raw := `{"model":"minimax-h3-image","prompt":"一只橘猫","size":"2048x2048",
	         "response_format":"url","watermark":false}`
	body, err := ConvertImageRequest([]byte(raw))
	if err != nil {
		t.Fatalf("unexpected err: %+v", err)
	}
	if body["model"] != "minimax-h3-image" || body["prompt"] != "一只橘猫" {
		t.Fatalf("model/prompt 没透传: %+v", body)
	}
	if body["size"] != "2048x2048" {
		t.Fatalf("size 应原样透传: %v", body["size"])
	}
	if body["response_format"] != "url" {
		t.Fatalf("response_format = %v", body["response_format"])
	}
	if _, has := body["image"]; has {
		t.Fatalf("没有参考图时不该带 image: %+v", body)
	}
	if _, has := body["watermark"]; has {
		t.Fatalf("watermark=false 不必转发: %+v", body)
	}
}

// 单图走 image、多图走 images —— 我们自己的适配器两种都认，但语义不能混。
func TestConvertImageRequestReferenceShape(t *testing.T) {
	single := `{"model":"m","prompt":"p","image":"https://x/1.png"}`
	body, err := ConvertImageRequest([]byte(single))
	if err != nil {
		t.Fatalf("unexpected err: %+v", err)
	}
	if body["image"] != "https://x/1.png" {
		t.Fatalf("单图应走 image: %+v", body)
	}

	multi := `{"model":"m","prompt":"p","image":["https://x/1.png","https://x/2.png"]}`
	body, err = ConvertImageRequest([]byte(multi))
	if err != nil {
		t.Fatalf("unexpected err: %+v", err)
	}
	images, ok := body["images"].([]string)
	if !ok || len(images) != 2 {
		t.Fatalf("多图应走 images: %+v", body["images"])
	}
	if _, has := body["image"]; has {
		t.Fatalf("多图时不该同时给 image: %+v", body)
	}
}

// 做不到的一律显式拒绝：水印、组图、非法 response_format。
func TestConvertImageRequestRejectsUnsupported(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"水印", `{"model":"m","prompt":"p","watermark":true}`, "watermark=true"},
		{"组图", `{"model":"m","prompt":"p","sequential_image_generation":"enabled"}`, "sequential_image_generation"},
		{"缺 model", `{"prompt":"p"}`, "model is required"},
		{"缺 prompt", `{"model":"m"}`, "prompt is required"},
		{"坏 response_format", `{"model":"m","prompt":"p","response_format":"png"}`, "response_format"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ConvertImageRequest([]byte(tc.body))
			if err == nil {
				t.Fatalf("应拒绝 %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误信息应包含 %q，实际 %q", tc.want, err.Error())
			}
		})
	}
}

// 参考图数量上限：超过 10 张直接拒，别让下游猜。
func TestConvertImageRequestCapsReferences(t *testing.T) {
	items := make([]string, 12)
	for i := range items {
		items[i] = `"https://x/i.png"`
	}
	raw := `{"model":"m","prompt":"p","image":[` + strings.Join(items, ",") + `]}`
	_, err := ConvertImageRequest([]byte(raw))
	if err == nil || !strings.Contains(err.Error(), "at most 10") {
		t.Fatalf("超过 10 张应被拒，实际 %+v", err)
	}
}

// response_format 缺省必须是 url（方舟的缺省），不能落到 OpenAI 的 b64_json。
func TestConvertImageRequestDefaultsResponseFormatToURL(t *testing.T) {
	body, err := ConvertImageRequest([]byte(`{"model":"m","prompt":"p"}`))
	if err != nil {
		t.Fatalf("unexpected err: %+v", err)
	}
	if body["response_format"] != "url" {
		t.Fatalf("response_format 缺省应为 url，实际 %v", body["response_format"])
	}
	if _, err := common.Marshal(body); err != nil {
		t.Fatalf("转换结果不可序列化: %v", err)
	}
}
