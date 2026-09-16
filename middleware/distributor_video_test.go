package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// 只读 / 只操作本地任务表的视频端点不能去选渠道。
//
// 这条测试锁的是一个「路由注册成功 ≠ 请求到得了 handler」的坑：DELETE 没有请求体，
// getModelRequest 解析不出 model，而 shouldSelectChannel 只要是 true，Distribute 就会
// 停在「模型名不能为空」的 400 上 —— handler 一行都不会跑，而路由注册测试完全看不出来。
func TestGetModelRequestSkipsChannelSelectionForVideoReadPaths(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name   string
		method string
		path   string
	}{
		{"查询单个视频任务", http.MethodGet, "/v1/videos/task_abc"},
		{"取消视频任务", http.MethodDelete, "/v1/videos/task_abc"},
		{"体验区查询", http.MethodGet, "/pg/videos/task_abc"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(tc.method, tc.path, nil)

			_, shouldSelectChannel, err := getModelRequest(c)
			if err != nil {
				t.Fatalf("getModelRequest: %s", err)
			}
			if shouldSelectChannel {
				t.Errorf("%s %s 不该选渠道：它没有模型名，Distribute 会直接 400", tc.method, tc.path)
			}
		})
	}
}
