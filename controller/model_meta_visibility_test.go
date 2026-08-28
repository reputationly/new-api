package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// putModelMeta 走真实 handler 提交一次模型更新。
func putModelMeta(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPut, "/api/models/",
		bytes.NewBufferString(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	UpdateModelMeta(ctx)
	return recorder
}

// TestUpdateModelMetaPersistsVisibleGroups 端到端钉住 visible_groups 的写入链路：
// JSON 绑定 -> Model.Update 的 Select 白名单 -> DB。
//
// 这条链最危险的地方是**全程不报错**：字段漏进白名单时接口照样返回 200、页面显示
// 已保存、刷新后配置消失。单测里的 modelUpdateSelectFields 断言只能保证白名单里
// 有这个名字，保证不了 json tag 拼对、绑定没被 statusOnly 分支吞掉。
func TestUpdateModelMetaPersistsVisibleGroups(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	original := model.Model{
		ModelName: "zz-meta-vis-model",
		NameRule:  model.NameRuleExact,
		Status:    1,
	}
	require.NoError(t, db.Create(&original).Error)

	t.Run("写入可见档位", func(t *testing.T) {
		recorder := putModelMeta(t, `{
			"id": `+strconv.Itoa(original.Id)+`,
			"model_name": "zz-meta-vis-model",
			"name_rule": 0,
			"status": 1,
			"visible_groups": "vip,geostar"
		}`)
		require.Contains(t, recorder.Body.String(), `"success":true`)

		var got model.Model
		require.NoError(t, db.First(&got, original.Id).Error)
		require.Equal(t, "vip,geostar", got.VisibleGroups)
	})

	t.Run("清空可见档位 = 放开权限", func(t *testing.T) {
		recorder := putModelMeta(t, `{
			"id": `+strconv.Itoa(original.Id)+`,
			"model_name": "zz-meta-vis-model",
			"name_rule": 0,
			"status": 1,
			"visible_groups": ""
		}`)
		require.Contains(t, recorder.Body.String(), `"success":true`)

		var got model.Model
		require.NoError(t, db.First(&got, original.Id).Error)
		require.Equal(t, "", got.VisibleGroups,
			"清空是正常操作（放开权限），非零字段更新做不到——Update 必须用 Select 强制写零值")
	})
}
