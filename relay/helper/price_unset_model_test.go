package helper

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 用户级「接受未设置价格模型」开关已删除：存量 JSON 里的 true 不再生效，
// 未定价模型对所有人一律拒绝。站点级自用模式不受影响（它有单独的分支）。
func TestUnsetModelRejectedDespiteLegacyUserSetting(t *testing.T) {
	gin.SetMode(gin.TestMode)

	original := operation_setting.SelfUseModeEnabled
	operation_setting.SelfUseModeEnabled = false
	t.Cleanup(func() { operation_setting.SelfUseModeEnabled = original })

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	ctx.Set("group", "default")

	// 存量 JSON 反序列化进 dto.UserSetting —— 新结构里已经没有这个字段，
	// 解析时被忽略，所以这个请求拿不到任何特权。
	var legacy dto.UserSetting
	require.NoError(t, common.UnmarshalJsonStr(`{"accept_unset_model_ratio_model":true}`, &legacy))

	// UserId 用 0 是为了避开错误文案里的管理员判定（需要数据库），
	// 本用例只关心「是否放行」。
	info := &relaycommon.RelayInfo{
		OriginModelName: "zz-unpriced-legacy-model",
		UserGroup:       "default",
		UsingGroup:      "default",
		UserSetting:     legacy,
	}

	_, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})
	require.Error(t, err, "未定价模型必须被拒绝")

	_, err = ModelPriceHelperPerCall(ctx, info)
	require.Error(t, err, "按次计费的入口同样必须拒绝")
}
