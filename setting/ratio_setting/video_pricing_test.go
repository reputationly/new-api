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

// per_second：[分辨率] → $/秒。LTX-2.5 与 minimax-h3 的时长是连续的
// （H3 4~15 秒、LTX 1080p 最长 17.7 秒），穷举成 per_call 要填 92 格，
// 按秒只要 6 格。
const perSecondCfg = `{
  "ltx2.5": {
    "mode": "per_second",
    "per_second": { "544p": 0.0137, "704p": 0.0274, "1080p": 0.0685, "2k": 0.137 }
  },
  "minimax-h3-fl2va": {
    "mode": "per_second",
    "per_second": { "480p": 0.0274, "768p": 0.0548 }
  }
}`

func TestVideoPricing_PerSecondLookup(t *testing.T) {
	loadCfg(t, perSecondCfg)

	e, ok := GetVideoPricing("ltx2.5")
	require.True(t, ok)
	require.Equal(t, VideoPriceModePerSecond, e.Mode)

	p, ok := e.LookupPerSecond("1080p")
	require.True(t, ok)
	require.InDelta(t, 0.0685, p, 1e-9)

	// 2K 是 LTX 最贵的档，也是 VideoResolutionTier 曾经解析不出来的那个
	p, ok = e.LookupPerSecond("2k")
	require.True(t, ok)
	require.InDelta(t, 0.137, p, 1e-9)

	// 行名归一化与另外两种模式同一套
	for _, form := range []string{"544p", "544P", " 544P "} {
		p, ok := e.LookupPerSecond(form)
		require.Truef(t, ok, "form=%q", form)
		require.InDelta(t, 0.0137, p, 1e-9)
	}

	// 未配的档位不回退到相邻档——收错钱比收不到钱更难发现
	_, ok = e.LookupPerSecond("720p")
	require.False(t, ok)
	_, ok = e.LookupPerSecond("")
	require.False(t, ok)
}

// 三种模式互斥：拿错模式查表必须落空，否则一个模型改了 mode 却没清空旧表时，
// 会按上一种模式的价继续收费。
func TestVideoPricing_ModesAreExclusive(t *testing.T) {
	loadCfg(t, perSecondCfg)
	e, _ := GetVideoPricing("ltx2.5")

	_, ok := e.LookupPerCall("1080p", 5)
	require.False(t, ok, "per_second 的表不能被 LookupPerCall 查到")
	_, ok = e.LookupToken("1080p", false)
	require.False(t, ok, "per_second 的表不能被 LookupToken 查到")

	loadCfg(t, seedanceCfg)
	e, _ = GetVideoPricing("kling-v2-master")
	_, ok = e.LookupPerSecond("720p")
	require.False(t, ok, "per_call 的表不能被 LookupPerSecond 查到")
}

func TestVideoPricing_PerSecondValidate(t *testing.T) {
	// 价格为 0 视为未配置（与另外两种模式同口径），保存时放行、查表时未命中
	require.NoError(t, UpdateVideoPricingByJSONString(
		`{"m": {"mode": "per_second", "per_second": {"720p": 0}}}`))
	e, _ := GetVideoPricing("m")
	_, ok := e.LookupPerSecond("720p")
	require.False(t, ok)

	// 负价 / NaN 必须在保存时拦住，不能等到结算时算出负数配额
	require.Error(t, UpdateVideoPricingByJSONString(
		`{"m": {"mode": "per_second", "per_second": {"720p": -1}}}`))
	// 空行名同理
	require.Error(t, UpdateVideoPricingByJSONString(
		`{"m": {"mode": "per_second", "per_second": {"  ": 1}}}`))
	// mode 拼错要报错而不是静默按旧路径计费
	require.Error(t, UpdateVideoPricingByJSONString(
		`{"m": {"mode": "persecond", "per_second": {"720p": 1}}}`))

	t.Cleanup(func() { _ = UpdateVideoPricingByJSONString("") })
}

// B：`*` 兜底行。语义与 GroupModelRatio 的 `*` 一致——精确行优先，兜底行接住长尾。
//
// 存在的理由：分辨率这一维**没有后端校验**（common/media_model_config.go 文件头说明了
// 为什么 sizes 不能当白名单），API 直连可以传任意 size。而档位词与精确像素是两套
// 坐标系：LTX 的 1080p 档真实像素是 1920×1088，VideoResolutionTier 按短边归档会得到
// "4k"，矩阵里没有这一行 → 静默回退固定价。兜底行让长尾至少有价可查。
const fallbackRowCfg = `{
  "ltx2.5": {
    "mode": "per_second",
    "per_second": { "1080p": 0.0685, "*": 0.02 }
  },
  "kling-v2-master": {
    "mode": "per_call",
    "per_call": { "720p": { "5": 0.2 }, "*": { "5": 0.15, "10": 0.3 } }
  }
}`

