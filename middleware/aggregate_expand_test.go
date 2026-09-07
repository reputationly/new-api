package middleware

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
)

// 这两个包装只是把"从 body 归一化"这一步补上,让既有用例保持按单个请求体书写。
// 生产路径复用同一份归一化结果(见 applyAggregateExpansion),不重复序列化 ——
// 参考素材常是 base64 data URL,多序列化一次是几 MB 的代价。
func collectInputImagesFromBody(body map[string]any) []string {
	return collectInputImages(body, normalizeTaskRequest(body))
}

func buildTaskContextFromBody(body map[string]any) string {
	return buildTaskContext(body, normalizeTaskRequest(body))
}

func withAggregateConfig(t *testing.T, raw string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	orig, had := common.OptionMap["AggregateModelConfig"]
	common.OptionMap["AggregateModelConfig"] = raw
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if had {
			common.OptionMap["AggregateModelConfig"] = orig
		} else {
			delete(common.OptionMap, "AggregateModelConfig")
		}
		common.OptionMapRWMutex.Unlock()
		common.GetAggregateModels()
	})
}

const aggConfig = `[{
	"name":"h3-2k","type":"video","enabled":true,
	"generate":{"model":"minimax-h3"},
	"upscale":{"model":"seedvr2","target_size":"2k"}
}]`

// 聚合模型名要能展开成生成段的真实模型 —— 它自己没有 ability,不展开就选不到任何渠道。
func TestExpandAggregateModel(t *testing.T) {
	withAggregateConfig(t, aggConfig)

	got, _, err := expandAggregateModel("h3-2k", "default", "default")
	require.NoError(t, err)
	require.Equal(t, "minimax-h3", got)
}

// 非聚合模型原样放行:返回空串表示"这不是聚合模型",调用方不得改写任何东西。
func TestExpandLeavesNormalModelUntouched(t *testing.T) {
	withAggregateConfig(t, aggConfig)

	got, _, err := expandAggregateModel("gpt-4o", "default", "default")
	require.NoError(t, err)
	require.Empty(t, got)
}

// 停用的聚合模型不可调用(GetAggregateModel 只返回 enabled 的)。
func TestExpandIgnoresDisabledAggregate(t *testing.T) {
	withAggregateConfig(t, `[{"name":"off","type":"video","enabled":false,"generate":{"model":"x"}}]`)

	got, _, err := expandAggregateModel("off", "default", "default")
	require.NoError(t, err)
	require.Empty(t, got, "停用的聚合模型不该被展开")
}

// 配了 groups 就是显式白名单:不在名单里的分组按「模型不存在」拒绝,
// 与可见性拦截同口径 —— 不能泄露这个定向能力的存在。
func TestExpandEnforcesGroupWhitelist(t *testing.T) {
	withAggregateConfig(t, `[{
		"name":"vip-only","type":"video","enabled":true,
		"groups":["vip"],
		"generate":{"model":"minimax-h3"}
	}]`)

	got, _, err := expandAggregateModel("vip-only", "vip", "vip")
	require.NoError(t, err)
	require.Equal(t, "minimax-h3", got)

	_, _, err = expandAggregateModel("vip-only", "default", "default")
	require.Error(t, err, "不在白名单的分组应被拒绝")
}

// 未配 groups = 不额外限制:约束交给展开后的生成段模型(选渠道那步会拒),
// 而不是在这里凭空拒掉。两处语义必须与 controller/token.go 的同名判定一致。
func TestExpandWithoutGroupsAllowsAny(t *testing.T) {
	withAggregateConfig(t, aggConfig)

	for _, g := range []string{"default", "vip", "whatever"} {
		got, _, err := expandAggregateModel("h3-2k", g, g)
		require.NoError(t, err)
		require.Equal(t, "minimax-h3", got, "未配 groups 时不该按分组拒绝")
	}
}

