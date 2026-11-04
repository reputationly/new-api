package service

import (
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// withMusicEngineConfig 临时把「音乐模型配置」换成给定的 JSON。
// 引擎族就存在那份配置里(common.MusicEngineFamilyForModel),不换它测不到"按引擎族分派"。
func withMusicEngineConfig(t *testing.T, raw string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	orig, had := common.OptionMap["MusicModelConfig"]
	common.OptionMap["MusicModelConfig"] = raw
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

// 模板的产出形状就是引擎吃的东西:三节标题、英文、250~450 词。
// 它一旦被"顺手改写"成别的形状,不会有任何报错 —— 只是歌变难听,所以用结构断言守住。
func TestMusic3EnhanceTemplateContract(t *testing.T) {
	tpl := defaultMusic3EnhanceTemplate

	// 三节标题、且顺序固定(官方 Structured Caption 的定义)
	g := strings.Index(tpl, "### Global Metadata")
	v := strings.Index(tpl, "### Vocal Details")
	a := strings.Index(tpl, "### Arrangement")
	if g < 0 || v < 0 || a < 0 {
		t.Fatalf("模板缺少三节标题之一:%d/%d/%d", g, v, a)
	}
	if !(g < v && v < a) {
		t.Errorf("三节顺序不对:Global %d / Vocal %d / Arrangement %d", g, v, a)
	}

	// 歌词只作上下文,永不复述 —— 这条丢了,模型就会把歌词摘要甚至原句写进 caption,
	// 而 caption 与歌词会被引擎拼成同一条 prompt。
	for _, must := range []string{
		"Never quote, paraphrase",
		"instrumental",
		"250–450",
		"Write the caption in English",
	} {
		if !strings.Contains(tpl, must) {
			t.Errorf("模板缺少关键约束 %q", must)
		}
	}

	// 官方 skill 的模板库没有内联进来:references/ 与 templates/ 都是几十万字符的东西,
	// 塞进来会撑爆上下文,而且单次调用根本没有"读文件"这一步。
	for _, forbidden := range []string{"references/", "templates/", "genre-router"} {
		if strings.Contains(tpl, forbidden) {
			t.Errorf("模板里出现了模板库路径 %q —— 单次调用读不到文件,写了只会让模型空转", forbidden)
		}
	}
}

// 三节下的推荐子标题取自官方模板库的写法(Basic Attributes / Vocal Gender & Timbre /
// Instrument Lifecycle …)。它们是把 caption 写成"官方那种密度"的关键,不是装饰。
func TestMusic3EnhanceTemplateUsesOfficialSubLabels(t *testing.T) {
	tpl := defaultMusic3EnhanceTemplate
	for _, label := range []string{
		"Genre & Subgenre",
		"Emotional Progression",
		"Sonic & Production Profile",
		"Vocal Gender & Timbre",
		"Harmony & Backing Vocals",
		"Instrument Lifecycle",
		"Groove & Foundation Progression",
	} {
		if !strings.Contains(tpl, label) {
			t.Errorf("模板缺少官方子标题 %q", label)
		}
	}
}

// 分派优先级:**声明的引擎族 > 名字兜底**。
//
// 名字兜底不是"顺手":出厂配置里的 minimax-music3 在刚部署的站点上通常还没在「音乐模型
// 配置」里声明过,而"没声明"不该让一条出厂配置直接变红。但一旦声明了别的引擎族,声明
// 必须压过名字 —— ACE-Step 的字段语义与 Music3 相反,认错就是改写错字段。
func TestDefaultEnhanceTemplatePicksMusicByEngineFamily(t *testing.T) {
	withMusicEngineConfig(t, `{"models":{
		"my-music":{"engine":"minimax-music3"},
		"ace":{"engine":"acestep"},
		"music3-sold-as-ace":{"engine":"acestep"}
	}}`)

	// 1) 声明了 Music3,哪怕对外名里没有 music3
	if got := DefaultEnhanceTemplate("my-music"); got != defaultMusic3EnhanceTemplate {
		t.Error("声明为 minimax-music3 的模型没有拿到音乐模板")
	}
	// 2) 没声明,按名字兜底(出厂配置靠这条)
	if got := DefaultEnhanceTemplate("minimax-music3"); got != defaultMusic3EnhanceTemplate {
		t.Error("未声明引擎族时没有按模型名兜底,出厂配置会变成一条会静默降级的配置")
	}
	// 3) 没声明、名字也不像:拿不到模板(宁可显式失败,也不硬套)
	if got := DefaultEnhanceTemplate("my-tune"); got != "" {
		t.Error("既没声明也不像 Music3 的模型不该拿到音乐模板")
	}
	// 4) **声明压过名字**:名字里带 music3 但声明的是 ACE-Step
	if got := DefaultEnhanceTemplate("music3-sold-as-ace"); got != "" {
		t.Error("声明为 ACE-Step 时不该按名字套 Music3 模板")
	}
	// 5) ACE-Step 本身:它的描述在 prompt 位、产物不是 Music3 的 caption 形状
	if got := DefaultEnhanceTemplate("ace"); got != "" {
		t.Error("ACE-Step 不该拿到 Music3 模板")
	}
	// 6) H3 那条不受影响
	if got := DefaultEnhanceTemplate("minimax-h3-fl2va"); got != defaultH3EnhanceTemplate {
		t.Error("H3 模板分派被音乐分支影响了")
	}
}

func TestMusic3LyricsInstrumental(t *testing.T) {
	cases := []struct {
		name   string
		lyrics string
		want   bool
	}{
		{"空", "", true},
		{"只有标签", "[Intro]\n[Chorus]\n[Outro]", true},
		{"带修饰的标签", "[Verse 1: soft]\n\n[Chorus]", true},
		{"有唱词", "[Verse]\n雨落在霓虹上", false},
		{"拉丁字母唱词", "[Verse]\nrain on the neon", false},
	}
	for _, c := range cases {
		if got := Music3LyricsInstrumental(c.lyrics); got != c.want {
			t.Errorf("%s: 期望 %v,实得 %v", c.name, c.want, got)
		}
	}
}

// 事实块要求:有没有唱词、section 顺序、还剩多少字符预算。
// 这三件事模型都猜不对,而猜错不报错、只是默默变差。
func TestBuildMusic3EnhanceContext(t *testing.T) {
	withLabel := BuildMusic3EnhanceContext("a warm acoustic pop song", "[Verse]\n雨落在霓虹上\n[Chorus]\n[Chorus]")
	for _, must := range []string{
		"Write the caption in English",
		"not one word of them may appear",
		"Section tags present, in order: Verse, Chorus",
		"Caption budget:",
	} {
		if !strings.Contains(withLabel, must) {
			t.Errorf("事实块缺少 %q:\n%s", must, withLabel)
		}
	}
	// 去重:出现两次的 [Chorus] 只列一次
	if strings.Count(withLabel, "Chorus") != 1 {
		t.Errorf("section 标签没有去重:\n%s", withLabel)
	}

	instrumental := BuildMusic3EnhanceContext("lofi beat", "[Intro]\n[Chorus]")
	if !strings.Contains(instrumental, "no sung words") {
		t.Errorf("无唱词时没有告诉模型按器乐处理:\n%s", instrumental)
	}

	noLyrics := BuildMusic3EnhanceContext("piano solo", "")
	if !strings.Contains(noLyrics, "No lyrics were supplied") {
		t.Errorf("空歌词没有走器乐那支:\n%s", noLyrics)
	}
	if strings.Contains(noLyrics, "Section tags") {
		t.Errorf("没有歌词时不该出现 section 标签事实:\n%s", noLyrics)
	}

	// 预算按 rune 算,且扣掉歌词:16000 字歌词直接把余量打穿。
	huge := BuildMusic3EnhanceContext("x", strings.Repeat("雨", common.Music3CaptionBudget("")+10))
	if !strings.Contains(huge, "already fill the engine's joint prompt budget") {
		t.Errorf("歌词超预算时没有提示压缩:\n%s", huge)
	}
}

// 音乐 caption 的产物比图文改写长一个量级,预算必须放开到 30s;
// 但**配置只能调小**这条规则不变(与 textBudget 的原意一致)。
func TestTextBudgetForMusic(t *testing.T) {
	if got := textBudget(nil, music3EnhanceTimeout); got != 30*time.Second {
		t.Errorf("音乐默认预算应为 30s,实得 %v", got)
	}
	if got := textBudget(nil, enhanceTimeout); got != enhanceTimeout {
		t.Errorf("非音乐仍应为 20s,实得 %v", got)
	}
	small := &common.AggregatePromptEnhance{TimeoutSeconds: 10}
	if got := textBudget(small, music3EnhanceTimeout); got != 10*time.Second {
		t.Errorf("配置调小应生效,实得 %v", got)
	}
	large := &common.AggregatePromptEnhance{TimeoutSeconds: 120}
	if got := textBudget(large, music3EnhanceTimeout); got != 30*time.Second {
		t.Errorf("配置不能把音乐预算放大到 30s 以上,实得 %v", got)
	}
}

// 歌词必须作为独立块进 user 消息,而不是接在描述后面当"更多描述"。
func TestUserPromptCarriesLyricsSeparately(t *testing.T) {
	got := userPromptWithLyrics("a warm ballad", "[Verse]\n雨落在霓虹上")
	if !strings.HasPrefix(got, "a warm ballad") {
		t.Fatalf("描述没有放在最前:\n%s", got)
	}
	if !strings.Contains(got, "=== LYRICS") || !strings.Contains(got, "=== END LYRICS ===") {
		t.Errorf("缺少歌词分隔标记,模型会把歌词当描述继续扩写:\n%s", got)
	}
	if strings.Index(got, "=== LYRICS") > strings.Index(got, "雨落在霓虹上") {
		t.Error("歌词出现在了分隔标记之前")
	}
	if got := userPromptWithLyrics("piano solo", "  "); got != "piano solo" {
		t.Errorf("空歌词不该拼空块,实得 %q", got)
	}
}