func TestVideoPricing_PerSecondFallbackRow(t *testing.T) {
	loadCfg(t, fallbackRowCfg)
	e, _ := GetVideoPricing("ltx2.5")

	// 精确行优先于兜底行
	p, ok := e.LookupPerSecond("1080p")
	require.True(t, ok)
	require.InDelta(t, 0.0685, p, 1e-9)

	// 未列出的档位落到兜底行，而不是未命中
	for _, r := range []string{"4k", "2k", "544p", "720p"} {
		p, ok := e.LookupPerSecond(r)
		require.Truef(t, ok, "resolution=%q 应落到兜底行", r)
		require.InDeltaf(t, 0.02, p, 1e-9, "resolution=%q", r)
	}

	// 空行名仍然未命中：解析不出分辨率时兜底行也不该接——那意味着连"这是什么尺寸"
	// 都不知道，按兜底价收是在猜
	_, ok = e.LookupPerSecond("")
	require.False(t, ok)
}

func TestVideoPricing_PerCallFallbackRow(t *testing.T) {
	loadCfg(t, fallbackRowCfg)
	e, _ := GetVideoPricing("kling-v2-master")

	p, ok := e.LookupPerCall("720p", 5)
	require.True(t, ok)
	require.InDelta(t, 0.2, p, 1e-9)

	// 精确行存在但该秒数没配 → 落兜底行的同名列
	p, ok = e.LookupPerCall("720p", 10)
	require.True(t, ok)
	require.InDelta(t, 0.3, p, 1e-9)

	// 整行未列出 → 兜底行
	p, ok = e.LookupPerCall("1080p", 5)
	require.True(t, ok)
	require.InDelta(t, 0.15, p, 1e-9)

	// 兜底行也没有的秒数仍然未命中——兜底管的是分辨率维度，不是秒数维度
	_, ok = e.LookupPerCall("1080p", 7)
	require.False(t, ok)
}

// 没配兜底行时行为逐位不变——这条保证 B 是纯增量
func TestVideoPricing_NoFallbackRowUnchanged(t *testing.T) {
	loadCfg(t, perSecondCfg)
	e, _ := GetVideoPricing("ltx2.5")
	_, ok := e.LookupPerSecond("720p")
	require.False(t, ok)
}

// 三种模式的兜底行为必须逐位一致。
//
// 判据是那条既有语义：「价格为 0 一律视为**未配置**而非免费」。既然 0 == 未配置，
// 「这一行是 0」就必须等价于「这一行不存在」——而不存在的行是要落兜底的。
// LookupPerSecond 第一版在这里直接 return false，外层的 "*" 一次都试不到，
// 与 lookupCell 分叉。
func TestVideoPricing_ZeroRowFallsThroughToFallback(t *testing.T) {
	loadCfg(t, `{
	  "m-sec":  { "mode": "per_second", "per_second": { "1080p": 0, "*": 0.02 } },
	  "m-call": { "mode": "per_call",   "per_call": { "1080p": { "5": 0 }, "*": { "5": 0.02 } } }
	}`)

	e, _ := GetVideoPricing("m-sec")
	p, ok := e.LookupPerSecond("1080p")
	require.True(t, ok, "精确行价格为 0 = 未配置，应落兜底行")
	require.InDelta(t, 0.02, p, 1e-9)

	// 与 per_call 对照——两边必须给出同一个答案
	e2, _ := GetVideoPricing("m-call")
	p2, ok2 := e2.LookupPerCall("1080p", 5)
	require.True(t, ok2)
	require.InDelta(t, p, p2, 1e-9, "per_second 与 per_call 的兜底行为必须一致")
}

// 没有兜底行时，0 仍然是未命中——这条保证上面的改动没把「0 = 未配置」改成「0 = 免费」
func TestVideoPricing_ZeroRowWithoutFallbackStillMisses(t *testing.T) {
	loadCfg(t, `{"m": {"mode": "per_second", "per_second": {"1080p": 0}}}`)
	e, _ := GetVideoPricing("m")
	_, ok := e.LookupPerSecond("1080p")
	require.False(t, ok)
}