// 缺生成段模型的配置要报错,不能展开成空模型名后一路走到选渠道才报"模型名为空"。
func TestExpandRejectsMissingGenerateModel(t *testing.T) {
	withAggregateConfig(t, `[{"name":"broken","type":"video","enabled":true}]`)

	_, _, err := expandAggregateModel("broken", "default", "default")
	require.Error(t, err)
}

// body 里的 model 必须一并改写:适配器构造上游请求时读的是 body,
// 只改内存里那份会把聚合模型名原样发给上游,上游根本不认识它。
func TestApplyExpansionRewritesBody(t *testing.T) {
	withAggregateConfig(t, aggConfig)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos",
		strings.NewReader(`{"model":"h3-2k","prompt":"a cat","size":"2k"}`))

	agg := common.GetAggregateModel("h3-2k")
	require.NoError(t, applyAggregateExpansion(c, "h3-2k", "minimax-h3", agg))

	body := readBodyMap(t, c)
	require.Equal(t, "minimax-h3", body["model"], "body 里的 model 必须改写成生成段模型")
	// 其余字段不得丢失 —— 改写是外科手术,不是重建请求。
	require.Equal(t, "a cat", body["prompt"])
	require.Equal(t, "2k", body["size"])

	// 展开结果要留住"客户以为自己在调什么",否则排障时无从知道这是一次聚合调用。
	exp := GetAggregateExpansion(c)
	require.NotNil(t, exp)
	require.Equal(t, "h3-2k", exp.PublicName)
	require.NotNil(t, exp.Config)
	require.Equal(t, "seedvr2", exp.Config.Upscale.Model)
}

// Overrides 是聚合模型的要害:客户传的是「最终尺寸」,生成段必须收到「中间尺寸」。
// 不应用它的话,客户传 size=2k 会原样打到 H3 上 —— 正是那个会 OOM/被钳位的请求,
// 也就是这个功能要解决的问题本身。
func TestApplyExpansionAppliesOverrides(t *testing.T) {
	withAggregateConfig(t, `[{
		"name":"h3-2k","type":"video","enabled":true,
		"generate":{"model":"minimax-h3","overrides":{"size":"1280x720"}},
		"upscale":{"model":"seedvr2","target_size":"2k"}
	}]`)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos",
		strings.NewReader(`{"model":"h3-2k","prompt":"a cat","size":"2k"}`))

	require.NoError(t, applyAggregateExpansion(c, "h3-2k", "minimax-h3", common.GetAggregateModel("h3-2k")))

	body := readBodyMap(t, c)
	require.Equal(t, "1280x720", body["size"],
		"客户要的 2k 是最终尺寸,生成段必须收到被 overrides 改写后的中间尺寸")
	require.Equal(t, "minimax-h3", body["model"])
	require.Equal(t, "a cat", body["prompt"], "未被覆盖的字段应原样保留")
}

// overrides 里手滑写了 model 不该顶掉展开结果 —— 那会让请求发去一个既非聚合模型、
// 也非配置的生成段模型的地方,而且不报错。
func TestApplyExpansionOverridesCannotHijackModel(t *testing.T) {
	withAggregateConfig(t, `[{
		"name":"h3-2k","type":"video","enabled":true,
		"generate":{"model":"minimax-h3","overrides":{"model":"someone-else"}}
	}]`)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos",
		strings.NewReader(`{"model":"h3-2k","prompt":"x"}`))

	require.NoError(t, applyAggregateExpansion(c, "h3-2k", "minimax-h3", common.GetAggregateModel("h3-2k")))

	require.Equal(t, "minimax-h3", readBodyMap(t, c)["model"],
		"overrides 不得覆盖展开决定的 model")
}

// readBodyMap 直接从 body storage 取字节解析,不经 Content-Type 分派。
func readBodyMap(t *testing.T, c *gin.Context) map[string]any {
	t.Helper()
	storage, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	raw, err := storage.Bytes()
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, common.Unmarshal(raw, &m))
	return m
}

