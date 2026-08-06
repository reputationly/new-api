package gpustackplus

import (
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// task_type 解析链的回归测试。核心命题:玩法是请求的属性,不是模型的属性 ——
// 同一个部署服务多种玩法时,判据必须来自「体验区配置声明的候选集 ∩ 请求输入形态」,
// 而不是模型名里的 token。名字推断只在模型没配进体验区时兜底。

func setVideoConfig(t *testing.T, raw string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	common.OptionMap["VideoModelConfig"] = raw
	common.OptionMap["ImageModelSizeConfig"] = ""
	common.OptionMap["AudioModelConfig"] = ""
	common.OptionMap["MusicModelConfig"] = ""
	common.OptionMapRWMutex.Unlock()
}

// 一个部署同时服务文生 / 图生 / 首尾帧 —— 这是我们期望的部署形态(省显存),
// 模型名里不可能编码出"这一次是哪种玩法"。这里模型名故意起成完全无特征的
// "video-pro":名字推断只会给出 t2v 兜底,全靠输入形态区分。
const multiPlayConfig = `{"models":{"video-pro":{"tabs":{"text2video":{},"flf2v":{}}}}}`

func TestResolveByInputShape_MultiPlayModel(t *testing.T) {
	setVideoConfig(t, multiPlayConfig)

	cases := []struct {
		name string
		req  relaycommon.TaskSubmitReq
		want string
	}{
		{"无输入 → 文生视频", relaycommon.TaskSubmitReq{}, "t2v"},
		{"1 张首帧图 → 图生视频", relaycommon.TaskSubmitReq{Images: []string{"a"}}, "i2v"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := taskTypeOfRequest(&tc.req, "video-pro", "video-pro")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// 2 张图对 i2v(只吃首帧、多余忽略)和 flf2v(首帧+尾帧)都成立,是真歧义。
// 期望明确报错要求显式指定,而不是默默猜一个发上去。
func TestResolveAmbiguous_TwoImagesNeedsExplicit(t *testing.T) {
	setVideoConfig(t, multiPlayConfig)
	req := relaycommon.TaskSubmitReq{Images: []string{"a", "b"}}
	_, err := taskTypeOfRequest(&req, "video-pro", "video-pro")
	if err == nil {
		t.Fatal("2 张图在 i2v/flf2v 间有歧义,应报错")
	}
	if !strings.Contains(err.Error(), "metadata.task_type") {
		t.Fatalf("报错应指明该传什么,得到: %v", err)
	}
}

// 同样是 2 张图,但模型名带任务标识:名字作为最后一道裁决收口,不报错。
// 这是迁移后的存量形态 —— 老「首尾帧」模型被扇出成空的 tabs.flf2v(候选集 {flf2v,i2v}),
// 直连 API 发首帧+尾帧改造前一直能跑,不裁决就会成片打 400。
func TestNameBreaksTieWithinCandidates(t *testing.T) {
	setVideoConfig(t, `{"models":{"wan2.2-flf2v-a14b":{"tabs":{"flf2v":{}}}}}`)
	req := relaycommon.TaskSubmitReq{Images: []string{"a", "b"}}
	got, err := taskTypeOfRequest(&req, "wan2.2-flf2v-a14b", "wan2.2-flf2v-a14b")
	if err != nil {
		t.Fatalf("名字含 flf2v,应由名字裁决而非报错: %v", err)
	}
	if got != "flf2v" {
		t.Fatalf("got %q, want flf2v", got)
	}
}

// 名字裁决只在「与本次输入相容」的候选里做:输入形态与该模型声明的任何玩法都不匹配时,
// 名字给的答案同样发不得,仍要报错。
func TestNameDoesNotBreakTieWhenNothingCompatible(t *testing.T) {
	// 只挂「关键帧」(候选集 {flf2v,i2v}),却收到 Bernini 参考图 → 兼容集为空。
	setVideoConfig(t, `{"models":{"wan2.2-flf2v-a14b":{"tabs":{"flf2v":{}}}}}`)
	req := relaycommon.TaskSubmitReq{
		Metadata: map[string]any{"src_ref_images": []any{"a"}},
	}
	if _, err := taskTypeOfRequest(&req, "wan2.2-flf2v-a14b", "wan2.2-flf2v-a14b"); err == nil {
		t.Fatal("输入形态与声明的任何玩法都不匹配,不该拿名字兜底")
	}
}

// tab 条目声明 taskType 是消歧的第一手段:运营在体验区管理页指明这个模型的
// 「关键帧」格按 flf2v 处理,候选集就收敛到 {t2v, flf2v},2 张图不再有歧义。
func TestTabDeclaredTaskTypeResolvesAmbiguity(t *testing.T) {
	setVideoConfig(t, `{"models":{"video-pro":{"tabs":{"text2video":{},"flf2v":{"taskType":"flf2v"}}}}}`)
	req := relaycommon.TaskSubmitReq{Images: []string{"a", "b"}}
	got, err := taskTypeOfRequest(&req, "video-pro", "video-pro")
	if err != nil {
		t.Fatalf("声明了 taskType 就不该有歧义: %v", err)
	}
	if got != "flf2v" {
		t.Fatalf("got %q, want flf2v", got)
	}
}

// 显式 metadata.task_type 优先级最高,连配置都不查。
func TestExplicitTaskTypeWins(t *testing.T) {
	setVideoConfig(t, multiPlayConfig)
	req := relaycommon.TaskSubmitReq{
		Images:   []string{"a", "b"},
		Metadata: map[string]any{"task_type": "flf2v"},
	}
	got, err := taskTypeOfRequest(&req, "video-pro", "video-pro")
	if err != nil || got != "flf2v" {
		t.Fatalf("got %q err %v, want flf2v", got, err)
	}
}

// 模型没配进体验区(纯直连模型):退回名字推断,维持改造前语义,不能报错。
func TestUnconfiguredModelFallsBackToNameInference(t *testing.T) {
	setVideoConfig(t, `{"models":{}}`)
	req := relaycommon.TaskSubmitReq{Images: []string{"a", "b"}}
	got, err := taskTypeOfRequest(&req, "wan2.2-flf2v-a14b", "wan2.2-flf2v-a14b")
	if err != nil {
		t.Fatalf("未配置模型不该报错: %v", err)
	}
	if got != "flf2v" {
		t.Fatalf("got %q, want flf2v(由名字推断)", got)
	}
}

// 单玩法模型:候选集唯一,输入形态都不用看 —— 名字再没特征也能定。
func TestSingleTabModelResolvesWithoutName(t *testing.T) {
	setVideoConfig(t, `{"models":{"nameless-deploy":{"tabs":{"s2v":{}}}}}`)
	req := relaycommon.TaskSubmitReq{}
	got, err := taskTypeOfRequest(&req, "nameless-deploy", "nameless-deploy")
	if err != nil || got != "s2v" {
		t.Fatalf("got %q err %v, want s2v", got, err)
	}
}

// 参考图与首帧图落在不同的键上,"都是一张图"并不代表分不开。
// 两个键同时给才是真歧义。
func TestRefImageVsFirstFrameAreSeparableByKey(t *testing.T) {
	setVideoConfig(t, `{"models":{"m":{"tabs":{"flf2v":{"taskType":"i2v"},"image2video":{}}}}}`)

	// 顶层图 → i2v(r2v 不读这个键)
	req := relaycommon.TaskSubmitReq{Images: []string{"a"}}
	if got, err := taskTypeOfRequest(&req, "m", "m"); err != nil || got != "i2v" {
		t.Fatalf("顶层图应判 i2v,得到 %q err %v", got, err)
	}
	// 参考图 → r2v(i2v 不读这个键)
	req = relaycommon.TaskSubmitReq{Metadata: map[string]any{"src_ref_images": []any{"a"}}}
	if got, err := taskTypeOfRequest(&req, "m", "m"); err != nil || got != "r2v" {
		t.Fatalf("参考图应判 r2v,得到 %q err %v", got, err)
	}
	// 两个键都给 → 谁都不兼容(各自都有"外来输入"),报错要求显式指定
	req = relaycommon.TaskSubmitReq{
		Images:   []string{"a"},
		Metadata: map[string]any{"src_ref_images": []any{"b"}},
	}
	if _, err := taskTypeOfRequest(&req, "m", "m"); err == nil {
		t.Fatal("首帧图与参考图混用应报错")
	}
}

// 输入形态相同、只能靠显式 task_type 分的两处,确认会走到报错而不是猜。
func TestKnownIndistinguishablePairs(t *testing.T) {
	// metadata.video 单独出现:超分(sr)与配乐(v2a)输入完全一致。
	// 这两个 task_type 分属不同 tab(sr 无 tab、dub 有),用声明字段构造出候选集。
	setVideoConfig(t, `{"models":{"m":{"tabs":{"dub":{},"vace":{"taskType":"sr"}}}}}`)
	req := relaycommon.TaskSubmitReq{Metadata: map[string]any{"video": "u"}}
	if _, err := taskTypeOfRequest(&req, "m", "m"); err == nil {
		t.Fatal("sr 与 v2a 输入同形,应报错要求显式指定")
	}

	// src_video 恰好 2 个:多源编辑(mv2v)与广告植入(ads2v)输入完全一致。
	setVideoConfig(t, `{"models":{"b":{"tabs":{"vace":{}}}}}`)
	req = relaycommon.TaskSubmitReq{Metadata: map[string]any{"src_video": []any{"v1", "v2"}}}
	if _, err := taskTypeOfRequest(&req, "b", "b"); err == nil {
		t.Fatal("mv2v 与 ads2v 输入同形,应报错要求显式指定")
	}
}

// 非视频大类不做输入形态推导,行为与改造前一致(音乐 t2m/t2a/svs 都是纯文本,
// 区分度不足;语音四个 tab 共用 tts,候选集本就唯一)。
func TestNonVideoCategoriesKeepLegacyBehaviour(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	common.OptionMap["VideoModelConfig"] = ""
	common.OptionMap["MusicModelConfig"] = `{"models":{"acestep-v1":{"tabs":{"t2m":{},"cover":{},"repaint":{}}}}}`
	common.OptionMapRWMutex.Unlock()

	req := relaycommon.TaskSubmitReq{}
	got, err := taskTypeOfRequest(&req, "acestep-v1", "acestep-v1")
	if err != nil {
		t.Fatalf("音乐多 tab 模型不该报错(维持改造前行为): %v", err)
	}
	if got != "t2m" {
		t.Fatalf("got %q, want t2m(由名字推断)", got)
	}
}
