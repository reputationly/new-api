package common

import "testing"

// 像素串 → 最近的 H3 具名宽高比。非具名的约分结果(26:15 之类)不能直接下发,必须落到具名值上。
func TestNearestH3AspectRatio(t *testing.T) {
	cases := []struct {
		name string
		w, h int
		want string
	}{
		{"竖版 1080x1920", 1080, 1920, "9:16"},
		{"横版 1920x1080", 1920, 1080, "16:9"},
		{"480P 16:9 的 832x480 不能约成 26:15", 832, 480, "16:9"},
		{"768P 16:9 的 1344x768 实际画幅 1.75", 1344, 768, "16:9"},
		{"方形", 1024, 1024, "1:1"},
		{"接近方形但偏竖", 1080, 1200, "1:1"},
		{"竖版 3:4", 1000, 1500, "3:4"},
		{"超宽 21:9", 2560, 1080, "21:9"},
		{"非法宽度", 0, 100, ""},
		{"非法高度", 100, -1, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NearestH3AspectRatio(tc.w, tc.h); got != tc.want {
				t.Fatalf("NearestH3AspectRatio(%d, %d) = %q, want %q", tc.w, tc.h, got, tc.want)
			}
		})
	}
}
