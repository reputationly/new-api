package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 内部流水线令牌对"用户能看到的地方"必须完全隐形。
//
// 它由服务端在推进后置段时懒建（见 service.GetPipelineToken），只放行后置段模型。
// 三处查询各写各的 SQL，漏一处就是：
//   - 列表：用户看见一个自己没建过的令牌，改它的白名单会让超分段整段失败；
//   - 计数：分页总数比能翻到的东西多，最后一页永远翻不满；
//   - 搜索：能搜出来，但列表里没有 —— 看起来像数据库不一致。
//
// 另一个陷阱是 LIKE 通配符：`__pipeline__%` 里的 `_` 在 SQL 里匹配任意单字符，
// 不转义会把 `12pipeline34xyz` 这种**普通用户的令牌**一起藏起来。
const internalTokenName = "__pipeline__default"

// seedTokens 铺一组令牌。key 必须全局唯一（同包测试共用一个内存库），
// 用 tag 区分不同用力的数据。
func seedTokens(t *testing.T, tag string) {
	t.Helper()
	require.NoError(t, DB.AutoMigrate(&Token{}))
	rows := []*Token{
		{UserId: 9701, Key: "sk-visible-" + tag, Name: "生产环境", Status: 1},
		// 名字里含 pipeline 但**不是**内部令牌：LIKE 的 `_` 若不转义就会把它误伤。
		{UserId: 9701, Key: "sk-lookalike-" + tag, Name: "12pipeline34xyz", Status: 1},
		{UserId: 9701, Key: "sk-internal-" + tag, Name: internalTokenName, Status: 1},
		{UserId: 9702, Key: "sk-other-" + tag, Name: "别人的", Status: 1},
	}
	for _, r := range rows {
		require.NoError(t, DB.Create(r).Error)
	}
}

func TestPipelineTokenHiddenFromListAndCount(t *testing.T) {
	seedTokens(t, "list")

	list, err := GetAllUserTokens(9701, 0, 50)
	require.NoError(t, err)
	names := map[string]bool{}
	for _, tk := range list {
		names[tk.Name] = true
	}
	require.False(t, names[internalTokenName], "内部令牌不能出现在列表里")
	require.True(t, names["生产环境"])
	require.True(t, names["12pipeline34xyz"], "名字里含 pipeline 的普通令牌不能被误伤")

	total, err := CountUserTokens(9701)
	require.NoError(t, err)
	require.EqualValues(t, len(list), total, "计数必须与列表同口径，否则分页永远翻不满")
}

func TestPipelineTokenHiddenFromSearch(t *testing.T) {
	seedTokens(t, "search")

	tokens, total, err := SearchUserTokens(9701, "%pipeline%", "", 0, 20, nil)
	require.NoError(t, err)
	for _, tk := range tokens {
		require.NotEqual(t, internalTokenName, tk.Name, "内部令牌不能被搜出来")
	}
	require.EqualValues(t, len(tokens), total)

	// 搜普通令牌仍然要能搜到（转义没把正常名字误伤）。
	tokens, _, err = SearchUserTokens(9701, "%12pipeline34%", "", 0, 20, nil)
	require.NoError(t, err)
	found := false
	for _, tk := range tokens {
		if tk.Name == "12pipeline34xyz" {
			found = true
		}
	}
	require.True(t, found, "名字里含 pipeline 的普通令牌必须搜得到")
}

// 前缀常量只有一处定义：service.GetPipelineToken 建的名字与这里的过滤必须字面一致。
func TestPipelineTokenNamePrefixMatchesPattern(t *testing.T) {
	require.Equal(t, PipelineTokenNamePrefix, "__pipeline__")
	// 模式是前缀把 `_` 转义后的样子，最后接 `%`。
	require.Equal(t, "!_!_pipeline!_!_%", tokenInternalNameLike)
	require.True(t, IsPipelineTokenNameLike(PipelineTokenNamePrefix+"g1"))
	require.False(t, IsPipelineTokenNameLike("12pipeline34xyz"))
}
