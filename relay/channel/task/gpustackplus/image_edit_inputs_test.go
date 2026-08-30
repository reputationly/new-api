package gpustackplus

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

// 异步图片编辑(i2i)的输入校验。这些分支都在真正写 NFS 之前返回，所以测试不碰磁盘。
//
// 这组用例守护的核心命题：i2i 在异步链路上必须与同步链路等能力。
// 改造前 i2i 落在 materializeVideoInputs（那支只取 images[0]、完全不认蒙版），
// 结果就是「同一份请求，同步能多图融合 + 带蒙版，异步静默退化成单图无蒙版」。

func newTestGinContext() *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/images/edits", nil)
	return c
}

func callImageEditInputs(t *testing.T, req relaycommon.TaskSubmitReq) error {
	t.Helper()
	// TaskRelayInfo 必须给：task 链路上它由 GenRelayInfo 恒初始化
	// （relay/common/relay_info.go:575），适配器据此读 PublicTaskID 当 input-group id。
	info := &relaycommon.RelayInfo{
		UserId:        1,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_test"},
	}
	_, err := materializeImageEditInputs(newTestGinContext(), info, "i2i", "qwen-image-edit", req)
	return err
}

func TestImageEditInputsRejectsMissingBaseImage(t *testing.T) {
	err := callImageEditInputs(t, relaycommon.TaskSubmitReq{})
	if err == nil {
		t.Fatal("i2i without a base image should be rejected")
	}
	if !strings.Contains(err.Error(), "底图") {
		t.Errorf("error should name the missing input, got: %s", err)
	}
}

func TestImageEditInputsRejectsTooManyImages(t *testing.T) {
	images := make([]string, 6) // MaxImageRefs = 5
	for i := range images {
		images[i] = "https://example.com/a.png"
	}
	err := callImageEditInputs(t, relaycommon.TaskSubmitReq{Images: images})
	if err == nil {
		t.Fatal("i2i with 6 base images should be rejected (MaxImageRefs = 5)")
	}
	if !strings.Contains(err.Error(), "最多支持") {
		t.Errorf("error should mention the cap, got: %s", err)
	}
}

// 引擎约束：带蒙版时底图必须恰好 1 张。与同步链路 materializeEditInputs 同一条防呆 ——
// 两条链路对同一份请求必须给出相同的接受/拒绝判断。
func TestImageEditInputsRejectsMaskWithMultipleImages(t *testing.T) {
	err := callImageEditInputs(t, relaycommon.TaskSubmitReq{
		Images:   []string{"https://example.com/a.png", "https://example.com/b.png"},
		Metadata: map[string]any{"mask": "https://example.com/m.png"},
	})
	if err == nil {
		t.Fatal("mask with 2 base images should be rejected")
	}
	if !strings.Contains(err.Error(), "蒙版") {
		t.Errorf("error should mention the mask constraint, got: %s", err)
	}
}

// 反向用例：多图但不带蒙版是合法的（hunyuan-image-3 的多图融合），
// 不能被上面那条防呆误伤。这里只断言「没有在校验阶段被拒」——
// 再往下就要真写 NFS 了，那属于集成测试范围。
func TestImageEditInputsAllowsMultipleImagesWithoutMask(t *testing.T) {
	err := callImageEditInputs(t, relaycommon.TaskSubmitReq{
		Images: []string{"https://example.com/a.png", "https://example.com/b.png"},
	})
	if err != nil && strings.Contains(err.Error(), "蒙版") {
		t.Errorf("multi-image without mask was wrongly rejected by the mask guard: %s", err)
	}
	if err != nil && strings.Contains(err.Error(), "最多支持") {
		t.Errorf("2 images should be within MaxImageRefs: %s", err)
	}
}

// i2i 必须由 BuildRequestBody 路由到 materializeImageEditInputs，而不是落回
// default 分支的 materializeVideoInputs。
//
// 判据是那条只有新函数才有的错误信息（「带蒙版的图片编辑只允许 1 张底图」）——
// materializeVideoInputs 压根不读蒙版，落回去的话这份请求会被静默接受，
// 蒙版丢失、第二张底图丢失，而调用方看到的是一次「成功」的提交。
func TestBuildRequestBodyRoutesI2IToImageEditInputs(t *testing.T) {
	c := newTestGinContext()
	req := relaycommon.TaskSubmitReq{
		Model:  "qwen-image-edit",
		Prompt: "make it blue",
		Images: []string{"https://example.com/a.png", "https://example.com/b.png"},
		Metadata: map[string]any{
			"task_type": "i2i",
			"mask":      "https://example.com/m.png",
		},
	}
	c.Set("task_request", req)

	// UpstreamModelName 挂在 ChannelMeta 上（RelayInfo 内嵌指针），
	// 不给就是空指针解引用 —— 真实链路由 InitChannelMeta 填。
	info := &relaycommon.RelayInfo{
		UserId:        1,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_test"},
		ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: "qwen-image-edit"},
	}
	info.OriginModelName = "qwen-image-edit"

	a := &TaskAdaptor{}
	_, err := a.BuildRequestBody(c, info)
	if err == nil {
		t.Fatal("i2i with mask + 2 base images should be rejected; it was accepted, " +
			"which means the request fell through to materializeVideoInputs (mask silently dropped)")
	}
	if !strings.Contains(err.Error(), "蒙版") {
		t.Errorf("expected the mask constraint error from materializeImageEditInputs, got: %s", err)
	}
}

