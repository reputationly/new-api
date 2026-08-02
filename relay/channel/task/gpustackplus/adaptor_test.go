package gpustackplus

import "testing"

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
