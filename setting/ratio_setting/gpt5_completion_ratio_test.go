package ratio_setting

import "testing"

func TestGPT5CompletionRatioMatrix(t *testing.T) {
	cases := []struct {
		name       string
		wantRatio  float64
		wantLocked bool
	}{
		// 无点的 gpt-5 系：维持 8/locked
		{"gpt-5", 8, true},
		{"gpt-5-mini", 8, true},
		{"gpt-5-codex", 8, true},
		// 5.1/5.2/5.3 仍在售：必须维持 8/locked（上游会误降到 6/unlocked）
		{"gpt-5.1", 8, true},
		{"gpt-5.1-codex", 8, true},
		{"gpt-5.2", 8, true},
		{"gpt-5.2-pro", 8, true},
		{"gpt-5.3-codex", 8, true},
		// 5.4 保持原有分档
		{"gpt-5.4", 6, true},
		{"gpt-5.4-nano", 6.25, true},
		// 5.5 起解锁，允许运营覆盖
		{"gpt-5.5", 6, false},
		{"gpt-5.6-sol", 6, false},
		{"gpt-5.6-terra", 6, false},
		{"gpt-5.6-luna", 6, false},
	}
	for _, tc := range cases {
		gotRatio, gotLocked := getHardcodedCompletionModelRatio(tc.name)
		if gotRatio != tc.wantRatio || gotLocked != tc.wantLocked {
			t.Errorf("%s: got (%v, %v), want (%v, %v)",
				tc.name, gotRatio, gotLocked, tc.wantRatio, tc.wantLocked)
		}
	}
}
