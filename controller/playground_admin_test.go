package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

// 「分类显示」区块靠 GET /api/playground_admin/options 回填开关状态。这个键漏在白名单
// 外时页面不会报错，只会静静回落到前端的 DEFAULT_ADMIN_CONFIG（图像/视频/语音/音乐
// 一律关），下一次保存再把库里的真实配置覆盖成默认值——所以必须钉住它被返回。
func TestGetPlaygroundAdminOptionsIncludesSidebarModules(t *testing.T) {
	gin.SetMode(gin.TestMode)

	const want = `{"chat":{"enabled":true,"image":true}}`
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	prev, had := common.OptionMap["SidebarModulesAdmin"]
	common.OptionMap["SidebarModulesAdmin"] = want
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if had {
			common.OptionMap["SidebarModulesAdmin"] = prev
		} else {
			delete(common.OptionMap, "SidebarModulesAdmin")
		}
		common.OptionMapRWMutex.Unlock()
	})

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/playground_admin/options", nil)
	GetPlaygroundAdminOptions(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "SidebarModulesAdmin") {
		t.Fatalf("响应里没有 SidebarModulesAdmin，「分类显示」的开关会回落到默认值：%s", body)
	}
	if !strings.Contains(body, `\"image\":true`) {
		t.Errorf("SidebarModulesAdmin 的值没原样带回：%s", body)
	}
}

// 保存走的是同一组 AdminAuth 接口，键不在白名单里会被 403 挡掉。
// 这里故意送一个非字符串的 value：它先过白名单、再撞上「配置值必须是字符串」的 400，
// 于是不碰 DB 也能区分「白名单放行(400)」与「白名单拒绝(403)」。
func TestUpdatePlaygroundAdminOptionAllowsSidebarModules(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		key  string
		want int
	}{
		{key: "SidebarModulesAdmin", want: http.StatusBadRequest},
		{key: "PlaygroundTabConfig", want: http.StatusBadRequest},
		{key: "SMTPToken", want: http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(
				http.MethodPut,
				"/api/playground_admin/option",
				strings.NewReader(`{"key":"`+tc.key+`","value":123}`),
			)
			c.Request.Header.Set("Content-Type", "application/json")
			UpdatePlaygroundAdminOption(c)

			if w.Code != tc.want {
				t.Errorf("key=%s status = %d, want %d（body=%s）", tc.key, w.Code, tc.want, w.Body.String())
			}
		})
	}
}
