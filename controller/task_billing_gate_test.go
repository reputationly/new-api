package controller

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/stretchr/testify/require"
)

func videoBillingInfo(t *testing.T) *relaycommon.RelayInfo {
	t.Helper()
	info := &relaycommon.RelayInfo{OriginModelName: "m"}
	info.TaskRelayInfo = &relaycommon.TaskRelayInfo{}
	info.PriceData = types.PriceData{}
	return info
}

func TestFreezeVideoBilling_NilWhenNotHit(t *testing.T) {
	info := videoBillingInfo(t)
	require.Nil(t, freezeVideoBilling(info))

	info.TaskRelayInfo = nil
	require.Nil(t, freezeVideoBilling(info))
}

func TestFreezeVideoBilling_CopiesAllDimensions(t *testing.T) {
	info := videoBillingInfo(t)
	info.VideoBilling = &relaycommon.VideoBillingContext{
		Mode:          ratio_setting.VideoPriceModeToken,
		UnitPrice:     8.3836,
		Resolution:    "1080p",
		Seconds:       10,
		HasVideoInput: true,
	}

	got := freezeVideoBilling(info)
	require.NotNil(t, got)
	require.Equal(t, ratio_setting.VideoPriceModeToken, got.Mode)
	require.InDelta(t, 8.3836, got.UnitPrice, 1e-9)
	require.Equal(t, "1080p", got.Resolution)
	require.Equal(t, 10, got.Seconds)
	require.True(t, got.HasVideoInput)
}
