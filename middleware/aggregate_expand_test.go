package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
)

func withAggregateConfig(t *testing.T, raw string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	orig, had := common.OptionMap["AggregateModelConfig"]
	common.OptionMap["AggregateModelConfig"] = raw
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if had {
			common.OptionMap["AggregateModelConfig"] = orig
		} else {
			delete(common.OptionMap, "AggregateModelConfig")
		}
		common.OptionMapRWMutex.Unlock()
		common.GetAggregateModels()
	})
}

const aggConfig = `[{
	"name":"h3-2k","type":"video","enabled":true,
	"generate":{"model":"minimax-h3"},
	"upscale":{"model":"seedvr2","target_size":"2k"}
}]`

// 聚合模型名要能展开成生成段的真实模型 —— 它自己没有 ability,不展开就选不到任何渠道。
func TestExpandAggregateModel(t *testing.T) {
	withAggregateConfig(t, aggConfig)

	got, err := expandAggregateModel("h3-2k", "default")
	require.NoError(t, err)
	require.Equal(t, "minimax-h3", got)
}

// 非聚合模型原样放行:返回空串表示"这不是聚合模型",调用方不得改写任何东西。
func TestExpandLeavesNormalModelUntouched(t *testing.T) {
	withAggregateConfig(t, aggConfig)

	got, err := expandAggregateModel("gpt-4o", "default")
	require.NoError(t, err)
	require.Empty(t, got)
}

// 停用的聚合模型不可调用(GetAggregateModel 只返回 enabled 的)。
func TestExpandIgnoresDisabledAggregate(t *testing.T) {
	withAggregateConfig(t, `[{"name":"off","type":"video","enabled":false,"generate":{"model":"x"}}]`)

	got, err := expandAggregateModel("off", "default")
	require.NoError(t, err)
	require.Empty(t, got, "停用的聚合模型不该被展开")
}

// 配了 groups 就是显式白名单:不在名单里的分组按「模型不存在」拒绝,
// 与可见性拦截同口径 —— 不能泄露这个定向能力的存在。
func TestExpandEnforcesGroupWhitelist(t *testing.T) {
	withAggregateConfig(t, `[{
		"name":"vip-only","type":"video","enabled":true,
		"groups":["vip"],
		"generate":{"model":"minimax-h3"}
	}]`)

	got, err := expandAggregateModel("vip-only", "vip")
	require.NoError(t, err)
	require.Equal(t, "minimax-h3", got)

	_, err = expandAggregateModel("vip-only", "default")
	require.Error(t, err, "不在白名单的分组应被拒绝")
}

// 未配 groups = 不额外限制:约束交给展开后的生成段模型(选渠道那步会拒),
// 而不是在这里凭空拒掉。两处语义必须与 controller/token.go 的同名判定一致。
func TestExpandWithoutGroupsAllowsAny(t *testing.T) {
	withAggregateConfig(t, aggConfig)

	for _, g := range []string{"default", "vip", "whatever"} {
		got, err := expandAggregateModel("h3-2k", g)
		require.NoError(t, err)
		require.Equal(t, "minimax-h3", got, "未配 groups 时不该按分组拒绝")
	}
}

// 缺生成段模型的配置要报错,不能展开成空模型名后一路走到选渠道才报"模型名为空"。
func TestExpandRejectsMissingGenerateModel(t *testing.T) {
	withAggregateConfig(t, `[{"name":"broken","type":"video","enabled":true}]`)

	_, err := expandAggregateModel("broken", "default")
	require.Error(t, err)
}

// body 里的 model 必须一并改写:适配器构造上游请求时读的是 body,
// 只改内存里那份会把聚合模型名原样发给上游,上游根本不认识它。
func TestApplyExpansionRewritesBody(t *testing.T) {
	withAggregateConfig(t, aggConfig)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos",
		strings.NewReader(`{"model":"h3-2k","prompt":"a cat","size":"2k"}`))

	agg := common.GetAggregateModel("h3-2k")
	require.NoError(t, applyAggregateExpansion(c, "h3-2k", "minimax-h3", agg))

	body := readBodyMap(t, c)
	require.Equal(t, "minimax-h3", body["model"], "body 里的 model 必须改写成生成段模型")
	// 其余字段不得丢失 —— 改写是外科手术,不是重建请求。
	require.Equal(t, "a cat", body["prompt"])
	require.Equal(t, "2k", body["size"])

	// 展开结果要留住"客户以为自己在调什么",否则排障时无从知道这是一次聚合调用。
	exp := GetAggregateExpansion(c)
	require.NotNil(t, exp)
	require.Equal(t, "h3-2k", exp.PublicName)
	require.NotNil(t, exp.Config)
	require.Equal(t, "seedvr2", exp.Config.Upscale.Model)
}

