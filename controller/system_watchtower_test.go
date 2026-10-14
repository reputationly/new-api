package controller

import "testing"

// TestWatchtowerConfigured 钉住 /api/status 下发给前端的那个判据。
//
// 这个判据错了不会有任何报错：页面上「立即安装」按钮直接消失，
// 而且 /api/status 里那个字段本身就是唯一线索。所以用例要写清楚
// 每个取值组合**为什么**是那个期望值，尤其是 compose 默认值那一行。
func TestWatchtowerConfigured(t *testing.T) {
	cases := []struct {
		name  string
		token string
		url   string
		want  bool
	}{
		{
			// 回归用例：docker-compose.yml 给 new-api 和 watchtower 两个服务
			// 用的是同一个 `${WATCHTOWER_API_TOKEN:-CHANGE_ME_TO_RANDOM_TOKEN}`，
			// 所以未覆盖时两边拿到的都是这个串——它是**两边一致、开箱即用**的
			// 默认凭证，不是"未配置"的哨兵值。曾经把占位值判成未配置，
			// 于是 BMS/compose 部署上这个本来可用的按钮被藏掉了。
			name:  "compose 默认占位串 + compose 写死的 URL = 可用",
			token: "CHANGE_ME_TO_RANDOM_TOKEN",
			url:   "http://watchtower:8080",
			want:  true,
		},
		{
			name:  "真实 token 覆盖后的 compose 部署 = 可用",
			token: "7f3c1e9a5b204d68",
			url:   "http://watchtower:8080",
			want:  true,
		},
		{
			// 迁 CCE 时两个变量都要整行删掉。只删一半是最容易犯的错，
			// 这里要求它同样判为不可用，避免前端显示一个点了必然报错的按钮。
			name:  "只删了 URL、token 忘了删 = 不可用",
			token: "7f3c1e9a5b204d68",
			url:   "",
			want:  false,
		},
		{
			name:  "只删了 token、URL 忘了删 = 不可用",
			token: "",
			url:   "http://watchtower:8080",
			want:  false,
		},
		{
			name:  "两个都删掉（CCE 目标态）= 不可用",
			token: "",
			url:   "",
			want:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WATCHTOWER_API_TOKEN", tc.token)
			t.Setenv("WATCHTOWER_API_URL", tc.url)

			if got := watchtowerConfigured(); got != tc.want {
				t.Fatalf("watchtowerConfigured() = %v, want %v (token=%q url=%q)",
					got, tc.want, tc.token, tc.url)
			}
		})
	}
}
