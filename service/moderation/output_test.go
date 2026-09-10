package moderation

import (
	"context"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/QuantumNous/new-api/types"
)

func TestOutputMediaURL(t *testing.T) {
	ctx := context.Background()
	task := &model.Task{TaskID: "abc123"}

	// **fixture 必须是 BuildProxyURL 真正产出的那个串。**
	//
	// 上一版这里写的是 "/v1/video/proxy/abc123"——相对路径，而且这条路由压根不存在
	// （真实路由是 /v1/videos/:task_id/content）。于是测试绿着，而线上 BuildProxyURL
	// 返回的绝对地址被「上游直链」分支照单全收，upstreamURL 那条回落永远走不到。
	proxy := taskcommon.BuildProxyURL(task.TaskID)
	if !strings.HasPrefix(proxy, "http") {
		t.Fatalf("前提变了：BuildProxyURL 不再返回绝对地址（%q），这条测试的立论要重写", proxy)
	}
	if got := outputMediaURL(ctx, task, proxy); got != "" {
		t.Fatalf("本站取件端点送不进模型，应返回空，得到 %q", got)
	}
	if got := outputMediaURL(ctx, task, ""); got != "" {
		t.Fatalf("空 URL 应返回空，得到 %q", got)
	}

	// 上游直链与 data-url 直接透传，判定模型自己拉。
	up := "https://cdn.example.com/out/a.mp4"
	if got := outputMediaURL(ctx, task, up); got != up {
		t.Fatalf("上游直链应原样返回，得到 %q", got)
	}
	data := "data:image/png;base64,AAAA"
	if got := outputMediaURL(ctx, task, data); got != data {
		t.Fatalf("data-url 应原样返回，得到 %q", got)
	}

	// 别的任务的取件端点不该被当成自己的（TaskID 不同即不同）
	other := taskcommon.BuildProxyURL("other-task")
	if got := outputMediaURL(ctx, task, other); got != other {
		t.Fatalf("只按本任务的取件端点判断，得到 %q", got)
	}

	// obs:// 在存储没启用时签不出来。**绝不能把 obs:// 原样返回**——
	// 那是内部占位符，送给模型必然失败，而失败会被记成审核异常，
	// 让「存储没配」这个配置问题伪装成审核服务故障。
	if got := outputMediaURL(ctx, task, "obs://t2i/2026/09/10/1/x.png"); got != "" {
		t.Fatalf("签不出来时应返回空而不是 obs:// 占位符，得到 %q", got)
	}
}

func TestOutputCandidateURLFallsBackToUpstream(t *testing.T) {
	ctx := context.Background()
	task := &model.Task{TaskID: "abc123"}

	// 这是 data: 产物唯一能被审到的路径。
	//
	// 轮询在 data: 分支里把 ResultURL 改写成了 BuildProxyURL（挂在 TokenOrUserAuth
	// 后面，判定节点必然拉失败），真正的内容只在上游那个 data: 里。
	// 认不出这是自家端点的话，Vertex 这类 base64 产物 **100% 漏审**，
	// 而管理端看到的仍然是「产物审核已开启」。
	data := "data:video/mp4;base64,AAAA"
	got := outputCandidateURL(ctx, task, taskcommon.BuildProxyURL(task.TaskID), data)
	if got != data {
		t.Fatalf("ResultURL 送不进模型时应回落到上游地址，得到 %q", got)
	}

	// 反向：ResultURL 本身可用时不能被上游地址顶掉——
	// obs:// 签出来的短时效链接是我们能控的那一份，优先级更高。
	up := "https://cdn.example.com/out/a.mp4"
	if got := outputCandidateURL(ctx, task, up, "https://other.example.com/b.mp4"); got != up {
		t.Fatalf("ResultURL 可用时应优先使用它，得到 %q", got)
	}

	// 两个都拿不到 → 空，由调用方按「跳过并出声」处理
	if got := outputCandidateURL(ctx, task, taskcommon.BuildProxyURL(task.TaskID), ""); got != "" {
		t.Fatalf("两个地址都不可用时应返回空，得到 %q", got)
	}
}

