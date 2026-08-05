package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/require"
)

const seedanceCfg = `{
  "doubao-seedance-2-0-260128": {
    "mode": "token",
    "token": {
      "480p":  { "with_video": 4.6027, "without_video": 7.5616 },
      "720p":  { "with_video": 4.6027, "without_video": 7.5616 },
      "1080p": { "with_video": 5.0959, "without_video": 8.3836 }
    }
  },
  "kling-v2-master": {
    "mode": "per_call",
    "per_call": { "720p": { "5": 0.1918, "10": 0.3836 } }
  }
}`

func loadCfg(t *testing.T, s string) {
	t.Helper()
	require.NoError(t, UpdateVideoPricingByJSONString(s))
	t.Cleanup(func() { _ = UpdateVideoPricingByJSONString("") })
}

func TestVideoPricing_TokenLookup(t *testing.T) {
	loadCfg(t, seedanceCfg)

	e, ok := GetVideoPricing("doubao-seedance-2-0-260128")
	require.True(t, ok)

	// 1080p 与 720p 必须取到不同的价——这正是改造前缺失的那一维。
	p, ok := e.LookupToken("1080p", false)
	require.True(t, ok)
	require.InDelta(t, 8.3836, p, 1e-9)

	p, ok = e.LookupToken("720p", false)
	require.True(t, ok)
	require.InDelta(t, 7.5616, p, 1e-9)

	p, ok = e.LookupToken("1080p", true)
	require.True(t, ok)
	require.InDelta(t, 5.0959, p, 1e-9)
}

func TestVideoPricing_ResolutionKeyIsCaseInsensitive(t *testing.T) {
	loadCfg(t, seedanceCfg)
	e, _ := GetVideoPricing("doubao-seedance-2-0-260128")
	for _, form := range []string{"720p", "720P", " 720P "} {
		p, ok := e.LookupToken(form, false)
		require.Truef(t, ok, "form=%q", form)
		require.InDelta(t, 7.5616, p, 1e-9)
	}
}

func TestVideoPricing_PerCallLookup(t *testing.T) {
	loadCfg(t, seedanceCfg)
	e, ok := GetVideoPricing("kling-v2-master")
	require.True(t, ok)

	p, ok := e.LookupPerCall("720p", 10)
	require.True(t, ok)
	require.InDelta(t, 0.3836, p, 1e-9)

	// 未配的秒数不该回退到别的档
	_, ok = e.LookupPerCall("720p", 7)
	require.False(t, ok)
	_, ok = e.LookupPerCall("720p", 0)
	require.False(t, ok)
}

func TestVideoPricing_ModeMismatchMisses(t *testing.T) {
	loadCfg(t, seedanceCfg)
	tokenEntry, _ := GetVideoPricing("doubao-seedance-2-0-260128")
	perCallEntry, _ := GetVideoPricing("kling-v2-master")

	_, ok := tokenEntry.LookupPerCall("720p", 5)
	require.False(t, ok, "token 模式不该被当成按次表查")
	_, ok = perCallEntry.LookupToken("720p", false)
	require.False(t, ok, "per_call 模式不该被当成 token 表查")
}

func TestVideoPricing_Misses(t *testing.T) {
	loadCfg(t, seedanceCfg)

	_, ok := GetVideoPricing("不存在的模型")
	require.False(t, ok)

	e, _ := GetVideoPricing("doubao-seedance-2-0-260128")
	_, ok = e.LookupToken("4k", false) // 该模型没配 4k
	require.False(t, ok)
	_, ok = e.LookupToken("", false)
	require.False(t, ok)
}

// 0 视为未配置而非免费：调用方靠 ok 决定是否回退旧计费路径，
// 若 0 返回 true 就会以「免费」结算，这是最贵的一种误判。
func TestVideoPricing_ZeroPriceTreatedAsUnset(t *testing.T) {
	loadCfg(t, `{"m":{"mode":"token","token":{"720p":{"without_video":0}}}}`)
	e, _ := GetVideoPricing("m")
	_, ok := e.LookupToken("720p", false)
	require.False(t, ok)
}

func TestVideoPricing_EmptyStringClearsConfig(t *testing.T) {
	loadCfg(t, seedanceCfg)
	require.NoError(t, UpdateVideoPricingByJSONString(""))
	_, ok := GetVideoPricing("doubao-seedance-2-0-260128")
	require.False(t, ok)
}

func TestVideoPricing_RoundTrip(t *testing.T) {
	loadCfg(t, seedanceCfg)
	dumped := VideoPricing2JSONString()
	require.NoError(t, UpdateVideoPricingByJSONString(dumped))
	e, ok := GetVideoPricing("doubao-seedance-2-0-260128")
	require.True(t, ok)
	p, ok := e.LookupToken("1080p", false)
	require.True(t, ok)
	require.InDelta(t, 8.3836, p, 1e-9)
}

func TestVideoPricing_Rejects(t *testing.T) {
	cases := map[string]string{
		"非法 mode":       `{"m":{"mode":"ratio","token":{"720p":{"without_video":1}}}}`,
		"缺 mode":        `{"m":{"token":{"720p":{"without_video":1}}}}`,
		"负价格":           `{"m":{"mode":"token","token":{"720p":{"without_video":-1}}}}`,
		"token 列名拼错":    `{"m":{"mode":"token","token":{"720p":{"withvideo":1}}}}`,
		"per_call 列名非数": `{"m":{"mode":"per_call","per_call":{"720p":{"five":1}}}}`,
		"per_call 秒数为零": `{"m":{"mode":"per_call","per_call":{"720p":{"0":1}}}}`,
		"空分辨率":          `{"m":{"mode":"token","token":{"  ":{"without_video":1}}}}`,
		"坏 JSON":        `{"m":`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			require.Error(t, UpdateVideoPricingByJSONString(raw))
		})
	}
}

// 校验失败时不能留下半张表。
func TestVideoPricing_RejectedSaveLeavesPreviousConfigIntact(t *testing.T) {
	loadCfg(t, seedanceCfg)
	require.Error(t, UpdateVideoPricingByJSONString(
		`{"good":{"mode":"token","token":{"720p":{"without_video":1}}},"bad":{"mode":"nope"}}`))

	// 旧配置仍在，新配置一个都没进来
	_, ok := GetVideoPricing("doubao-seedance-2-0-260128")
	require.True(t, ok)
	_, ok = GetVideoPricing("good")
	require.False(t, ok)
}
