package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
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

// expandMusic 跑一次真实的展开,返回改写后的请求体与捕获到的增强输入。
func expandMusic(t *testing.T, body string) (map[string]any, *service.EnhanceInput) {
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
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", strings.NewReader(body))
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

// 声明成音乐但引擎族没写成 minimax-music3:一个字都不改。
// 落到视频那条路会把顶层 prompt(歌词)当成提示词改写 —— 比不增强糟得多。
func TestMusicAggregateWithoutMusic3EngineLeavesBodyUntouched(t *testing.T) {
	withMusic3Engine(t, "acestep")
	withAggregateConfig(t, musicAggConfig)

	body, got := expandMusic(t, `{
		"model":"music3-enhanced","prompt":"a warm acoustic pop song",
		"metadata":{"task_type":"t2m","instructions":"ignored"}
	}`)
	require.Nil(t, got, "引擎族不是 Music3 时不该发起增强")
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
