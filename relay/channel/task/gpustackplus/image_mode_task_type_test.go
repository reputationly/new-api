package gpustackplus

import (
	"strings"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// taskTypeFromImageMode:客户端按官方口径(mode)说话,门面按 task_type 干活。
//
// 这张表是"一个模型多种玩法"的收口点之一 —— 翻错一个值,请求会被发到另一个
// checkpoint 上,而报错来自引擎、看不出关联(见 aggregate_route_mode_test.go 里
// 那条真机故障)。所以每个取值都钉死。
func TestTaskTypeFromImageMode(t *testing.T) {
	cases := []struct {
		name    string
		mode    string
		images  []string
		single  string
		want    string
		wantErr string
	}{
		{name: "文生视频", mode: "text-to-video", want: "t2v"},
		{name: "文生视频别名", mode: "t2v", want: "t2v"},
		{name: "首尾帧两张图", mode: "first-last-frame", images: []string{"a.png", "b.png"}, want: "flf2v"},
		{name: "首尾帧一张图退化成首帧", mode: "first-last-frame", images: []string{"a.png"}, want: "i2v"},
		{name: "首尾帧零张图也给 i2v(由输入防呆报缺图)", mode: "first-last-frame", want: "i2v"},
		{name: "首帧走顶层 image 字段也算一张", mode: "first-last-frame", single: "a.png", want: "i2v"},
		{name: "参考生视频", mode: "reference", want: "r2va"},
		{name: "参考生视频别名 ref2va", mode: "ref2va", want: "r2va"},
		{name: "大小写与空白容错", mode: "  Reference  ", want: "r2va"},
		{name: "续写未上线要明说", mode: "video-extension", wantErr: "尚未上线"},
		{name: "未知取值要报错而不是猜", mode: "first-frame", wantErr: "不支持的 image_mode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := &relaycommon.TaskSubmitReq{Images: tc.images, Image: tc.single}
			got, err := taskTypeFromImageMode(tc.mode, req)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want 含 %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("taskType = %q, want %q", got, tc.want)
			}
		})
	}
}

// metadata.image_mode 必须与 metadata.task_type 走同一条解析链:
//   - 两者都给时以 task_type 为准(显式声明优先,不猜);
//   - 只给 image_mode 时它能决定 task_type,且校验阶段看到的是同一个答案。
func TestTaskTypeOfRequestHonorsImageMode(t *testing.T) {
	t.Run("只给 image_mode", func(t *testing.T) {
		req := &relaycommon.TaskSubmitReq{
			Metadata: map[string]interface{}{"image_mode": "reference"},
		}
		got, err := taskTypeOfRequest(req, "")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got != "r2va" {
			t.Fatalf("taskType = %q, want r2va", got)
		}
	})

	t.Run("task_type 优先于 image_mode", func(t *testing.T) {
		req := &relaycommon.TaskSubmitReq{
			Metadata: map[string]interface{}{
				"task_type":  "i2v",
				"image_mode": "reference",
			},
		}
		got, err := taskTypeOfRequest(req, "")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got != "i2v" {
			t.Fatalf("taskType = %q, want i2v(task_type 优先)", got)
		}
	})

	t.Run("首尾帧按张数分岔", func(t *testing.T) {
		req := &relaycommon.TaskSubmitReq{
			Images:   []string{"a.png", "b.png"},
			Metadata: map[string]interface{}{"image_mode": "first-last-frame"},
		}
		got, err := taskTypeOfRequest(req, "")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got != "flf2v" {
			t.Fatalf("taskType = %q, want flf2v", got)
		}
	})

	t.Run("空 image_mode 不参与判定", func(t *testing.T) {
		req := &relaycommon.TaskSubmitReq{
			Metadata: map[string]interface{}{"image_mode": "   "},
		}
		// inferName 给一个 H3 帧族名,走名字推断,不能被空 image_mode 影响。
		got, err := taskTypeOfRequest(req, "minimax-h3-fl2va")
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got == "" {
			t.Fatalf("空 image_mode 不该让解析失败")
		}
	})
}
