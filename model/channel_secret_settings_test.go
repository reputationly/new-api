package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

func settingsOf(t *testing.T, ch *Channel) map[string]any {
	t.Helper()
	m := map[string]any{}
	require.NoError(t, common.UnmarshalJsonStr(ch.OtherSettings, &m))
	return m
}

func TestRedactSecretSettingsHidesSKAndKeepsUnknownKeys(t *testing.T) {
	ch := &Channel{OtherSettings: `{"ark_asset_enabled":true,"ark_asset_access_key":"AK","ark_asset_secret_key":"SK","custom_field":"x"}`}
	ch.RedactSecretSettings()

	m := settingsOf(t, ch)
	require.NotContains(t, m, "ark_asset_secret_key")
	require.Equal(t, true, m["ark_asset_secret_key_set"])
	require.Equal(t, "AK", m["ark_asset_access_key"])
	require.Equal(t, "x", m["custom_field"], "结构体没声明的键不能被丢掉")

	untouched := `{"aws_key_type":"ak_sk"}`
	ch = &Channel{OtherSettings: untouched}
	ch.RedactSecretSettings()
	require.Equal(t, untouched, ch.OtherSettings, "没有 SK 的渠道原样返回")
}

func TestKeepSecretSettings(t *testing.T) {
	origin := &Channel{OtherSettings: `{"ark_asset_access_key":"AK","ark_asset_secret_key":"OLD"}`}

	t.Run("SK 留空沿用原值，展示标记不落库", func(t *testing.T) {
		ch := &Channel{OtherSettings: `{"ark_asset_access_key":"AK","ark_asset_secret_key_set":true}`}
		ch.KeepSecretSettings(origin)
		m := settingsOf(t, ch)
		require.Equal(t, "OLD", m["ark_asset_secret_key"])
		require.NotContains(t, m, "ark_asset_secret_key_set")
	})
	t.Run("填了新 SK 就用新的", func(t *testing.T) {
		ch := &Channel{OtherSettings: `{"ark_asset_access_key":"AK","ark_asset_secret_key":"NEW"}`}
		ch.KeepSecretSettings(origin)
		require.Equal(t, "NEW", settingsOf(t, ch)["ark_asset_secret_key"])
	})
	t.Run("清空 AK 连带清空 SK", func(t *testing.T) {
		ch := &Channel{OtherSettings: `{"ark_asset_access_key":"","ark_asset_secret_key":"NEW"}`}
		ch.KeepSecretSettings(origin)
		require.NotContains(t, settingsOf(t, ch), "ark_asset_secret_key")
	})
	t.Run("与素材库无关的设置不改写", func(t *testing.T) {
		raw := `{"aws_key_type":"ak_sk"}`
		ch := &Channel{OtherSettings: raw}
		ch.KeepSecretSettings(origin)
		require.Equal(t, raw, ch.OtherSettings)
	})
}