// 改写不能依赖 Content-Type。UnmarshalBodyReusable 对未知 Content-Type 会
// 「返回 nil 却什么都不填」,照它写会让改写在客户端不带该请求头时静默落空:
// 聚合模型名原样发给上游,上游报未知模型,而我们这边一切正常。
func TestApplyExpansionWorksWithoutContentType(t *testing.T) {
	withAggregateConfig(t, aggConfig)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos",
		strings.NewReader(`{"model":"h3-2k","prompt":"a cat"}`))
	c.Request.Header.Del("Content-Type")

	require.NoError(t, applyAggregateExpansion(c, "h3-2k", "minimax-h3", common.GetAggregateModel("h3-2k")))

	body := readBodyMap(t, c)
	require.Equal(t, "minimax-h3", body["model"])
	require.Equal(t, "a cat", body["prompt"], "其余字段不得丢失")
}

// 空请求体必须报错,不能补一个空 map 继续 —— 那样改写完只剩 {"model":...},
// prompt / 输入图全没了,请求却还会照常发给上游。
func TestApplyExpansionRejectsEmptyBody(t *testing.T) {
	withAggregateConfig(t, aggConfig)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(``))

	require.Error(t, applyAggregateExpansion(c, "h3-2k", "minimax-h3", common.GetAggregateModel("h3-2k")))
}

// JSON 字面量 null 解析成功但得到 nil map —— 这是唯一能走到 body==nil 分支的输入
// (空串会先被 Unmarshal 判成语法错误)。若在这里补一个空 map 继续,改写完就只剩
// {"model":...},prompt / 输入图全丢,而请求照常发给上游。
func TestApplyExpansionRejectsNullBody(t *testing.T) {
	withAggregateConfig(t, aggConfig)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`null`))

	err := applyAggregateExpansion(c, "h3-2k", "minimax-h3", common.GetAggregateModel("h3-2k"))
	require.Error(t, err, "body 为 null 时必须报错,不能补空 map 继续")
}

// 非聚合请求不该挂上展开结果。
func TestGetAggregateExpansionNilForNormalRequest(t *testing.T) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	require.Nil(t, GetAggregateExpansion(c))
}

// 展开判定与令牌白名单判定必须同语义,否则会出现「存得进白名单却调不通」
// 或反过来的错位。这里把两边的分组判定放在一起比对。
func TestGroupAllowedMatchesTokenSideSemantics(t *testing.T) {
	noGroups := &common.AggregateModel{Name: "a"}
	vipOnly := &common.AggregateModel{Name: "b", Groups: []string{"vip"}}

	require.True(t, groupAllowedForAggregate(noGroups, "anything", "anything"),
		"未配 groups 应放行任意分组")
	require.True(t, groupAllowedForAggregate(vipOnly, "vip", "vip"))
	require.False(t, groupAllowedForAggregate(vipOnly, "default", "default"))
	require.False(t, groupAllowedForAggregate(nil, "default", "default"))
}

// 增强降级时**必须保留客户的原始 prompt**,不能把降级后的空值或半成品写回 body。
// 这里用一个必然降级的配置(没配增强模型)验证整条接线。
func TestApplyExpansionKeepsPromptWhenEnhanceDegrades(t *testing.T) {
	withAggregateConfig(t, `[{
		"name":"h3-2k","type":"video","enabled":true,
		"generate":{"model":"minimax-h3"},
		"prompt_enhance":{"system_prompt":"改写以下提示词"}
	}]`)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos",
		strings.NewReader(`{"model":"h3-2k","prompt":"a cat"}`))

	require.NoError(t, applyAggregateExpansion(c, "h3-2k", "minimax-h3", common.GetAggregateModel("h3-2k")))

	require.Equal(t, "a cat", readBodyMap(t, c)["prompt"],
		"增强降级后必须原样使用客户的提示词")

	exp := GetAggregateExpansion(c)
	require.NotNil(t, exp.Enhance, "降级也要留下记录,否则排障时看不出增强跑没跑")
	require.True(t, exp.Enhance.Degraded)
	require.NotEmpty(t, exp.Enhance.DegradeReason)
}