func TestOutputMediaType(t *testing.T) {
	// **fixture 必须用仓库里真实存在的 action 值。**
	//
	// 上一版这条测试喂的是 "i2v" / "t2v" / "generate_video"——三个本仓库从不写入的
	// 取值，于是它绿着，而线上每个视频都被判成图片、抽帧永远不跑。
	// 真实取值见 constant/task.go：视频恒为 generate 系列，图片是 imageGenerate/imageEdit。
	cases := []struct {
		action string
		want   types.FileType
		wantOK bool
	}{
		{constant.TaskActionGenerate, types.FileTypeVideo, true},
		{constant.TaskActionTextGenerate, types.FileTypeVideo, true},
		{constant.TaskActionFirstTailGenerate, types.FileTypeVideo, true},
		{constant.TaskActionReferenceGenerate, types.FileTypeVideo, true},
		{constant.TaskActionRemix, types.FileTypeVideo, true},
		{constant.TaskActionImageGenerate, types.FileTypeImage, true},
		{constant.TaskActionImageEdit, types.FileTypeImage, true},
		// Suno 音乐/歌词是音频，本期明确不审（§2.3）。拿视觉模型判只会误判，
		// 而误判在 blocking 下就是把正常的音乐任务判失败。
		{constant.SunoActionMusic, "", false},
		{constant.SunoActionLyrics, "", false},
		// 未知 action 同样不送审：宁可漏也不能拿视觉模型去判一个不知道是什么的东西。
		{"someNewAction", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		task := &model.Task{Action: c.action}
		got, ok := outputMediaType(task)
		if ok != c.wantOK || got != c.want {
			t.Errorf("action=%q → (%q, %v), want (%q, %v)", c.action, got, ok, c.want, c.wantOK)
		}
	}
}

func TestOutputModeIsIndependentFromInput(t *testing.T) {
	// 两个开关必须独立。灰度期几乎必然要用「输入拦、产物只观察」这种组合——
	// 共用一个 mode 就做不到，而产物审核的准召比输入侧更没底
	// （它判的是我们自己生成的内容，误杀直接变成交付失败）。
	s := system_setting.GetModerationSettings()
	origMode, origOut, origEndpoints := s.Mode, s.OutputMode, s.Endpoints
	t.Cleanup(func() { s.Mode, s.OutputMode, s.Endpoints = origMode, origOut, origEndpoints })

	s.Endpoints = []system_setting.ModerationEndpoint{{
		Name: "img", BaseURL: "http://x", Model: "m",
		Modality: ModalityImage, Enabled: true,
	}}

	s.Mode = system_setting.ModerationModeBlocking
	s.OutputMode = system_setting.ModerationModeOff
	if !MediaActive("default", "gpt-4o") {
		t.Fatal("输入侧开着时 MediaActive 应为 true")
	}
	if OutputMediaActive("default", "gpt-4o") {
		t.Fatal("产物侧关着时 OutputMediaActive 必须为 false——两个开关不能串")
	}

	// 反向：产物开、输入关
	s.Mode = system_setting.ModerationModeOff
	s.OutputMode = system_setting.ModerationModeBlocking
	if MediaActive("default", "gpt-4o") {
		t.Fatal("输入侧关着时 MediaActive 必须为 false")
	}
	if !OutputMediaActive("default", "gpt-4o") {
		t.Fatal("产物侧开着时 OutputMediaActive 应为 true")
	}

	// OutputMode 零值必须按 off：升级后默认不开，不能有意外行为
	s.OutputMode = ""
	if OutputMediaActive("default", "gpt-4o") {
		t.Fatal("OutputMode 零值必须按关闭处理，否则升级后产物审核会自己开起来")
	}
}

func TestMediaStageDefaultsToInput(t *testing.T) {
	// 第二期的调用方不传 Stage，存量记录也没有这个区分——零值必须落回输入侧，
	// 否则那些记录会被算进产物统计里。
	if got := mediaStage(""); got != StageInputMedia {
		t.Fatalf("零值阶段应按输入侧，得到 %q", got)
	}
	if got := mediaStage(StageOutput); got != StageOutput {
		t.Fatalf("产物阶段应保留，得到 %q", got)
	}
}