// Overrides 是聚合模型的要害:客户传的是「最终尺寸」,生成段必须收到「中间尺寸」。
// 不应用它的话,客户传 size=2k 会原样打到 H3 上 —— 正是那个会 OOM/被钳位的请求,
// 也就是这个功能要解决的问题本身。
func TestApplyExpansionAppliesOverrides(t *testing.T) {
	withAggregateConfig(t, `[{
		"name":"h3-2k","type":"video","enabled":true,
		"generate":{"model":"minimax-h3","overrides":{"size":"1280x720"}},
		"upscale":{"model":"seedvr2","target_size":"2k"}
	}]`)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos",
		strings.NewReader(`{"model":"h3-2k","prompt":"a cat","size":"2k"}`))

	require.NoError(t, applyAggregateExpansion(c, "h3-2k", "minimax-h3", common.GetAggregateModel("h3-2k")))

	body := readBodyMap(t, c)
	require.Equal(t, "1280x720", body["size"],
		"客户要的 2k 是最终尺寸,生成段必须收到被 overrides 改写后的中间尺寸")
	require.Equal(t, "minimax-h3", body["model"])
	require.Equal(t, "a cat", body["prompt"], "未被覆盖的字段应原样保留")
}

// overrides 里手滑写了 model 不该顶掉展开结果 —— 那会让请求发去一个既非聚合模型、
// 也非配置的生成段模型的地方,而且不报错。
func TestApplyExpansionOverridesCannotHijackModel(t *testing.T) {
	withAggregateConfig(t, `[{
		"name":"h3-2k","type":"video","enabled":true,
		"generate":{"model":"minimax-h3","overrides":{"model":"someone-else"}}
	}]`)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos",
		strings.NewReader(`{"model":"h3-2k","prompt":"x"}`))

	require.NoError(t, applyAggregateExpansion(c, "h3-2k", "minimax-h3", common.GetAggregateModel("h3-2k")))

	require.Equal(t, "minimax-h3", readBodyMap(t, c)["model"],
		"overrides 不得覆盖展开决定的 model")
}

// readBodyMap 直接从 body storage 取字节解析,不经 Content-Type 分派。
func readBodyMap(t *testing.T, c *gin.Context) map[string]any {
	t.Helper()
	storage, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	raw, err := storage.Bytes()
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, common.Unmarshal(raw, &m))
	return m
}

// 改写不能依赖 Content-Type。UnmarshalBodyReusable 对未知 Content-Type 会
// 「返回 nil 却什么都不填」,照它写会让改写在客户端不带该请求头时静默落空:
// 聚合模型名原样发给上游,上游报未知模型,而我们这边一切正常。
func TestApplyExpansionWorksWithoutContentType(t *testing.T) {
	withAggregateConfig(t, aggConfig)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos",
		strings.NewReader(`{"model":"h3-2k","prompt":"a cat"}`))
	c.Request.Header.Del("Content-Type")

	require.NoError(t, applyAggregateExpansion(c, "h3-2k", "minimax-h3", common.GetAggregateModel("h3-2k")))

	body := readBodyMap(t, c)
	require.Equal(t, "minimax-h3", body["model"])
	require.Equal(t, "a cat", body["prompt"], "其余字段不得丢失")
}

// 空请求体必须报错,不能补一个空 map 继续 —— 那样改写完只剩 {"model":...},
// prompt / 输入图全没了,请求却还会照常发给上游。
func TestApplyExpansionRejectsEmptyBody(t *testing.T) {
	withAggregateConfig(t, aggConfig)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(``))

	require.Error(t, applyAggregateExpansion(c, "h3-2k", "minimax-h3", common.GetAggregateModel("h3-2k")))
}

// JSON 字面量 null 解析成功但得到 nil map —— 这是唯一能走到 body==nil 分支的输入
// (空串会先被 Unmarshal 判成语法错误)。若在这里补一个空 map 继续,改写完就只剩
// {"model":...},prompt / 输入图全丢,而请求照常发给上游。
func TestApplyExpansionRejectsNullBody(t *testing.T) {
	withAggregateConfig(t, aggConfig)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`null`))

	err := applyAggregateExpansion(c, "h3-2k", "minimax-h3", common.GetAggregateModel("h3-2k"))
	require.Error(t, err, "body 为 null 时必须报错,不能补空 map 继续")
}

// 非聚合请求不该挂上展开结果。
func TestGetAggregateExpansionNilForNormalRequest(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	require.Nil(t, GetAggregateExpansion(c))
}

// 展开判定与令牌白名单判定必须同语义,否则会出现「存得进白名单却调不通」
// 或反过来的错位。这里把两边的分组判定放在一起比对。
func TestGroupAllowedMatchesTokenSideSemantics(t *testing.T) {
	noGroups := &common.AggregateModel{Name: "a"}
	vipOnly := &common.AggregateModel{Name: "b", Groups: []string{"vip"}}

	require.True(t, groupAllowedForAggregate(noGroups, "anything"),
		"未配 groups 应放行任意分组")
	require.True(t, groupAllowedForAggregate(vipOnly, "vip"))
	require.False(t, groupAllowedForAggregate(vipOnly, "default"))
	require.False(t, groupAllowedForAggregate(nil, "default"))
}
