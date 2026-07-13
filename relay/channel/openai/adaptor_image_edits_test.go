package openai

import (
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"

	"github.com/gin-gonic/gin"
)

func newJSONEditsContext(t *testing.T) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/edits", strings.NewReader("{}"))
	c.Request.Header.Set("Content-Type", "application/json")
	return c
}

func parseFormBody(t *testing.T, c *gin.Context, body *bytes.Buffer) (map[string]string, map[string][]byte) {
	t.Helper()
	_, params, err := mime.ParseMediaType(c.Request.Header.Get("Content-Type"))
	if err != nil {
		t.Fatalf("parse content type: %v", err)
	}
	reader := multipart.NewReader(bytes.NewReader(body.Bytes()), params["boundary"])
	fields := map[string]string{}
	files := map[string][]byte{}
	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("read part: %v", err)
		}
		data, _ := io.ReadAll(part)
		if part.FileName() != "" {
			files[part.FormName()] = data
		} else {
			fields[part.FormName()] = string(data)
		}
	}
	return fields, files
}

// JSON 版 edits 请求(底图为 data URL)应被转成 multipart,并改写 Content-Type。
func TestConvertImageRequestJSONEditsToMultipart(t *testing.T) {
	c := newJSONEditsContext(t)
	a := &Adaptor{}
	n := uint(1)
	req := dto.ImageRequest{
		Model:  "gpt-image-2",
		Prompt: "背景替换为晴天",
		N:      &n,
		// JSON 透传会保留的已知字段,转换后必须同样出现在表单中
		User:       []byte(`"u-123"`),
		Background: []byte(`"transparent"`),
		// "hello" 的 base64
		Image: []byte(`["data:image/png;base64,aGVsbG8="]`),
	}

	out, err := a.ConvertImageRequest(c, &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits}, req)
	if err != nil {
		t.Fatalf("convert failed: %v", err)
	}
	body, ok := out.(*bytes.Buffer)
	if !ok {
		t.Fatalf("expected *bytes.Buffer, got %T", out)
	}
	if !strings.HasPrefix(c.Request.Header.Get("Content-Type"), "multipart/form-data") {
		t.Fatalf("content type not rewritten: %s", c.Request.Header.Get("Content-Type"))
	}
	if !c.GetBool(jsonImageEditsConvertedKey) {
		t.Fatal("converted flag not set")
	}

	fields, files := parseFormBody(t, c, body)
	if fields["model"] != "gpt-image-2" || fields["prompt"] != "背景替换为晴天" || fields["n"] != "1" {
		t.Fatalf("unexpected fields: %v", fields)
	}
	// 字段集须与 JSON 透传一致:user/background 等已知字段不能在转换中丢失
	if fields["user"] != "u-123" || fields["background"] != "transparent" {
		t.Fatalf("passthrough fields missing in form: %v", fields)
	}
	if string(files["image"]) != "hello" {
		t.Fatalf("unexpected image part: %q", files["image"])
	}
}

// 多图应使用 image[] 字段名(与 multipart 透传分支一致)。
func TestConvertImageRequestJSONEditsMultiImage(t *testing.T) {
	c := newJSONEditsContext(t)
	a := &Adaptor{}
	req := dto.ImageRequest{
		Model:  "gpt-image-2",
		Prompt: "p",
		Image:  []byte(`["data:image/png;base64,aGVsbG8=","data:image/jpeg;base64,aGVsbG8="]`),
	}

	out, err := a.ConvertImageRequest(c, &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits}, req)
	if err != nil {
		t.Fatalf("convert failed: %v", err)
	}
	_, files := parseFormBody(t, c, out.(*bytes.Buffer))
	if _, ok := files["image[]"]; !ok {
		t.Fatalf("expected image[] part, got files: %v", files)
	}
}

// image 为远程 URL 时维持 JSON 透传,不改写 Content-Type。
func TestConvertImageRequestJSONEditsURLPassthrough(t *testing.T) {
	c := newJSONEditsContext(t)
	a := &Adaptor{}
	req := dto.ImageRequest{
		Model:  "gpt-image-2",
		Prompt: "p",
		Image:  []byte(`["https://example.com/a.png"]`),
	}

	out, err := a.ConvertImageRequest(c, &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits}, req)
	if err != nil {
		t.Fatalf("convert failed: %v", err)
	}
	if _, ok := out.(dto.ImageRequest); !ok {
		t.Fatalf("expected passthrough dto.ImageRequest, got %T", out)
	}
	if ct := c.Request.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content type should stay json: %s", ct)
	}
}

// 渠道重试:首次转换已把 Content-Type 改成 multipart,二次进入仍应按原始 JSON 体转换,
// 而不是误走 c.MultipartForm() 解析分支报 failed to parse multipart form。
func TestConvertImageRequestJSONEditsRetry(t *testing.T) {
	c := newJSONEditsContext(t)
	a := &Adaptor{}
	req := dto.ImageRequest{
		Model:  "gpt-image-2",
		Prompt: "p",
		Image:  []byte(`["data:image/png;base64,aGVsbG8="]`),
	}
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits}

	if _, err := a.ConvertImageRequest(c, info, req); err != nil {
		t.Fatalf("first convert failed: %v", err)
	}
	out, err := a.ConvertImageRequest(c, info, req)
	if err != nil {
		t.Fatalf("retry convert failed: %v", err)
	}
	if _, ok := out.(*bytes.Buffer); !ok {
		t.Fatalf("expected *bytes.Buffer on retry, got %T", out)
	}
}
