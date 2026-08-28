package middleware

import (
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

/*
停用分组在 TokenAuth 里的两个位置性事实。

两者都用源码位置断言而不是起 gin context 跑一遍：后者要造 token、user cache、
Redis 一整套依赖，而这里要钉的事实只有「哪段代码在哪段之前 / 之内」。这类断言在
重构时会误报，但误报的代价是看一眼注释，远小于漏掉这两个洞的代价——它们都不报错，
只是静默放行或静默显示错误文案。
*/

func readAuthSource(t *testing.T) []byte {
	t.Helper()
	src, err := os.ReadFile("auth.go")
	require.NoError(t, err)
	return src
}

// TestGroupDisabledCheckPrecedesUsableCheck 停用检查必须排在「无权访问」检查之前。
//
// GetUserUsableGroups（service/group.go）会把停用分组从返回的 map 里删掉，所以
// 一旦顺序反了，停用分支就是死代码：用户拿到的是「无权访问」，而他其实有权限，
// 只是分组被运营暂停了。两种情况的处置完全不同——一个等活动重开、一个换令牌。
func TestGroupDisabledCheckPrecedesUsableCheck(t *testing.T) {
	src := readAuthSource(t)

	disabledIdx := regexp.MustCompile(`setting\.IsGroupDisabled\(`).FindIndex(src)
	usableIdx := regexp.MustCompile(`service\.GetUserUsableGroups\(userGroup\)\[tokenGroup\]`).
		FindIndex(src)

	require.NotNil(t, disabledIdx, "未找到停用检查")
	require.NotNil(t, usableIdx, "未找到可用分组检查")
	require.Less(t, disabledIdx[0], usableIdx[0],
		"IsGroupDisabled 必须在 GetUserUsableGroups 之前，否则停用分支是死代码")
}

// TestGroupDisabledCheckCoversEmptyTokenGroup 停用检查必须在 `tokenGroup != ""`
// 判断**之外**。
//
// 令牌没指定分组时会回落到用户自己的分组（userCache.Group），检查放在块内就整个
// 绕过去了——现网有 9 个空 group 的老令牌。症状是服务层认为该分组不可用、auth 层
// 却放行，一半拦一半放。
//
// 顺序断言（上一条）挡不住这个：顺序确实对了，只是覆盖面不够。
func TestGroupDisabledCheckCoversEmptyTokenGroup(t *testing.T) {
	src := readAuthSource(t)

	blockIdx := regexp.MustCompile(`if tokenGroup != ""`).FindIndex(src)
	disabledIdx := regexp.MustCompile(`setting\.IsGroupDisabled\(`).FindIndex(src)

	require.NotNil(t, blockIdx, "未找到 tokenGroup 判空块")
	require.NotNil(t, disabledIdx, "未找到停用检查")
	require.Less(t, disabledIdx[0], blockIdx[0],
		"IsGroupDisabled 必须在 `if tokenGroup != \"\"` 之外，否则空 group 令牌会绕过停用")
}

// TestGroupDisabledCheckUsesEffectiveGroup 判的必须是「实际生效的分组」，
// 而不是 tokenGroup —— 后者为空时判空串等于不判。
func TestGroupDisabledCheckUsesEffectiveGroup(t *testing.T) {
	src := readAuthSource(t)

	require.Regexp(t, `setting\.IsGroupDisabled\(effectiveGroup\)`, string(src),
		"停用检查的入参必须是回落后的实际分组")
	require.Regexp(t, `effectiveGroup\s*=\s*userGroup`, string(src),
		"令牌未指定分组时必须回落到用户自己的分组")
}
