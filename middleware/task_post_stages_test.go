package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

// 声明式后置段（metadata.post_stages）的接单层。
//
// 这一层同时承担三件事，任何一件做错都是**静默**的：
//   1. 校验不合法就 400（否则后台推进时才发现，只能降级交付）；
//   2. 把键从请求体里剥掉（漏摘不是报错，是发给引擎的多余字段）；
//   3. 挂到 context 供落库（漏了整条流水线不生效，用户以为买了 2K）。

func setupPostStagesTest(t *testing.T, enabled bool, pipelineModels string, group []string) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	origSwitch := common.PlaygroundPipelineServerSide
	origGroup := groupEnabledModels
	t.Cleanup(func() {
		common.PlaygroundPipelineServerSide = origSwitch
		groupEnabledModels = origGroup
	})
	common.PlaygroundPipelineServerSide = enabled
	groupEnabledModels = func(string) []string { return group }

	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil { // 本包没有 TestMain 初始化 OptionMap
		common.OptionMap = map[string]string{}
	}
	origCfg, hadCfg := common.OptionMap["VideoModelConfig"]
	common.OptionMap["VideoModelConfig"] = pipelineModels
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if hadCfg {
			common.OptionMap["VideoModelConfig"] = origCfg
		} else {
			delete(common.OptionMap, "VideoModelConfig")
		}
		common.OptionMapRWMutex.Unlock()
	})
}

// readBody 取当前（可能已被改写过的）请求体字节。
func readBody(t *testing.T, c *gin.Context) string {
	t.Helper()
	storage, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	raw, err := storage.Bytes()
	require.NoError(t, err)
	return string(raw)
}

func runPostStages(t *testing.T, body string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
	return c, w
}

func TestTaskPostStagesDisabledIsNoop(t *testing.T) {
	setupPostStagesTest(t, false, `{"models":{"h3":{"pipeline":true}}}`, []string{"relayvsr"})
	c, w := runPostStages(t, `{"model":"h3","metadata":{"task_type":"t2v","post_stages":[{"kind":"sr","model":"relayvsr"}]}}`)

	TaskPostStages()(c)

	require.Empty(t, PostStagesFromContext(c), "开关关着时不该接管")
	require.Equal(t, http.StatusOK, w.Code)
	// 关掉时**仍然要剥掉这个键**：它是发给引擎的未知字段，漏摘是静默的。
	// 开关只决定"服务端要不要接管"，不决定"要不要把脏数据递给上游"。
	require.NotContains(t, readBody(t, c), "post_stages")
}

func TestTaskPostStagesStripsAndPublishes(t *testing.T) {
	setupPostStagesTest(t, true, `{"models":{"h3":{"pipeline":true}}}`, []string{"relayvsr"})
	c, w := runPostStages(t, `{
		"model":"h3",
		"prompt":"a cat",
		"metadata":{"task_type":"t2v","seed":9007199254740993,
			"post_stages":[{"kind":"sr","model":"relayvsr","sr_ratio":2,"target_short_edge":1440}]}
	}`)

	TaskPostStages()(c)

	require.Equal(t, http.StatusOK, w.Code)
	stages := PostStagesFromContext(c)
	require.Len(t, stages, 1)
	require.Equal(t, "relayvsr", stages[0].Model)

	got := readBody(t, c)
	require.NotContains(t, got, "post_stages", "必须从发给引擎的请求里剥掉")
	require.Contains(t, got, "a cat", "其余字段要原样保留")
	// 大整数不能被 float64 改写（Seed 一类的值超过 2^53）。
	require.Contains(t, got, "9007199254740993")
}

func TestTaskPostStagesRejectsThirdPartyGeneration(t *testing.T) {
	setupPostStagesTest(t, true, `{"models":{"doubao-video":{"pipeline":false}}}`, []string{"relayvsr"})
	c, w := runPostStages(t, `{"model":"doubao-video","metadata":{"post_stages":[{"kind":"sr","model":"relayvsr"}]}}`)

	TaskPostStages()(c)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "自建引擎")
}

func TestTaskPostStagesRejectsModelNotInGroup(t *testing.T) {
	setupPostStagesTest(t, true, `{"models":{"h3":{"pipeline":true}}}`, []string{"seedvr2"})
	c, w := runPostStages(t, `{"model":"h3","metadata":{"post_stages":[{"kind":"sr","model":"relayvsr"}]}}`)

	TaskPostStages()(c)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "不可用")
}

func TestTaskPostStagesRejectsBadSpec(t *testing.T) {
	setupPostStagesTest(t, true, `{"models":{"h3":{"pipeline":true}}}`, []string{"relayvsr"})
	c, w := runPostStages(t, `{"model":"h3","metadata":{"post_stages":[{"kind":"upscale","model":"relayvsr"}]}}`)

	TaskPostStages()(c)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.Contains(t, w.Body.String(), "kind 只能是")
}

// metadata 是**JSON 字符串**形态时同样要剥离 + 接管。
//
// TaskSubmitReq 的 UnmarshalJSON 两种形态都收（聚合展开的 expansionMetadata 也是
// 两种都认）。只处理 map 的话，字符串形态下 post_stages 既不会被剥离、也不会被校验：
// 声明静默失效（用户以为买了 2K），而那个键还会原样发给引擎。
func TestTaskPostStagesHandlesStringMetadata(t *testing.T) {
	setupPostStagesTest(t, true, `{"models":{"h3":{"pipeline":true}}}`, []string{"relayvsr"})
	c, w := runPostStages(t, `{
		"model":"h3",
		"prompt":"a cat",
		"metadata":"{\"task_type\":\"t2v\",\"post_stages\":[{\"kind\":\"sr\",\"model\":\"relayvsr\"}]}"
	}`)

	TaskPostStages()(c)

	require.Equal(t, http.StatusOK, w.Code)
	require.Len(t, PostStagesFromContext(c), 1, "字符串形态的 metadata 也要被接管")

	got := readBody(t, c)
	require.NotContains(t, got, "post_stages", "必须剥离")
	// 形态要保持：进来是字符串，出去还是字符串（换编码这种"语义不变"的差异最难查）。
	require.Contains(t, got, `\"task_type\":\"t2v\"`, "metadata 应仍是 JSON 字符串")
	require.Contains(t, got, "a cat")
}

// 不带这个键的普通请求（绝大多数）必须原样通过。
func TestTaskPostStagesIgnoresPlainRequests(t *testing.T) {
	setupPostStagesTest(t, true, `{"models":{"h3":{"pipeline":true}}}`, []string{"relayvsr"})
	c, w := runPostStages(t, `{"model":"h3","metadata":{"task_type":"t2v"}}`)

	TaskPostStages()(c)

	require.Equal(t, http.StatusOK, w.Code)
	require.Empty(t, PostStagesFromContext(c))
}
