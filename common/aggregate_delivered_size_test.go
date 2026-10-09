package common

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 交付档位优先,留空回落到客户要的目标;nil 不 panic。
func TestAggregateUpscaleBilledSize(t *testing.T) {
	var nilUp *AggregateUpscale
	require.Equal(t, "", nilUp.BilledSize())
	require.Equal(t, "2k", (&AggregateUpscale{Target: "2k"}).BilledSize())
	require.Equal(t, "1080P", (&AggregateUpscale{Target: "2k", DeliveredSize: " 1080P "}).BilledSize())
}

// 运营在配置里写的 delivered_size 必须真的被解析进来。
func TestAggregateModelParsesDeliveredSize(t *testing.T) {
	raw := `[{"name":"h3-2k","type":"video","enabled":true,
		"generate":{"model":"minimax-h3"},
		"upscale":{"model":"seedvr2","target_size":"2k","delivered_size":"1080P"}}]`
	var models []AggregateModel
	require.NoError(t, UnmarshalJsonStr(raw, &models))
	require.Len(t, models, 1)
	require.Equal(t, "1080P", models[0].Upscale.DeliveredSize)
	require.Equal(t, "1080P", models[0].Upscale.BilledSize())
}