// 未启用增强段时不该产生任何增强记录,也不该动 prompt。
func TestApplyExpansionSkipsEnhanceWhenDisabled(t *testing.T) {
	withAggregateConfig(t, aggConfig)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos",
		strings.NewReader(`{"model":"h3-2k","prompt":"a cat"}`))

	require.NoError(t, applyAggregateExpansion(c, "h3-2k", "minimax-h3", common.GetAggregateModel("h3-2k")))

	require.Equal(t, "a cat", readBodyMap(t, c)["prompt"])
	require.Nil(t, GetAggregateExpansion(c).Enhance, "未启用增强不该留下记录")
}

// 输入图要从请求体里收集出来喂给增强模型 —— 覆盖图生图/首尾帧/参考生视频的入参形态。
func TestCollectInputImages(t *testing.T) {
	got := collectInputImagesFromBody(map[string]any{
		"image":           "https://a/1.png",
		"images":          []any{"https://a/2.png", "", "https://a/3.png"},
		"input_reference": "https://a/4.png",
		"prompt":          "not an image",
	})
	require.ElementsMatch(t,
		[]string{"https://a/1.png", "https://a/2.png", "https://a/3.png", "https://a/4.png"}, got)
}

// 纯文生请求没有图,收集结果为空,增强照常按文字工作(不该 panic 或塞入空串)。
func TestCollectInputImagesEmptyForTextOnly(t *testing.T) {
	require.Empty(t, collectInputImagesFromBody(map[string]any{"prompt": "a cat"}))
}

// 参考生视频(r2va)的参考图在 metadata.src_ref_images 里,不在顶层。
//
// 只看顶层的话,这类请求收集到的图是**空的**,增强模型只能从文字猜 —— 而参考生视频的
// 全部创作意图就在这些素材里,猜出来的描述会与素材直接打架。
func TestCollectInputImagesReadsMetadataRefs(t *testing.T) {
	got := collectInputImagesFromBody(map[string]any{
		"prompt": "a cat",
		"metadata": map[string]any{
			"task_type":      "r2va",
			"src_ref_images": []any{"https://a/ref1.png", "", "https://a/ref2.png"},
		},
	})
	require.ElementsMatch(t, []string{"https://a/ref1.png", "https://a/ref2.png"}, got)
}

// 首帧(顶层 image)与参考素材(metadata)是不同的键,两边都要收。
func TestCollectInputImagesMergesTopLevelAndMetadata(t *testing.T) {
	got := collectInputImagesFromBody(map[string]any{
		"image":    "https://a/first.png",
		"metadata": map[string]any{"src_ref_images": []any{"https://a/ref.png"}},
	})
	require.ElementsMatch(t, []string{"https://a/first.png", "https://a/ref.png"}, got)
}

// 参考视频/音频**不能**当成图片发给增强模型。
//
// 每一项都会被编成 image_url part,塞一段视频进去轻则被忽略、重则整个请求被拒;
// 而调用方常传 base64 data-uri,一段视频还会把请求撑爆 —— 结果是增强整体降级,
// 比看不见参考视频更糟。
func TestCollectInputImagesSkipsNonImageRefs(t *testing.T) {
	got := collectInputImagesFromBody(map[string]any{
		"metadata": map[string]any{
			"src_ref_images":               []any{"https://a/ref.png"},
			"reference_videos":             []any{"https://a/clip.mp4"},
			"reference_audios":             []any{"https://a/voice.wav"},
			"reference_video_durations_ms": []any{3000},
		},
	})
	require.Equal(t, []string{"https://a/ref.png"}, got)
}

// metadata 不是对象时按"没有参考素材"处理,不能 panic ——
// 请求体的合法性由上游适配器判定,在中间件里为它报错只会挡住本能跑通的生成。
func TestCollectInputImagesToleratesNonObjectMetadata(t *testing.T) {
	require.Empty(t, collectInputImagesFromBody(map[string]any{"metadata": "not-an-object"}))
	require.Empty(t, collectInputImagesFromBody(map[string]any{"metadata": nil}))
}

