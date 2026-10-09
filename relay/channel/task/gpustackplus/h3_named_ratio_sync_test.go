package gpustackplus

import (
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
)

// 聚合展开(middleware)要从像素串推导宽高比,用的是 common 里的一份具名列表;
// 它必须与适配器这边的 h3NamedAspectRatios 逐项一致,否则推导出的值会被这里拒掉。
func TestH3NamedAspectRatiosMatchCommonCopy(t *testing.T) {
	want := make([]string, 0, len(h3NamedAspectRatios))
	for name := range h3NamedAspectRatios {
		want = append(want, name)
	}
	sort.Strings(want)
	require.Equal(t, want, common.H3NamedAspectRatioNames())
}
