package billingexpr

import "testing"

func TestUsesTimeFunctions(t *testing.T) {
	cases := []struct {
		expr string
		want bool
	}{
		{"", false},
		{`v2:tier("base", p * 2.5 + c * 15)`, false},
		{`v2:tier("base", p * 2 + c * 8 + cr * 0.3)`, false},
		{`v2:tier("base", hour("Asia/Shanghai") >= 9 ? p * 5 : p * 2)`, true},
		{`v2:tier("base", weekday("Asia/Shanghai") >= 1 ? p * 2 : p * 3)`, true},
		// 拼接格式：时间函数在第二段里，整串编译会失败，必须逐段判
		{`v2:tier("base", p * 2)|||hour("Asia/Shanghai") >= 9 ? 2 : 1`, true},
		// tier/max/min 不是时间函数，别误判
		{`v2:tier("x", max(p, 100) * 2)`, false},
	}
	for _, c := range cases {
		if got := UsesTimeFunctions(c.expr); got != c.want {
			t.Errorf("UsesTimeFunctions(%q) = %v, want %v", c.expr, got, c.want)
		}
	}
}