// 参考生视频要把素材清单和标号说清楚:增强模型看不到面板,不说它就不知道有几张图、
// 该用哪个编号指代 —— 而 guide 正是靠 <Picture N> / <Video N> 指代素材的。
func TestBuildTaskContextListsReferenceAssets(t *testing.T) {
	got := buildTaskContextFromBody(map[string]any{
		"metadata": map[string]any{
			"task_type":        "r2va",
			"src_ref_images":   []any{"a", "b"},
			"reference_videos": []any{"c"},
			"reference_audios": []any{"d"},
		},
	})
	require.Contains(t, got, "2 reference image(s), labelled <Picture 1>..<Picture 2>")
	require.Contains(t, got, "1 reference video(s), labelled <Video 1>")
	require.Contains(t, got, "<Audio 1>")
}

// 首尾帧齐全时说明两张图各自的时间角色 —— 说反了模型会朝错误的方向收敛。
func TestBuildTaskContextNamesFrameRoles(t *testing.T) {
	got := buildTaskContextFromBody(map[string]any{
		"images":   []any{"first.png", "last.png"},
		"metadata": map[string]any{"task_type": "flf2v"},
	})
	require.Contains(t, got, "FL2VA")
	require.Contains(t, got, "<Picture 1> is the first frame and <Picture 2> is the last frame")
}

// 只给一张关键帧时**不猜**是首是尾:猜反了不会报错,只会让模型朝反方向收敛。
// 陈述"这是一张关键帧"信息量更少,但不会误导。
func TestBuildTaskContextDoesNotGuessSingleKeyframeRole(t *testing.T) {
	got := buildTaskContextFromBody(map[string]any{
		"images":   []any{"only.png"},
		"metadata": map[string]any{"task_type": "flf2v"},
	})
	require.NotContains(t, got, "first frame and")
	require.Contains(t, got, "<Picture 1> is a keyframe")
}

// 时长要带两位小数:guide 要求每个切点时间戳落在成片时长内,而模型看不到面板上的秒数。
// duration 是 UnmarshalWithNumber 解出来的 json.Number,只判 float64 会静默取不到值。
func TestBuildTaskContextReadsJSONNumberDuration(t *testing.T) {
	got := buildTaskContextFromBody(map[string]any{
		"duration": json.Number("5"),
		"metadata": map[string]any{"task_type": "i2v"},
	})
	require.Contains(t, got, "Effective video duration: 5.00 seconds")
	require.Contains(t, got, "I2VA")
}

// 说不出任何事实就返回空串 —— 拼一个只有标题没有内容的"Current request:"上去,
// 等于告诉模型"这里本该有信息但没有",不如不拼。
func TestBuildTaskContextEmptyWhenNothingKnown(t *testing.T) {
	require.Empty(t, buildTaskContextFromBody(map[string]any{"prompt": "a cat"}))
	require.Empty(t, buildTaskContextFromBody(map[string]any{"metadata": "not-an-object"}))
}

// 大整数必须原样保留。
//
// 这里是对客户**整个请求体**做读-改-写:用普通 Unmarshal 的话所有 JSON 数字会变成
// float64,超过 2^53 的整数(seed、纳秒时间戳、id 形态的 metadata)在 Marshal 回去时
// 被静默改值或写成指数形式,上游按整数解析直接拒 —— 而我们这边一切正常。
func TestApplyExpansionPreservesLargeIntegers(t *testing.T) {
	withAggregateConfig(t, aggConfig)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos",
		strings.NewReader(`{"model":"h3-2k","prompt":"a cat","seed":9007199254740993,"metadata":{"trace_id":1739000000000000123}}`))

	require.NoError(t, applyAggregateExpansion(c, "h3-2k", "minimax-h3", common.GetAggregateModel("h3-2k")))

	storage, err := common.GetBodyStorage(c)
	require.NoError(t, err)
	raw, err := storage.Bytes()
	require.NoError(t, err)
	// 直接看序列化后的文本:既不能被改值,也不能变成指数形式。
	require.Contains(t, string(raw), "9007199254740993",
		"超过 2^53 的 seed 必须原样保留,不能被 float64 round-trip 改掉")
	require.Contains(t, string(raw), "1739000000000000123",
		"嵌套在 metadata 里的大整数同样要保住")
	require.NotContains(t, string(raw), "e+", "不得被写成指数形式")
}

