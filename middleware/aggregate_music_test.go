package middleware

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// 音乐聚合的字段语义与视频**相反**,这一组守的就是这件事:
//
//	Music3: 顶层 prompt = 歌词,metadata.instructions = 曲风描述
//
// 照视频那条路改写 prompt,就是把用户的词交出去"优化",而描述一个字没动 —— 不报错。

const musicAggConfig = `[{
	"name":"music3-enhanced","type":"music","enabled":true,
	"generate":{"model":"my-music"},
	"prompt_enhance":{"model":"enh"}
}]`

// withMusic3Engine 把「音乐模型配置」里 my-music 声明成 Music3 引擎族。
func withMusic3Engine(t *testing.T, engine string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	orig, had := common.OptionMap["MusicModelConfig"]
	common.OptionMap["MusicModelConfig"] = `{"models":{"my-music":{"engine":"` + engine + `"}}}`
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		if had {
			common.OptionMap["MusicModelConfig"] = orig
		} else {
			delete(common.OptionMap, "MusicModelConfig")
		}
		common.OptionMapRWMutex.Unlock()
	})
}

// expandMusic 跑一次真实的展开(内部路由 /v1/videos),返回改写后的请求体与捕获到的增强输入。
func expandMusic(t *testing.T, body string) (map[string]any, *service.EnhanceInput) {
	return expandMusicAt(t, "/v1/videos", body)
}

// expandMusicAt 与 expandMusic 相同,但请求路径可指定。
//
// 路径不是细节:/v1/music_generation 是 MiniMax 官方协议兼容层,它看到的 body 形状
// (prompt=描述、lyrics=歌词)与内部契约(prompt=歌词、metadata.instructions=描述)相反,
// 展开阶段按**路由**而不是按 body 字段决定走哪套语义 —— 这组用例两边都要覆盖。
func expandMusicAt(t *testing.T, path, body string) (map[string]any, *service.EnhanceInput) {
	t.Helper()
	orig := enhancePrompt
	t.Cleanup(func() { enhancePrompt = orig })
	var got *service.EnhanceInput
	enhancePrompt = func(_ context.Context, _ *common.AggregateModel, _ string,
		in service.EnhanceInput) *service.EnhanceResult {
		got = &in
		return &service.EnhanceResult{OriginalPrompt: in.Prompt, EnhancedPrompt: "NEW CAPTION"}
	}

	agg := common.GetAggregateModel("music3-enhanced")
	require.NotNil(t, agg)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	require.NoError(t, applyAggregateExpansion(c, "music3-enhanced", "my-music", agg))
	return readBodyMap(t, c), got
}

func TestMusic3EnhancesInstructionsNotLyrics(t *testing.T) {
	withMusic3Engine(t, "minimax-music3")
	withAggregateConfig(t, musicAggConfig)

	body, got := expandMusic(t, `{
		"model":"music3-enhanced",
		"prompt":"[Verse]\n雨落在霓虹上",
		"metadata":{"task_type":"t2m","instructions":"a warm acoustic pop song"}
	}`)

	require.NotNil(t, got, "增强没有被调用")
	require.Equal(t, "a warm acoustic pop song", got.Prompt,
		"改写对象必须是描述,不是歌词")
	require.Equal(t, "[Verse]\n雨落在霓虹上", got.Lyrics,
		"歌词必须以只读上下文传下去")
	require.Contains(t, got.TaskContext, "Write the caption in English",
		"音乐的事实块没有接上")

	md, _ := body["metadata"].(map[string]any)
	require.NotNil(t, md)
	require.Equal(t, "NEW CAPTION", md["instructions"], "描述没有被写回")
	require.Equal(t, "[Verse]\n雨落在霓虹上", body["prompt"], "歌词被改动了")
}

