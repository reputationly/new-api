package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 看板四个数据接口共用同一个时间窗上限：弹窗是一份，接口按角色分成管理端和用户端，
// 上限一旦不一致，就会出现「弹窗能选、提交后报错」。
func TestDataDashboardRangeError(t *testing.T) {
	const day = int64(86400)
	const start = int64(1790000000)

	cases := []struct {
		name  string
		start int64
		end   int64
		want  string
	}{
		{"正好 90 天放行", start, start + 90*day, ""},
		{"89 天放行", start, start + 89*day, ""},
		{"超过 90 天 1 秒拒绝", start, start + 90*day + 1, "时间跨度不能超过 90 天"},
		{"缺少起始时间不在这里判定", 0, start, ""},
		{"结束时间为 0 不在这里判定", start, 0, ""},
	}
	for _, c := range cases {
		require.Equal(t, c.want, dataDashboardRangeError(c.start, c.end), c.name)
	}
}