// "auto" 是合法的 token.Group,但不是任何真实分组的名字。
//
// 令牌保存侧对它的处理是展开成用户的自动分组集合再比白名单;调用侧若拿字面量 "auto"
// 去比,永远不中 —— auto 令牌能把聚合模型名存进白名单,每次调用却 404。
// 那正是这套判定要消除的「存得进却调不通」,只是换了个形式。
func TestGroupAllowedExpandsAutoGroup(t *testing.T) {
	orig := expandAutoGroups
	t.Cleanup(func() { expandAutoGroups = orig })
	// 该用户的 auto 池里含 vip。
	expandAutoGroups = func(userGroup string) []string { return []string{"default", "vip"} }

	vipOnly := &common.AggregateModel{Name: "a", Groups: []string{"vip"}}

	require.True(t, groupAllowedForAggregate(vipOnly, "auto", "default"),
		"auto 必须展开后比对:展开集合里有 vip,就该放行 —— 拿字面量 auto 比永远不中,"+
			"结果是 auto 令牌存得进白名单却每次调用 404")

	// 展开集合里没有白名单分组时,照常拒绝。
	expandAutoGroups = func(string) []string { return []string{"default"} }
	require.False(t, groupAllowedForAggregate(vipOnly, "auto", "default"))

	// 显式分组的语义不受影响。
	require.True(t, groupAllowedForAggregate(vipOnly, "vip", "vip"))
	require.False(t, groupAllowedForAggregate(vipOnly, "default", "default"))

	// 未配 groups 时一律放行,且不该去展开(没必要)。
	noGroups := &common.AggregateModel{Name: "b"}
	require.True(t, groupAllowedForAggregate(noGroups, "auto", "default"))
}

// ── 检视意见的回归 ───────────────────────────────────────────────────
// 这五条的共同点是「生成段读得到、增强段读不到」:请求照常出片,但改写出的提示词
// 描述的是另一个输入。全部不报错,只有产出对不上。

// l2va 与 i2v 的输入形态完全一样(一张图),只有语义相反。不说破,增强模型会按
// 最常见的 i2v 写"从这张图往下发展",方向正好反了。
func TestBuildTaskContextCoversL2VA(t *testing.T) {
	got := buildTaskContextFromBody(map[string]any{
		"images":   []any{"a.png"},
		"metadata": map[string]any{"task_type": "l2va"},
	})
	require.Contains(t, got, "L2VA")
	require.Contains(t, got, "LAST frame")
}

// R2VA 最多三段参考音频,写死成 1 会让后两段在增强模型眼里不存在。
func TestBuildTaskContextReportsAllReferenceAudios(t *testing.T) {
	got := buildTaskContextFromBody(map[string]any{
		"metadata": map[string]any{
			"task_type":        "r2va",
			"reference_audios": []any{"a.wav", "b.wav", "c.wav"},
		},
	})
	require.Contains(t, got, "3 reference audio(s)")
	require.Contains(t, got, "<Audio 1>..<Audio 3>")
}

// 逗号分隔的单串在生成段是多张参考图(common.MetadataStringList),
// 增强段若当成一项,既数错了、又把一个非法 URL 递给增强模型。
func TestCommaSeparatedRefsAgreeAcrossStages(t *testing.T) {
	body := map[string]any{
		"metadata": map[string]any{
			"task_type":      "r2va",
			"src_ref_images": "a.png,b.png",
		},
	}
	require.Contains(t, buildTaskContextFromBody(body), "2 reference image(s)")
	require.Equal(t, []string{"a.png", "b.png"}, collectInputImagesFromBody(body))
}

