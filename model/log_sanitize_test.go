package model

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
)

// 返回给普通用户的日志必须剥掉内部路由信息。
//
// 模型映射的结果与渠道名是同一类信息:渠道配了 model mapping 之后,
// upstream_model_name 会把供应商侧的真实模型名直接摆给终端用户看。
func TestFormatUserLogsStripsInternalRoutingInfo(t *testing.T) {
	logs := []*Log{{
		ChannelName: "某某中转渠道",
		Other: common.MapToJsonStr(map[string]any{
			"upstream_model_name": "deepseek-v4-flash",
			"is_model_mapped":     true,
			"admin_info":          map[string]any{"use_channel": []string{"7"}},
			"stream_status":       "ok",
			// 与计费相关的展示字段必须留下 —— 用户要能看懂自己被扣了多少。
			"model_price":      1.5,
			"user_group_ratio": 0.85,
		}),
	}}

	formatUserLogs(logs, 0)

	got, err := common.StrToMap(logs[0].Other)
	require.NoError(t, err)

	require.NotContains(t, got, "upstream_model_name",
		"上游真实模型名不该给终端用户 —— 它暴露了我们实际用的是谁家的模型")
	require.NotContains(t, got, "is_model_mapped",
		"只删名字不够:这个布尔照样告诉用户这里发生了映射")
	require.NotContains(t, got, "admin_info")
	require.NotContains(t, got, "stream_status")
	require.Empty(t, logs[0].ChannelName, "渠道名同理,不给用户")

	// 计费展示字段不能被误伤。
	require.Contains(t, got, "model_price")
	require.Contains(t, got, "user_group_ratio")
}

// 没有 other 的日志不该 panic(旧数据/系统日志可能为空)。
func TestFormatUserLogsHandlesEmptyOther(t *testing.T) {
	logs := []*Log{{Other: ""}, {Other: "not json"}}
	require.NotPanics(t, func() { formatUserLogs(logs, 0) })
}