// metadata 可能是 JSON 编码的字符串(TaskSubmitReq 两种都收),写回必须走同一个 helper。
func TestMusic3EnhancesInstructionsWhenMetadataIsAString(t *testing.T) {
	withMusic3Engine(t, "minimax-music3")
	withAggregateConfig(t, musicAggConfig)

	body, got := expandMusic(t, `{
		"model":"music3-enhanced",
		"prompt":"la la",
		"metadata":"{\"task_type\":\"t2m\",\"instructions\":\"lofi beat\"}"
	}`)
	require.NotNil(t, got, "metadata 是字符串时增强没被调用")
	require.Equal(t, "lofi beat", got.Prompt)
	md, _ := body["metadata"].(map[string]any)
	require.NotNil(t, md, "metadata 没有归一化回对象")
	require.Equal(t, "NEW CAPTION", md["instructions"])
}

// 引擎把 caption 与歌词拼成一条 prompt 编码,超了就整条拒 —— 而那时增强的钱已经花了。
// 所以回填前用同一个预算再核一次,超了就用原始描述(不截断:截断会连标题一起砍掉)。
func TestMusic3RejectsEnhancementOverJointBudget(t *testing.T) {
	withMusic3Engine(t, "minimax-music3")
	withAggregateConfig(t, musicAggConfig)

	orig := enhancePrompt
	t.Cleanup(func() { enhancePrompt = orig })
	enhancePrompt = func(_ context.Context, _ *common.AggregateModel, _ string,
		in service.EnhanceInput) *service.EnhanceResult {
		// 5000 字符的联合上限,歌词占 4 个字符,这份描述必然超
		return &service.EnhanceResult{
			OriginalPrompt: in.Prompt,
			EnhancedPrompt: strings.Repeat("a", common.Music3CaptionBudget("la la")+1),
		}
	}

	agg := common.GetAggregateModel("music3-enhanced")
	require.NotNil(t, agg)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{
		"model":"music3-enhanced","prompt":"la la",
		"metadata":{"task_type":"t2m","instructions":"lofi beat"}
	}`))
	require.NoError(t, applyAggregateExpansion(c, "music3-enhanced", "my-music", agg))

	body := readBodyMap(t, c)
	md, _ := body["metadata"].(map[string]any)
	require.Equal(t, "lofi beat", md["instructions"], "超预算的描述不该被写回")

	exp := GetAggregateExpansion(c)
	require.NotNil(t, exp)
	require.NotNil(t, exp.Enhance)
	require.True(t, exp.Enhance.Degraded, "超预算要显式记成降级,不能假装成功")
	require.Contains(t, exp.Enhance.DegradeReason, "上限")
}

// 没有描述就没有可改写的东西。**绝不能拿歌词顶上** —— 那等于毁掉用户写的词。
func TestMusic3SkipsEnhancementWithoutCaption(t *testing.T) {
	withMusic3Engine(t, "minimax-music3")
	withAggregateConfig(t, musicAggConfig)

	body, got := expandMusic(t, `{
		"model":"music3-enhanced","prompt":"[Verse]\n雨落在霓虹上",
		"metadata":{"task_type":"t2m"}
	}`)
	require.Nil(t, got, "没有描述时不该发起增强(会把歌词当描述改写)")
	require.Equal(t, "[Verse]\n雨落在霓虹上", body["prompt"])
}

// 声明成音乐,但引擎族既不是 Music3 也不是 ACE-Step:一个字都不改。
// 落到视频那条路会把顶层 prompt 当成提示词改写 —— 比不增强糟得多。
func TestMusicAggregateWithUnknownEngineLeavesBodyUntouched(t *testing.T) {
	withMusic3Engine(t, "suno-v5")
	withAggregateConfig(t, musicAggConfig)

	body, got := expandMusic(t, `{
		"model":"music3-enhanced","prompt":"a warm acoustic pop song",
		"metadata":{"task_type":"t2m","instructions":"ignored"}
	}`)
	require.Nil(t, got, "引擎族既不是 Music3 也不是 ACE-Step 时不该发起增强")
	require.Equal(t, "a warm acoustic pop song", body["prompt"], "歌词/描述被改动了")
	md, _ := body["metadata"].(map[string]any)
	require.Equal(t, "ignored", md["instructions"], "描述被改动了")
}

// 没有声明引擎族时按模型名兜底:出厂配置里的 minimax-music3 在刚部署的站点上通常还没
// 在「音乐模型配置」里声明过,而"没声明"不该让这条流水线的增强整个不工作。
func TestMusicAggregateFallsBackToNameWhenEngineUndeclared(t *testing.T) {
	withMusic3Engine(t, "") // 声明了 my-music 但没有 engine 字段
	withAggregateConfig(t, `[{
		"name":"music3-enhanced","type":"music","enabled":true,
		"generate":{"model":"minimax-music3"},
		"prompt_enhance":{"model":"enh"}
	}]`)

	orig := enhancePrompt
	t.Cleanup(func() { enhancePrompt = orig })
	called := false
	enhancePrompt = func(_ context.Context, _ *common.AggregateModel, _ string,
		in service.EnhanceInput) *service.EnhanceResult {
		called = true
		return &service.EnhanceResult{OriginalPrompt: in.Prompt, EnhancedPrompt: "NEW CAPTION"}
	}

	agg := common.GetAggregateModel("music3-enhanced")
	require.NotNil(t, agg)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(`{
		"model":"music3-enhanced","prompt":"[Verse]\n雨落在霓虹上",
		"metadata":{"task_type":"t2m","instructions":"a warm pop song"}
	}`))
	require.NoError(t, applyAggregateExpansion(c, "music3-enhanced", "minimax-music3", agg))

	require.True(t, called, "名字带 music3 时应按名字兜底发起增强")
	body := readBodyMap(t, c)
	md, _ := body["metadata"].(map[string]any)
	require.Equal(t, "NEW CAPTION", md["instructions"])
	require.Equal(t, "[Verse]\n雨落在霓虹上", body["prompt"])
}

// **ACE-Step 改的是描述,不是歌词。** 它的字段语义与 Music3 正相反:
//
//	Music3   顶层 prompt = 歌词,     metadata.instructions = 曲风描述
//	ACE-Step 顶层 prompt = 曲风描述, metadata.lyrics      = 歌词
//
// 写错字段不会报错,只会把用户唱出来的词交给优化,引擎再拿它当曲风描述编曲。
func TestAceStepEnhancesPromptNotLyrics(t *testing.T) {
	withMusic3Engine(t, "acestep")
	withAggregateConfig(t, musicAggConfig)

	body, got := expandMusic(t, `{
		"model":"music3-enhanced",
		"prompt":"a warm acoustic pop song",
		"metadata":{"task_type":"t2m","lyrics":"[Verse] rain on neon"}
	}`)

	require.NotNil(t, got, "增强没有被调用")
	require.Equal(t, "a warm acoustic pop song", got.Prompt, "改写对象必须是描述")
	require.Equal(t, "[Verse] rain on neon", got.Lyrics, "歌词必须以只读上下文传下去")
	require.Contains(t, got.TaskContext, "ACE-Step", "ACE-Step 的事实块没有接上")
	require.Contains(t, got.TaskContext, "at most", "字数预算没有接上")

	require.Equal(t, "NEW CAPTION", body["prompt"], "增强结果没写回 prompt 位")
	md, _ := body["metadata"].(map[string]any)
	require.NotNil(t, md)
	require.Equal(t, "[Verse] rain on neon", md["lyrics"], "歌词被改动了")
}

// sample 模式没有 prompt:描述在 metadata.sample_query,写回也得写在那儿。
func TestAceStepSampleModeRewritesSampleQuery(t *testing.T) {
	withMusic3Engine(t, "acestep")
	withAggregateConfig(t, musicAggConfig)

	body, got := expandMusic(t, `{
		"model":"music3-enhanced",
		"prompt":"",
		"metadata":{"task_type":"t2m","sample_query":"a cheerful song about spring"}
	}`)

	require.NotNil(t, got, "增强没有被调用")
	require.Equal(t, "a cheerful song about spring", got.Prompt)
	md, _ := body["metadata"].(map[string]any)
	require.Equal(t, "NEW CAPTION", md["sample_query"], "sample 模式要写回 sample_query")
	require.Equal(t, "", body["prompt"], "sample 模式的 prompt 位不该被填上东西")
}

// 没有描述(只有歌词)时**一个字都不改**:拿歌词顶上就是把用户的词交出去优化。
func TestAceStepSkipsEnhancementWithoutCaption(t *testing.T) {
	withMusic3Engine(t, "acestep")
	withAggregateConfig(t, musicAggConfig)

	body, got := expandMusic(t, `{
		"model":"music3-enhanced",
		"prompt":"",
		"metadata":{"task_type":"t2m","lyrics":"[Verse] rain on neon"}
	}`)
	require.Nil(t, got, "没有描述时不该发起增强")
	md, _ := body["metadata"].(map[string]any)
	require.Equal(t, "[Verse] rain on neon", md["lyrics"])
}

// **官方 MiniMax 形状**(prompt=描述、lyrics=歌词、没有 metadata.instructions)。
//
// /v1/music_generation 兼容层在展开**之后**才重建 body,所以展开阶段看到的是这个形状。
// 形状由**路由**判定,不由 body 字段猜 —— 猜错的后果是静默的:不增强,或把描述写进
// 兼容层会丢弃的字段。
func TestMusic3AggregateAcceptsOfficialShape(t *testing.T) {
	withMusic3Engine(t, "minimax-music3")
	withAggregateConfig(t, musicAggConfig)

	body, got := expandMusicAt(t, "/v1/music_generation",
		`{"model":"music3-enhanced","prompt":"a warm acoustic pop song","lyrics":"[Verse]\nthe night falls"}`)
	require.NotNil(t, got, "官方形状下也必须触发增强")
	require.Equal(t, "a warm acoustic pop song", got.Prompt, "改写的应是描述")
	require.Equal(t, "[Verse]\nthe night falls", got.Lyrics, "歌词只读")
	require.Equal(t, "NEW CAPTION", body["prompt"], "写回 prompt:兼容层正是从这里取描述")
	require.Equal(t, "[Verse]\nthe night falls", body["lyrics"], "歌词一字不动")
}

// 官方形状但既没描述也没歌词之外的东西时,仍然不增强(不猜)。
func TestMusic3OfficialShapeWithoutDescriptionDoesNothing(t *testing.T) {
	withMusic3Engine(t, "minimax-music3")
	withAggregateConfig(t, musicAggConfig)

	body, got := expandMusicAt(t, "/v1/music_generation", `{"model":"music3-enhanced","prompt":""}`)
	require.Nil(t, got, "没有描述就没有可改写的东西")
	require.NotContains(t, body, "lyrics")
}

// 官方形状下**没有 prompt** 时兼容层必 400("prompt is required")——
// 绝不能拿 lyrics 当描述去增强(fmt.Sprint(nil) 会得到 "<nil>",既计费又绕过校验)。
func TestMusic3OfficialRouteWithoutPromptDoesNotEnhance(t *testing.T) {
	withMusic3Engine(t, "minimax-music3")
	withAggregateConfig(t, musicAggConfig)

	body, got := expandMusicAt(t, "/v1/music_generation",
		`{"model":"music3-enhanced","lyrics":"[Verse]\nx"}`)
	require.Nil(t, got, "缺 prompt 的请求注定被兼容层拒掉,不该发起增强")
	require.NotContains(t, body, "prompt", "不该凭空填一个 prompt")
	require.Equal(t, "[Verse]\nx", body["lyrics"])
}

// 官方形状的纯器乐:没有歌词但 is_instrumental=true,描述仍在 prompt,
// 照样要增强(歌词位传空串,事实块会说明是器乐)。
// 纯器乐在 Music3 上不支持(实测:只给段落标记 9.8–22.7 秒;给足段落 121–144 秒但有人声),
// 兼容层对它明确 400。所以展开阶段不能先花增强的钱 —— 这条断言由"要增强"翻转为"不增强"。
func TestMusic3OfficialRouteInstrumentalSkipsEnhancement(t *testing.T) {
	withMusic3Engine(t, "minimax-music3")
	withAggregateConfig(t, musicAggConfig)

	body, got := expandMusicAt(t, "/v1/music_generation",
		`{"model":"music3-enhanced","prompt":"calm piano","is_instrumental":true}`)
	require.Nil(t, got, "器乐请求会被兼容层 400,不该先增强")
	require.Equal(t, "calm piano", body["prompt"], "描述原样不动")
}

// 官方形状:有描述但既没歌词又没声明 is_instrumental —— 兼容层会以
// "Music3 needs lyrics unless is_instrumental=true" 拒掉,不该先花增强的钱。
func TestMusic3OfficialRouteWithoutLyricsDoesNotEnhance(t *testing.T) {
	withMusic3Engine(t, "minimax-music3")
	withAggregateConfig(t, musicAggConfig)

	body, got := expandMusicAt(t, "/v1/music_generation",
		`{"model":"music3-enhanced","prompt":"calm piano"}`)
	require.Nil(t, got, "无歌词又非器乐的请求注定被兼容层拒掉,不该发起增强")
	require.Equal(t, "calm piano", body["prompt"], "描述不该被改动")
}

// 官方形状下 metadata.instructions **不算数**:兼容层重建任务 body 时会丢掉它
// (描述由 prompt 映射),读了它等于改写一个会被丢弃的字段。
func TestMusic3OfficialRouteIgnoresMetadataInstructions(t *testing.T) {
	withMusic3Engine(t, "minimax-music3")
	withAggregateConfig(t, musicAggConfig)

	body, got := expandMusicAt(t, "/v1/music_generation", `{
		"model":"music3-enhanced","prompt":"a warm pop song","lyrics":"[Verse]\nx",
		"metadata":{"instructions":"old desc","task_type":"t2m"}
	}`)
	require.NotNil(t, got)
	require.Equal(t, "a warm pop song", got.Prompt,
		"官方形状的描述在 prompt,metadata.instructions 会被兼容层丢弃")
	require.Equal(t, "[Verse]\nx", got.Lyrics)
	require.Equal(t, "NEW CAPTION", body["prompt"])
}

// 内部路由上的"官方长相"body(prompt=描述、lyrics=歌词)不再触发猜测:
// 内部契约里 prompt 位是歌词、metadata.instructions 才是描述,没有描述就不增强。
func TestMusic3InternalRouteDoesNotGuessOfficialShape(t *testing.T) {
	withMusic3Engine(t, "minimax-music3")
	withAggregateConfig(t, musicAggConfig)

	body, got := expandMusic(t,
		`{"model":"music3-enhanced","prompt":"a warm acoustic pop song","lyrics":"[Verse]\nthe night falls"}`)
	require.Nil(t, got, "内部路由不该按 body 字段猜官方形状")
	require.Equal(t, "a warm acoustic pop song", body["prompt"])
	require.Equal(t, "[Verse]\nthe night falls", body["lyrics"])
}

// 官方形状的 ACE-Step:歌词在顶层 lyrics(不在 metadata.lyrics),描述在 prompt。
// 以前只读 metadata.lyrics,歌词丢了、事实块还谎称"没有歌词,按器乐处理"。
func TestAceStepOfficialRouteReadsTopLevelLyrics(t *testing.T) {
	withMusic3Engine(t, "acestep")
	withAggregateConfig(t, musicAggConfig)

	body, got := expandMusicAt(t, "/v1/music_generation",
		`{"model":"music3-enhanced","prompt":"warm pop","lyrics":"[Verse]\nrain on neon"}`)
	require.NotNil(t, got, "增强没有被调用")
	require.Equal(t, "warm pop", got.Prompt, "改写对象必须是描述")
	require.Equal(t, "[Verse]\nrain on neon", got.Lyrics, "歌词必须取自顶层 lyrics")
	require.NotContains(t, got.TaskContext, "No lyrics were supplied",
		"歌词明明在,事实块却说没有 —— 会把人声写没")
	require.Equal(t, "NEW CAPTION", body["prompt"], "增强结果没写回 prompt 位")
	require.Equal(t, "[Verse]\nrain on neon", body["lyrics"], "歌词被改动了")
}

// 官方形状的 ACE-Step:prompt 是描述,空了兼容层必 400 ——
// metadata.sample_query 救不了它,不该为注定被拒的请求增强。
func TestAceStepOfficialRouteWithoutPromptDoesNotEnhance(t *testing.T) {
	withMusic3Engine(t, "acestep")
	withAggregateConfig(t, musicAggConfig)

	body, got := expandMusicAt(t, "/v1/music_generation", `{
		"model":"music3-enhanced","prompt":"",
		"metadata":{"task_type":"t2m","sample_query":"a cheerful song about spring"}
	}`)
	require.Nil(t, got, "缺 prompt 的请求注定被兼容层拒掉,不该发起增强")
	require.Equal(t, "", body["prompt"])
	md, _ := body["metadata"].(map[string]any)
	require.NotNil(t, md)
	require.Equal(t, "a cheerful song about spring", md["sample_query"], "sample_query 不该被改写")
}

// 真服务端到端:不打桩 enhancePrompt,让增强段真的发一次 HTTP 自调用
// (service.EnhancePrompt → callEnhance → ServerAddress/v1/chat/completions)。
// 假服务端验证三件事:歌词以 LYRICS 块进了 user 消息、带了 X-Aggregate-Stage 头、
// 改写结果真的写回了官方形状的 prompt 位 —— 桩测试证明不了最后这条链路是通的。
func TestMusic3OfficialRouteRealEnhanceCall(t *testing.T) {
	withMusic3Engine(t, "minimax-music3")
	withAggregateConfig(t, musicAggConfig)

	type enhanceCall struct {
		path  string
		auth  string
		stage string
		body  []byte
	}
	calls := make(chan enhanceCall, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		calls <- enhanceCall{
			path:  r.URL.Path,
			auth:  r.Header.Get("Authorization"),
			stage: r.Header.Get(service.AggregateStageHeader),
			body:  raw,
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ENHANCED CAPTION"}}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`))
	}))
	defer srv.Close()

	origAddr := system_setting.ServerAddress
	system_setting.ServerAddress = srv.URL
	t.Cleanup(func() { system_setting.ServerAddress = origAddr })

	agg := common.GetAggregateModel("music3-enhanced")
	require.NotNil(t, agg)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/music_generation",
		strings.NewReader(`{"model":"music3-enhanced","prompt":"calm piano","lyrics":"[Verse]\nrain on neon"}`))
	c.Request.Header.Set("Authorization", "Bearer test-token")
	require.NoError(t, applyAggregateExpansion(c, "music3-enhanced", "my-music", agg))

	body := readBodyMap(t, c)
	require.Equal(t, "ENHANCED CAPTION", body["prompt"], "真实增强的结果要写回 prompt 位")
	require.Equal(t, "[Verse]\nrain on neon", body["lyrics"], "歌词一字不动")

	var call enhanceCall
	select {
	case call = <-calls:
	case <-time.After(10 * time.Second):
		t.Fatal("增强段没有发起 HTTP 自调用")
	}
	require.Equal(t, "/v1/chat/completions", call.path)
	require.Equal(t, "Bearer test-token", call.auth, "增强要以客户身份发起")
	require.Equal(t, service.LogStageEnhance, call.stage)

	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	require.NoError(t, common.Unmarshal(call.body, &req))
	require.Len(t, req.Messages, 2)
	require.Equal(t, "user", req.Messages[1].Role)
	require.Contains(t, req.Messages[1].Content, "calm piano")
	require.Contains(t, req.Messages[1].Content, "=== LYRICS")
	require.Contains(t, req.Messages[1].Content, "[Verse]\nrain on neon")
}

func TestMusic3OfficialRouteWithoutLyricsSkipsEnhancement(t *testing.T) {
	withMusic3Engine(t, "minimax-music3")
	withAggregateConfig(t, musicAggConfig)

	_, got := expandMusic(t, `{"model":"music3-enhanced","prompt":"a calm piano solo"}`)
	require.Nil(t, got, "没有歌词必然被拒,不该先增强")
}
