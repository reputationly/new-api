package middleware

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/relay/hilo"
	"github.com/QuantumNous/new-api/service"
)

// **没时长就不编** —— 编译器唯一的硬门,而且静默:不传 duration 时
// buildCompilerInput 返回 nil,singlecall 回落 text 模板,记录里只留一句
// "缺少请求事实(CompilerInput),无法编译"。线上视频增强全部走了 text 就是因为这个。
//
// 运营在聚合模型里写 generate.overrides.duration 就能补上(overrides 在规范化之前
// 写进 body,于是 norm.Duration 读得到),这条钉住那个因果。
func TestBuildCompilerInputRequiresDuration(t *testing.T) {
	meta := map[string]any{"src_ref_images": []any{"https://a/ref.png"}}
	body := map[string]any{"model": "x", "prompt": "p", "metadata": meta}
	norm := normalizeTaskRequest(body)
	require.NotNil(t, norm)
	taskType := resolveCompilerTaskType(body, norm)
	require.Equal(t, hilo.TaskR2VA, taskType, "参考族应解析成 r2va")
	require.Nil(t, buildCompilerInput(body, norm, "p", taskType),
		"没有时长就不该编")

	withDur := map[string]any{"model": "x", "prompt": "p", "duration": 5, "metadata": meta}
	normDur := normalizeTaskRequest(withDur)
	require.Equal(t, 5, normDur.Duration)
	in := buildCompilerInput(withDur, normDur, "p", taskType)
	require.NotNil(t, in, "有时长就该编")
	require.Equal(t, hilo.TaskR2VA, in.TaskType)
	require.InDelta(t, 5, in.DurationSeconds, 1e-9)
}

// 回落 text 但改写成功时 enhance_degraded 仍是 false(那一位的含义是"用了原始提示词"),
// 所以要单独记一位"配置的档位没跑成",否则记录自相矛盾。
func TestToTaskAggregateInfoMarksFellBack(t *testing.T) {
	exp := &AggregateExpansion{
		PublicName: "h3-2k",
		Enhance:    &service.EnhanceResult{FallbackReason: "传输检查未通过: Shot times"},
	}
	info := exp.ToTaskAggregateInfo(nil)
	require.NotNil(t, info)
	require.True(t, info.EnhanceFellBack)
	require.False(t, info.EnhanceDegraded)

	noFallback := (&AggregateExpansion{PublicName: "m", Enhance: &service.EnhanceResult{}}).ToTaskAggregateInfo(nil)
	require.False(t, noFallback.EnhanceFellBack)
}
