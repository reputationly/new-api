package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
)

// decodeDryRun 取出干跑校验响应里的模型名顺序。
func decodeDryRun(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Passed  bool `json:"passed"`
			Results []struct {
				Name string `json:"name"`
			} `json:"results"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
	require.True(t, resp.Success)
	names := make([]string, 0, len(resp.Data.Results))
	for _, r := range resp.Data.Results {
		names = append(names, r.Name)
	}
	return names
}

func dryRunRequest(t *testing.T, body string, chunked bool) *httptest.ResponseRecorder {
	t.Helper()
	// 干跑校验会读定价缓存(GetPricing → abilities),没有 DB 会空指针 panic。
	// 表是空的,于是所有模型都"查不到",正是 pricingLoaded()=false 的降级路径 ——
	// 本文件断言的是 body 处理与输出顺序,不依赖具体校验结论。
	setupModelListControllerTestDB(t)
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	req := httptest.NewRequest(http.MethodPost, "/api/option/aggregate_model_dry_run",
		io.NopCloser(strings.NewReader(body)))
	if chunked {
		// 复刻 chunked transfer-encoding:Go 在这种请求上把 ContentLength 置为 -1。
		req.ContentLength = -1
		req.TransferEncoding = []string{"chunked"}
	}
	ctx.Request = req
	AggregateModelDryRun(ctx)
	return rec
}

// chunked 请求下 ContentLength 是 -1。按它判断会跳过 body 解析、转而校验**已保存的**
// 配置,并返回一个看起来正常的 200 —— 运营以为校验的是编辑器里那份未保存的配置。
// 这正是本接口要消除的「不报错、给错答案」。
func TestDryRunParsesChunkedBody(t *testing.T) {
	withAggregateModelConfig(t, `[{"name":"saved-one","type":"video","enabled":true}]`)

	rec := dryRunRequest(t, `{"models":[{"name":"submitted-one","type":"video","generate":{"model":"g"}}]}`, true)

	require.Equal(t, http.StatusOK, rec.Code)
	names := decodeDryRun(t, rec)
	require.Equal(t, []string{"submitted-one"}, names,
		"chunked body 必须被解析,而不是静默回落到已保存的配置")
}

// 「把最后一条配置删掉后点校验」:提交的是空列表,应当如实校验空列表(返回零条结果),
// 而不是回落去校验旧配置、报一堆用户刚刚删掉的问题。
func TestDryRunEmptyListIsNotFallback(t *testing.T) {
	withAggregateModelConfig(t, `[{"name":"saved-one","type":"video","enabled":true}]`)

	rec := dryRunRequest(t, `{"models":[]}`, false)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Empty(t, decodeDryRun(t, rec),
		"显式提交空列表不该回落到已保存配置")
}

// 完全不带 body 时才回落到已保存配置,且顺序必须稳定 ——
// GetAggregateModels 返回 map,不排序的话配置页列表每次点校验都在重排。
func TestDryRunFallsBackSortedWhenNoBody(t *testing.T) {
	withAggregateModelConfig(t, `[
		{"name":"zeta","type":"video","enabled":true},
		{"name":"alpha","type":"video","enabled":true},
		{"name":"mid","type":"video","enabled":true}
	]`)

	for i := 0; i < 5; i++ {
		rec := dryRunRequest(t, ``, false)
		require.Equal(t, http.StatusOK, rec.Code)
		require.Equal(t, []string{"alpha", "mid", "zeta"}, decodeDryRun(t, rec),
			"无 body 时应回落到已保存配置并按模型名稳定排序")
	}
}

// decodeDryRunFull 取出完整响应(含 passed / message),供假绿灯相关断言使用。
func decodeDryRunFull(t *testing.T, rec *httptest.ResponseRecorder) (passed bool, msg string, n int) {
	t.Helper()
	var resp struct {
		Data struct {
			Passed  bool             `json:"passed"`
			Message string           `json:"message"`
			Results []map[string]any `json:"results"`
		} `json:"data"`
	}
	require.NoError(t, common.Unmarshal(rec.Body.Bytes(), &resp))
	return resp.Data.Passed, resp.Data.Message, len(resp.Data.Results)
}

// 已保存的配置**存坏了**时,校验必须报出来。
//
// 这是本接口最讽刺的一种失效:它存在的全部理由就是消除"不报错、给错答案",而运行时视图
// (GetAggregateModels)在配置坏掉时返回空表,于是零条结果 → allPassed 恒真 → 绿灯。
// 运营看到"通过",而全部聚合模型其实一个都没生效,唯一线索是服务端日志里一条 SysError。
func TestDryRunReportsUnparsableSavedConfig(t *testing.T) {
	withAggregateModelConfig(t, `[{"name":"a","enabled":true},{"name":]`)

	passed, msg, n := decodeDryRunFull(t, dryRunRequest(t, ``, false))

	require.False(t, passed, "存坏了的配置不该报通过")
	require.Zero(t, n)
	require.Contains(t, msg, "无法解析")
}

// 零条结果必须说清是"没什么可校验的",不能让调用方把空结果读成"全部通过"。
func TestDryRunEmptyConfigSaysSo(t *testing.T) {
	withAggregateModelConfig(t, ``)

	_, msg, n := decodeDryRunFull(t, dryRunRequest(t, ``, false))

	require.Zero(t, n)
	require.Contains(t, msg, "没有配置任何聚合模型")
}

// 回落路径必须也校验 enabled:false 的条目 —— 提交 body 的那条路会校验它们,
// 两条路径口径不一致的话,停用中的坏配置会一直藏着,直到有人把它启用。
func TestDryRunFallbackIncludesDisabledEntries(t *testing.T) {
	withAggregateModelConfig(t, `[
		{"name":"on-one","type":"video","enabled":true,"generate":{"model":"g"}},
		{"name":"off-one","type":"video","enabled":false,"generate":{"model":"g"}}
	]`)

	names := decodeDryRun(t, dryRunRequest(t, ``, false))

	require.Equal(t, []string{"off-one", "on-one"}, names,
		"停用的条目也应出现在校验结果里")
}

// 非法 JSON 必须 400,不能静默当成"没提交"而去校验已保存配置。
func TestDryRunRejectsMalformedBody(t *testing.T) {
	withAggregateModelConfig(t, `[{"name":"saved-one","type":"video","enabled":true}]`)

	rec := dryRunRequest(t, `{ not json`, false)

	require.Equal(t, http.StatusBadRequest, rec.Code)
}

// 已保存的配置里有 null 条目时,干跑校验不能 panic 成 500。
//
// `[null, {...}]` 是合法 JSON,ParseAggregateModelList 只负责解析、不做语义过滤,
// 于是列表里真的会出现 nil。回落路径要对它排序,解引用 Name 就崩 —— 而这条路径的
// 职责恰恰是「把配置的问题报出来」,自己崩掉是最糟的失败方式。
func TestDryRunHandlesNullEntriesInSavedConfig(t *testing.T) {
	withAggregateModelConfig(t, `[null,{"name":"ok-one","type":"video","enabled":true,"generate":{"model":"g"}},null]`)

	rec := dryRunRequest(t, ``, false)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, []string{"ok-one"}, decodeDryRun(t, rec),
		"null 条目应被跳过,其余照常校验")
}
