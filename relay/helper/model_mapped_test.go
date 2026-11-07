package helper

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newMappingContext(t *testing.T, mapping, alias string) *gin.Context {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyChannelModelMapping, mapping)
	common.SetContextKey(c, constant.ContextKeyChannelModelAlias, alias)
	return c
}

func newMappingInfo(originModelName string) *relaycommon.RelayInfo {
	// UpstreamModelName 挂在嵌入的 ChannelMeta 上，与生产初始化路径一致
	// （RelayInfo.InitChannelMeta 里会把它初始化为 OriginModelName）。
	return &relaycommon.RelayInfo{
		OriginModelName: originModelName,
		ChannelMeta: &relaycommon.ChannelMeta{
			UpstreamModelName: originModelName,
		},
	}
}

// 别名是这次改造里唯一能让"配一条对外名->内部名"就完成上游改写的路径：
// 渠道只配别名、不配 model_mapping 时，上游也必须收到内部名。
func TestModelMappedHelper_AliasOnly(t *testing.T) {
	c := newMappingContext(t, "", `{"my-gpt":"gpt-4o"}`)
	info := newMappingInfo("my-gpt")

	require.NoError(t, ModelMappedHelper(c, info, nil))
	// 发给上游的是内部名……
	require.Equal(t, "gpt-4o", info.UpstreamModelName)
	require.True(t, info.IsModelMapped)
	// ……而对外的名字仍然是客户端请求的那个，计费/日志/响应都靠它。
	require.Equal(t, "my-gpt", info.OriginModelName)
	require.Equal(t, "my-gpt", relaycommon.PublicModelName(c))
	require.Equal(t, "gpt-4o", relaycommon.UpstreamModelName(c))
}

// 别名之后还能继续走 model_mapping：内部名 -> 真正上游名（Azure 部署名之类）。
func TestModelMappedHelper_AliasThenMapping(t *testing.T) {
	c := newMappingContext(t, `{"gpt-4o":"gpt-4o-2024-08-06"}`, `{"my-gpt":"gpt-4o"}`)
	info := newMappingInfo("my-gpt")

	require.NoError(t, ModelMappedHelper(c, info, nil))
	require.Equal(t, "gpt-4o-2024-08-06", info.UpstreamModelName)
	require.Equal(t, "my-gpt", relaycommon.PublicModelName(c))
}

// 既有的纯 mapping 行为必须逐位不变（这是回归线）。
func TestModelMappedHelper_MappingOnlyUnchanged(t *testing.T) {
	c := newMappingContext(t, `{"my-gpt":"gpt-4o"}`, "")
	info := newMappingInfo("my-gpt")

	require.NoError(t, ModelMappedHelper(c, info, nil))
	require.Equal(t, "gpt-4o", info.UpstreamModelName)
	require.Equal(t, "my-gpt", relaycommon.PublicModelName(c))
}

func TestModelMappedHelper_NoConfigIsNoop(t *testing.T) {
	c := newMappingContext(t, "", "")
	info := newMappingInfo("gpt-4o")

	require.NoError(t, ModelMappedHelper(c, info, nil))
	require.Equal(t, "gpt-4o", info.UpstreamModelName)
	require.False(t, info.IsModelMapped)
	// 没改名就不设置对外名 —— 响应改写、响应头过滤都以此为开关，
	// 未使用该功能的渠道必须继续走零成本的原始路径。
	require.Empty(t, relaycommon.PublicModelName(c))
}

// 别名与 mapping 互相指回时仍然要能被环检测拦住，而不是把请求挂死。
func TestModelMappedHelper_CycleAcrossAliasAndMapping(t *testing.T) {
	c := newMappingContext(t, `{"gpt-4o":"my-gpt"}`, `{"my-gpt":"gpt-4o"}`)
	info := newMappingInfo("my-gpt")

	err := ModelMappedHelper(c, info, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cycle")
}

// 别名配置损坏时不能猜：与 model_mapping 一致，直接报错。
func TestModelMappedHelper_BrokenAliasJSON(t *testing.T) {
	c := newMappingContext(t, "", `{"my-gpt":`)
	info := newMappingInfo("my-gpt")

	err := ModelMappedHelper(c, info, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "model_alias")
}

// 端到端（到写出口）：改名之后，流式 chunk 里不再出现上游模型名。
//
// 这条测试锁的是"两个改动点接上了"：把对外名放进 context（ModelMappedHelper 的职责）
// 与在写出口改写（StringData 的职责）。任何一边被重构掉，这里都会红。
func TestStringDataRewritesMappedModelName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	common.SetContextKey(c, constant.ContextKeyPublicModelName, "my-gpt-4o")
	common.SetContextKey(c, constant.ContextKeyUpstreamModelName, "gpt-4o")

	chunk := `{"id":"1","model":"gpt-4o","choices":[{"delta":{"content":"hi"}}]}`
	require.NoError(t, StringData(c, chunk))

	body := recorder.Body.String()
	require.Contains(t, body, `"model":"my-gpt-4o"`)
	require.NotContains(t, body, `"model":"gpt-4o"`)
}
