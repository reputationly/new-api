package gpustackplus

import (
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/model"
)

func TestParseTaskResultProgress(t *testing.T) {
	cases := []struct {
		name         string
		body         string
		wantStatus   string
		wantProgress string
	}{
		{
			// 老版本门面不返回 progress/phase:必须退回固定档位（由 task_polling
			// 的 ProgressInProgress 写 30%），而不是把零值当成"进度 0"报出去。
			name:         "无 progress 字段时不覆盖",
			body:         `{"task_id":"t1","status":"running"}`,
			wantStatus:   model.TaskStatusInProgress,
			wantProgress: "",
		},
		{
			name:         "progress 为 0 同样不覆盖",
			body:         `{"task_id":"t1","status":"running","progress":0,"phase":"prepare"}`,
			wantStatus:   model.TaskStatusInProgress,
			wantProgress: "",
		},
		{
			// 门面 denoise 50% => 全局 46 => 30 + 65*0.46 = 59.9
			name:         "去噪中段折进 30-95 区间",
			body:         `{"task_id":"t1","status":"running","progress":46.0,"phase":"denoise"}`,
			wantStatus:   model.TaskStatusInProgress,
			wantProgress: "59%",
		},
		{
			// 门面的运行中上限 99 也必须落在 95% 以内，给落 OBS 的尾巴留空间。
			name:         "运行中上限不触及 100%",
			body:         `{"task_id":"t1","status":"running","progress":99.0,"phase":"postprocess"}`,
			wantStatus:   model.TaskStatusInProgress,
			wantProgress: "94%",
		},
		{
			// 终态由状态机写 100%，adaptor 不该在这里塞进度。
			name:         "完成态不带 progress",
			body:         `{"task_id":"t1","status":"done","nfs_path":"/nfs-output/a.mp4","progress":100}`,
			wantStatus:   model.TaskStatusSuccess,
			wantProgress: "",
		},
		{
			name:         "排队中不带 progress",
			body:         `{"task_id":"t1","status":"queued"}`,
			wantStatus:   model.TaskStatusQueued,
			wantProgress: "",
		},
	}
	a := &TaskAdaptor{}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ti, err := a.ParseTaskResult([]byte(c.body))
			if err != nil {
				t.Fatalf("ParseTaskResult 报错: %v", err)
			}
			if ti.Status != c.wantStatus {
				t.Errorf("status got = %q, want = %q", ti.Status, c.wantStatus)
			}
			if ti.Progress != c.wantProgress {
				t.Errorf("progress got = %q, want = %q", ti.Progress, c.wantProgress)
			}
		})
	}
}

func TestScaleProgressIsMonotonicAndBounded(t *testing.T) {
	prev := -1.0
	for g := 0.0; g <= 100.0; g += 0.5 {
		got := scaleProgress(g)
		n, err := strconv.ParseFloat(strings.TrimSuffix(got, "%"), 64)
		if err != nil {
			t.Fatalf("scaleProgress(%v) = %q 无法解析: %v", g, got, err)
		}
		if n < progressInProgressFloor || n > progressInProgressCeil {
			t.Fatalf("scaleProgress(%v) = %q 越界 [%v,%v]", g, got, progressInProgressFloor, progressInProgressCeil)
		}
		if n < prev {
			t.Fatalf("scaleProgress 非单调: %v -> %q，前值 %v", g, got, prev)
		}
		prev = n
	}
	// 越界输入被夹紧，不产生非法百分比。
	if got := scaleProgress(-10); got != "30%" {
		t.Errorf("负值应夹到下界，got = %q", got)
	}
	if got := scaleProgress(1000); got != "95%" {
		t.Errorf("超限应夹到上界，got = %q", got)
	}
}

func TestWithFoleySuppression(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			// 空提示词是最需要补的情形:v2a 收到空 prompt 时最容易配出无关背景音乐。
			name: "空提示词补成纯抑制句",
			in:   "",
			want: foleySuppression,
		},
		{
			name: "空白字符等同于空",
			in:   "   \n\t ",
			want: foleySuppression,
		},
		{
			name: "中文句号收掉后接英文句点",
			in:   "一位老人在林间小路上散步。",
			want: "一位老人在林间小路上散步. " + foleySuppression,
		},
		{
			name: "无句末标点直接接",
			in:   "waves crashing on rocks",
			want: "waves crashing on rocks. " + foleySuppression,
		},
		{
			name: "英文句点不重复",
			in:   "A door creaks open.",
			want: "A door creaks open. " + foleySuppression,
		},
		{
			// 幂等:网关可能对同一请求整形多次(重试),不能越接越长。
			name: "已含抑制句时原样返回",
			in:   "Two men are playing tennis. " + foleySuppression,
			want: "Two men are playing tennis. " + foleySuppression,
		},
		{
			name: "大小写不同也算已含",
			in:   "footsteps. no music is present.",
			want: "footsteps. no music is present.",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := withFoleySuppression(c.in); got != c.want {
				t.Errorf("withFoleySuppression(%q)\n got = %q\nwant = %q", c.in, got, c.want)
			}
		})
	}
}

func TestWithFoleySuppressionIsIdempotent(t *testing.T) {
	for _, in := range []string{"", "小狗在木地板上跑动。", "glass shatters"} {
		once := withFoleySuppression(in)
		if twice := withFoleySuppression(once); twice != once {
			t.Errorf("非幂等 in=%q\n once = %q\ntwice = %q", in, once, twice)
		}
	}
}

func TestHasKeyFold(t *testing.T) {
	body := map[string]any{"Negative_Prompt": "x", "seed": 1}
	if !hasKeyFold(body, "negative_prompt") {
		t.Error("应忽略大小写命中 Negative_Prompt")
	}
	if hasKeyFold(body, "prompt") {
		t.Error("prompt 不该被 Negative_Prompt 误命中")
	}
	if !hasKeyFold(map[string]any{"  negative_prompt  ": "x"}, "negative_prompt") {
		t.Error("应忽略键名首尾空白")
	}
}
