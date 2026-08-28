package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
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

// TestCreateModelMetaResponseMatchesDB 接口响应必须与库里的实际状态一致。
//
// GORM 的 Create 回调在 INSERT **之前**就把零值字段改写成 `default` 标签的值
// （bool 的 false → true、int 的 0 → 1）。Model.Insert 用一次补偿 Update 修正了
// 库里的行，但结构体仍是被改写后的值——而 CreateModelMeta 会用 ApiSuccess 把这个
// 结构体直接序列化回客户端。
//
// 症状是「接口说已启用、库里是禁用」。管理页保存后会重新拉列表，所以 UI 显示正确，
// 这也正是它一直没被发现的原因——只测重查结果的用例同样看不见。
func TestCreateModelMetaResponseMatchesDB(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	body := `{
		"model_name": "zz-disabled-on-create",
		"name_rule": 0,
		"status": 0,
		"sync_official": 0
	}`
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/models/",
		bytes.NewBufferString(body))
	ctx.Request.Header.Set("Content-Type", "application/json")

	CreateModelMeta(ctx)

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Id           int `json:"id"`
			Status       int `json:"status"`
			SyncOfficial int `json:"sync_official"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &resp))
	require.True(t, resp.Success)

	var saved model.Model
	require.NoError(t, db.First(&saved, resp.Data.Id).Error)

	require.Equal(t, saved.Status, resp.Data.Status,
		"接口回的 status 与库里不一致——Create 回调改写了结构体而补偿 Update 只修了 DB")
	require.Equal(t, saved.SyncOfficial, resp.Data.SyncOfficial,
		"接口回的 sync_official 与库里不一致")
	require.Equal(t, 0, saved.Status, "新建时选择「禁用」必须被保留")
}