// data URL 自带逗号,拆了就成了两个残缺片段。这条是上面那条的反面,必须同时成立。
func TestDataURLRefIsNotCommaSplit(t *testing.T) {
	const dataURL = "data:image/png;base64,iVBORw0KGgo="
	body := map[string]any{
		"metadata": map[string]any{"task_type": "r2va", "src_ref_images": dataURL},
	}
	require.Contains(t, buildTaskContextFromBody(body), "1 reference image(s)")
	require.Equal(t, []string{dataURL}, collectInputImagesFromBody(body))
}

// metadata 允许是 JSON 编码的字符串(TaskSubmitReq.UnmarshalJSON 会解开)。
// 直接读 map 的话这种合法写法会让增强段一条事实都取不到。
func TestBuildTaskContextAcceptsJSONStringMetadata(t *testing.T) {
	got := buildTaskContextFromBody(map[string]any{
		"metadata": `{"task_type":"i2v"}`,
		"images":   []any{"a.png"},
	})
	require.Contains(t, got, "I2VA")
}

// duration 允许是字符串整数。取不到时表现为"增强照常跑、只是没有时长约束",
// 于是可能写出超出成片长度的分镜时间点。
func TestBuildTaskContextAcceptsStringDuration(t *testing.T) {
	got := buildTaskContextFromBody(map[string]any{
		"duration": "5",
		"metadata": map[string]any{"task_type": "t2v"},
	})
	require.Contains(t, got, "5.00 seconds")
}

// 单数键是平台契约里明确支持的拼法(与 doubao/Ark 对齐),生成段用
// TaskSubmitReq.RefVideos/RefAudios 两种都收。增强段只认复数就会说"没有参考素材",
// 而请求本身跑得好好的 —— 又一次"生成段看得到、增强段看不到"。
func TestBuildTaskContextAcceptsSingularReferenceKeys(t *testing.T) {
	got := buildTaskContextFromBody(map[string]any{
		"metadata": map[string]any{
			"task_type":       "r2va",
			"reference_video": "clip.mp4",
			"reference_audio": "voice.wav",
		},
	})
	require.Contains(t, got, "1 reference video(s)")
	require.Contains(t, got, "1 reference audio(s)")
	require.NotContains(t, got, "none yet")
}

// 只给 seconds 时**不陈述时长**。
//
// gpustackplus 确实会按 seconds 回落出片,但聚合展开跑在选渠道之前,这里不知道
// 生成段会落到哪个渠道;而 kling/vidu/jimeng 完全忽略 seconds,跟着回落就会告诉
// 增强模型一个上游根本不会采纳的时长,分镜时间点全落在片子之外。
// 计费侧对同一件事设了渠道闸(videoBillingSeconds 先判 TaskPlatform),我们拿不到
// 那个判据,就只能不说 —— 少说一句事实,比说一句错的安全。
func TestBuildTaskContextOmitsDurationWhenOnlySecondsGiven(t *testing.T) {
	got := buildTaskContextFromBody(map[string]any{
		"seconds":  "8",
		"metadata": map[string]any{"task_type": "t2v"},
	})
	require.NotContains(t, got, "seconds. Every cut timestamp")
}

// 显式 duration(含字符串整数)照常陈述 —— 那是调用方明确声明的事实,与渠道无关。
func TestBuildTaskContextStatesExplicitDuration(t *testing.T) {
	for _, raw := range []any{5, "5"} {
		got := buildTaskContextFromBody(map[string]any{
			"duration": raw,
			"metadata": map[string]any{"task_type": "t2v"},
		})
		require.Containsf(t, got, "5.00 seconds", "duration=%v(%T)", raw, raw)
	}
}