// 异步图片任务必须带自己的 action。图片与视频共用同一个 platform（渠道类型数字），
// action 是任务表里区分二者的依据之一；统一写成 generate 的话，运营在任务列表里
// 看到的一堆 "generate" 分不出哪条是出图、哪条是出视频。
func TestTaskActionOf(t *testing.T) {
	cases := []struct {
		name string
		set  string
		want string
	}{
		{"未设置 → 视频默认", "", constant.TaskActionGenerate},
		{"文生图", constant.TaskActionImageGenerate, constant.TaskActionImageGenerate},
		{"图生图", constant.TaskActionImageEdit, constant.TaskActionImageEdit},
		// 白名单之外的值一律回落，避免别处塞进来的任意字符串污染 action 列。
		{"非图片 action 不采纳", constant.TaskActionTextGenerate, constant.TaskActionGenerate},
		{"垃圾值不采纳", "../../etc/passwd", constant.TaskActionGenerate},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestGinContext()
			if tc.set != "" {
				c.Set("action", tc.set)
			}
			if got := taskActionOf(c); got != tc.want {
				t.Errorf("taskActionOf = %q, want %q", got, tc.want)
			}
		})
	}
}

// 蒙版物化后，原始 mask 键必须从发往门面的 body 里剥掉。
//
// 这是端到端实测抓到的缺陷（单测与三轮 code review 都没发现）：legacyInputKeys 里原本
// 只有 image_mask，没有 mask，于是异步 i2i 的提交体里残留一个裸的
// "mask": "data:image/png;base64,...."。后果有两个，都不轻：
//   - 门面见到原始输入字段会「整单 400」（见本文件头部的门面契约注释）；
//   - 几 MB 的 base64 被原样发给门面，而 NFS 物化方案的全部意义就是不发 base64。
func TestBuildRequestBodyStripsRawMaskKey(t *testing.T) {
	c := newTestGinContext()
	req := relaycommon.TaskSubmitReq{
		Model:  "qwen-image-edit",
		Prompt: "make it blue",
		Images: []string{"data:image/png;base64,iVBORw0KGgoAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"},
		Metadata: map[string]any{
			"task_type": "i2i",
			"mask":      "data:image/png;base64,iVBORw0KGgoAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		},
	}
	c.Set("task_request", req)

	// 物化会真写盘，指到临时目录（默认 /nfs-output 在测试机上不可写）。
	setNFSRoot(t, t.TempDir())

	info := &relaycommon.RelayInfo{
		UserId:        1,
		TaskRelayInfo: &relaycommon.TaskRelayInfo{PublicTaskID: "task_strip_mask"},
		ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: "qwen-image-edit"},
	}
	info.OriginModelName = "qwen-image-edit"

	a := &TaskAdaptor{}
	reader, err := a.BuildRequestBody(c, info)
	if err != nil {
		t.Fatalf("BuildRequestBody: %s", err)
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read body: %s", err)
	}
	var body map[string]any
	if err := common.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body is not valid json: %s", err)
	}

	if _, leaked := body["mask"]; leaked {
		t.Errorf("raw mask key leaked into the facade payload: %s", string(raw))
	}
	// 反向确认蒙版没有因为剥离而丢失 —— 它应该以 input_refs.image_mask 的形式在。
	refs, ok := body["input_refs"].(map[string]any)
	if !ok {
		t.Fatalf("input_refs missing, mask would be lost entirely: %s", string(raw))
	}
	if _, ok := refs["image_mask"]; !ok {
		t.Errorf("input_refs.image_mask missing; the mask was dropped instead of materialized: %+v", refs)
	}
}

// setNFSRoot 把物化根指向可写目录，测试结束后还原。
func setNFSRoot(t *testing.T, dir string) {
	t.Helper()
	s := system_setting.GetMediaStorageSettings()
	old := s.NFSOutputRoot
	s.NFSOutputRoot = dir
	t.Cleanup(func() { s.NFSOutputRoot = old })
}
