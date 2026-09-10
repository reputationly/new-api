package moderation

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/setting/system_setting"
)

// 打真实 ShieldGemma 节点的集成测试。默认跳过，需要时：
//
//	MODERATION_IT_ENDPOINT=http://127.0.0.1:40035 go test ./service/moderation/ -run Integration -v
//
// 存在的理由：单测覆盖的是「拿到响应之后怎么解释」，覆盖不了「我们发出去的请求
// 长得对不对」。而这一层最容易错的恰恰是后者——chat_template 要求 content 必须是
// 数组、图片必须排在文本之前，写反了不会报错，只会让判定悄悄失真。

func itEndpoint(t *testing.T) system_setting.ModerationEndpoint {
	base := os.Getenv("MODERATION_IT_ENDPOINT")
	if base == "" {
		t.Skip("未设置 MODERATION_IT_ENDPOINT，跳过集成测试")
	}
	model := os.Getenv("MODERATION_IT_MODEL")
	if model == "" {
		model = "shieldgemma2"
	}
	return system_setting.ModerationEndpoint{
		Name:      "it",
		BaseURL:   base,
		Model:     model,
		Modality:  ModalityImage,
		TimeoutMS: 20000,
		Enabled:   true,
	}
}

// withEndpoints 把节点装进全局配置并在结束后还原。
func withEndpoints(t *testing.T, eps ...system_setting.ModerationEndpoint) {
	s := system_setting.GetModerationSettings()
	orig := s.Endpoints
	s.Endpoints = eps
	t.Cleanup(func() { s.Endpoints = orig })
}

func TestIntegrationScoreImage(t *testing.T) {
	ep := itEndpoint(t)
	withEndpoints(t, ep)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	scores, err := scoreImage(ctx, testImagePNG)
	if err != nil {
		t.Fatalf("scoreImage 失败: %v", err)
	}
	// 三条策略必须都有结果：少一条就说明缓存会存进残缺的分数，
	// 换个严格度命中它就会判错。
	if len(scores) != len(shieldGemmaPolicies) {
		t.Fatalf("应得到 %d 条策略的分数，得到 %d: %+v", len(shieldGemmaPolicies), len(scores), scores)
	}
	for _, p := range shieldGemmaPolicies {
		v, ok := scores[p.Key]
		if !ok {
			t.Fatalf("缺少策略 %s 的分数", p.Key)
		}
		if v < 0 || v > 1 {
			t.Fatalf("策略 %s 的 P(Yes)=%v 不是合法概率", p.Key, v)
		}
		// 一张纯灰图不该被判违规。这条同时验证了「我们发出去的 prompt 是对的」——
		// content 数组顺序写反时模型的输出会明显漂移。
		if v > 0.5 {
			t.Errorf("纯灰图在策略 %s 上 P(Yes)=%.4f，超过 0.5：prompt 组装可能有问题", p.Key, v)
		}
		t.Logf("策略 %-10s P(Yes)=%.6f", p.Key, v)
	}
}

func TestIntegrationVerdictEndToEnd(t *testing.T) {
	ep := itEndpoint(t)
	withEndpoints(t, ep)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	policy := &system_setting.ModerationPolicy{
		Name:       "it",
		Strictness: system_setting.StrictnessStandard,
		Categories: map[string]string{
			system_setting.CategorySexual:  system_setting.CategoryActionBlock,
			system_setting.CategoryIllegal: system_setting.CategoryActionBlock,
			system_setting.CategoryViolent: system_setting.CategoryActionBlock,
		},
	}
	m := shieldGemmaModerator{strictness: system_setting.StrictnessStandard, policy: policy}

	v, err := m.ModerateImage(ctx, testImagePNG, "")
	if err != nil {
		t.Fatalf("ModerateImage 失败: %v", err)
	}
	if v.Action != ActionPass {
		t.Fatalf("纯灰图应放行，得到 %v (score=%.4f, cats=%v)", v.Action, v.Score, v.Categories)
	}

	// 违规分支：没有违规样本，只能把分数抬到 1 来走通它。判定链、类别映射、
	// 拒绝文案全都会被真实执行，唯一被模拟的是「模型给出高分」这一步。
	//
	// 这也是上线前 block 路径唯一被执行过的形式——真实召回仍未验证（§15.9）。
	scores, err := cachedImageScores(ctx, testImagePNG, "")
	if err != nil {
		t.Fatalf("取分数失败: %v", err)
	}
	if got := m.verdictFromScores(maxScores(scores)); got.Action != ActionBlock {
		t.Fatalf("分数为 1 时应判 block，得到 %v", got.Action)
	}
}

// maxScores 把每条策略的分数抬到 1，用于走通 block 分支。
// 不改 yesThreshold 本身——那是生产代码，测试不该为了自己好走而给它开后门。
func maxScores(in imageScores) imageScores {
	out := make(imageScores, len(in))
	for k := range in {
		out[k] = 1
	}
	return out
}

func TestIntegrationVideoFrames(t *testing.T) {
	ep := itEndpoint(t)
	withEndpoints(t, ep)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("本机没有 ffmpeg，跳过视频集成测试")
	}

	// 造一段 6 秒的测试视频：三个抽帧位置会落在不同画面上。
	dir := t.TempDir()
	video := dir + "/it.mp4"
	mk := exec.Command("ffmpeg", "-v", "error",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=25:duration=6",
		"-c:v", "libx264", "-preset", "ultrafast", video, "-y")
	if out, err := mk.CombinedOutput(); err != nil {
		t.Skipf("造测试视频失败，跳过: %v (%s)", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	frames, err := extractFrames(ctx, video, "file")
	if err != nil {
		t.Fatalf("抽帧失败: %v", err)
	}
	if len(frames) != 3 {
		t.Fatalf("6 秒视频应抽出三帧，得到 %d 帧", len(frames))
	}
	for i, f := range frames {
		if len(f) < 100 || f[:22] != "data:image/png;base64," {
			t.Fatalf("第 %d 帧不是合法的 PNG data-url: %.40s", i, f)
		}
	}

	// 本地文件路径必须被拒绝：ExtractVideoFrames 只认 data-url 与 http(s)。
	// 放开它等于让用户可控的字符串变成任意文件读取的入口。
	if _, err := ExtractVideoFrames(ctx, video); err == nil {
		t.Fatal("本地文件路径必须被协议白名单拒绝")
	}

	// 走真实形态：非白名单渠道的视频是 data-url，这条路会落临时文件再抽帧。
	raw, err := os.ReadFile(video)
	if err != nil {
		t.Fatalf("读测试视频失败: %v", err)
	}
	dataURL := "data:video/mp4;base64," + base64.StdEncoding.EncodeToString(raw)

	// 抽出的帧要真能被模型吃下去——这是整条视频链路唯一能一次验完的地方。
	scores, err := scoreVideo(ctx, dataURL)
	if err != nil {
		t.Fatalf("scoreVideo 失败: %v", err)
	}
	if len(scores) != 3 {
		t.Fatalf("应得到三帧的分数，得到 %d", len(scores))
	}
	for i, s := range scores {
		if len(s) != len(shieldGemmaPolicies) {
			t.Fatalf("第 %d 帧只有 %d 条策略分数", i, len(s))
		}
		t.Logf("帧 %d: %+v", i, s)
	}
}