// 四句任务类型措辞逐字抄自前端 buildH3OptimizeContext。H3 的 guide 就是靠
// "Emit the <X> alignment instruction" 这句触发对齐指令的;只留 T2VA 的否定句
// 而丢掉三句肯定句是最糟的组合 —— 帧类任务从没被要求发,纯文生却明确说别发。
func TestTaskTypeWordingMatchesFrontendVerbatim(t *testing.T) {
	cases := []struct {
		name string
		body map[string]any
		want string
	}{
		{"FL2VA", map[string]any{"images": []any{"a.png", "b.png"}, "metadata": map[string]any{"task_type": "flf2v"}},
			"Emit the FL2VA alignment instruction, and prefer a single shot."},
		{"I2VA", map[string]any{"images": []any{"a.png"}, "metadata": map[string]any{"task_type": "i2v"}},
			"Emit the I2VA alignment instruction and develop forward from it."},
		{"L2VA", map[string]any{"images": []any{"a.png"}, "metadata": map[string]any{"task_type": "l2va"}},
			"is the LAST frame, not the first. Emit the L2VA alignment instruction and converge onto it at the end."},
		{"T2VA", map[string]any{"metadata": map[string]any{"task_type": "t2v"}},
			"Do NOT emit any alignment instruction; begin directly with integrated_multimodal_description."},
	}
	for _, c := range cases {
		require.Containsf(t, buildTaskContextFromBody(c.body), c.want, "%s 措辞与前端不一致", c.name)
	}
}

// 送给增强模型的图片顺序必须与 <Picture N> 标号一致。
//
// 参考族只吃 metadata.src_ref_images —— 顶层图不在它的输入契约里。混着发会同时坏两件事:
// 参考图被顶层图往后挤(<Picture 1> 指向了别的素材),以及把生成段根本不看的图摆到
// 增强模型面前。两者都不报错,只是改写出的提示词描述的是另一个输入。
func TestCollectInputImagesForR2VASendsOnlyReferenceImages(t *testing.T) {
	body := map[string]any{
		"image": "https://a/not-used-by-r2va.png",
		"metadata": map[string]any{
			"task_type":      "r2va",
			"src_ref_images": []any{"https://a/ref1.png", "https://a/ref2.png"},
		},
	}
	require.Equal(t, []string{"https://a/ref1.png", "https://a/ref2.png"}, collectInputImagesFromBody(body))
	// 标号说有两张,发出去的就必须正好是这两张、且顺序一致。
	require.Contains(t, buildTaskContextFromBody(body), "2 reference image(s), labelled <Picture 1>..<Picture 2>")
}

// 帧族相反:只吃顶层 images,metadata 里的参考图不属于它的契约。
// <Picture 1> 是首帧、<Picture 2> 是尾帧,顺序错了方向就反了。
func TestCollectInputImagesForFrameTasksSendsOnlyTopLevel(t *testing.T) {
	body := map[string]any{
		"images": []any{"https://a/first.png", "https://a/last.png"},
		"metadata": map[string]any{
			"task_type":      "flf2v",
			"src_ref_images": []any{"https://a/stray-ref.png"},
		},
	}
	require.Equal(t, []string{"https://a/first.png", "https://a/last.png"}, collectInputImagesFromBody(body))
}

// 帧族的条件图要按回落取,不能并集。同时给 images 和 image 时,生成段只用 images;
// 增强段若把 image 也算一份,<Picture 2> 就从"尾帧"变成了重复的首帧,对齐指令写反。
func TestCollectInputImagesForFrameTasksHonoursAliasPrecedence(t *testing.T) {
	body := map[string]any{
		"images":   []any{"https://a/first.png", "https://a/last.png"},
		"image":    "https://a/first.png",
		"metadata": map[string]any{"task_type": "flf2v"},
	}
	got := collectInputImagesFromBody(body)
	require.Equal(t, []string{"https://a/first.png", "https://a/last.png"}, got,
		"images 已给出时不应再并入 image")
	// 标号声明的张数必须与实际发送数一致。
	require.Contains(t, buildTaskContextFromBody(body), "<Picture 2> is the last frame")
}
