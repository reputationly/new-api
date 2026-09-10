package moderation

import (
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// 配置链路的往返测试。
//
// 存在的理由是一次真实的翻车：L2 的判定器、媒体入口、三个挂载点、缓存、视频抽帧
// 全部做完并且集成测试全绿，但**没有任何用户能启用它**——配置页上没有模态选择器，
// `ImageEndpoints()` 恒为空，于是 `MediaActive()` 恒为 false。
//
// 当时的集成测试直接给 `s.Endpoints` 赋值，绕过了「前端提交 → 校验加密 → 落库 →
// 读回」这条生产真正走的链路，所以照样全绿。这个文件补的就是那一段：
// 只要 modality 在链路上任何一环丢失，这里就会红。

// saveAndLoad 模拟一次完整的保存：前端提交的 JSON → 校验加密 → 落库 → config
// manager 反序列化回内存单例。返回落库的那份 JSON（供回显测试用）。
func saveAndLoad(t *testing.T, raw string) string {
	t.Helper()
	stored, err := system_setting.EncryptModerationEndpoints(raw)
	if err != nil {
		t.Fatalf("保存被拒绝: %v", err)
	}
	var eps []system_setting.ModerationEndpoint
	if err := common.UnmarshalJsonStr(stored, &eps); err != nil {
		t.Fatalf("落库的 JSON 读不回来: %v", err)
	}
	system_setting.GetModerationSettings().Endpoints = eps
	return stored
}

func TestImageEndpointSurvivesTheSavePath(t *testing.T) {
	s := system_setting.GetModerationSettings()
	origEndpoints, origMode := s.Endpoints, s.Mode
	t.Cleanup(func() { s.Endpoints, s.Mode = origEndpoints, origMode })
	s.Mode = system_setting.ModerationModeBlocking

	// 配置页提交上来的形态：一个文本节点 + 一个图片节点。
	raw := `[
		{"name":"guard","base_url":"http://127.0.0.1:8000","model":"qwen3guard",
		 "modality":"text","timeout_ms":3000,"input_limit":24000,"enabled":true},
		{"name":"sg2","base_url":"http://127.0.0.1:8001","model":"shieldgemma2",
		 "modality":"image","timeout_ms":10000,"enabled":true}
	]`
	stored := saveAndLoad(t, raw)

	// 一、两类节点必须各归各的。混在一起会让文本审核拿视觉模型去判、反之亦然。
	if got := len(s.TextEndpoints()); got != 1 {
		t.Fatalf("应有 1 个文本节点，得到 %d", got)
	}
	if got := len(s.ImageEndpoints()); got != 1 {
		t.Fatalf("应有 1 个图片节点，得到 %d —— modality 在保存链路上丢了，"+
			"整条图片/视频审核链会静默失效", got)
	}
	if s.ImageEndpoints()[0].Model != "shieldgemma2" {
		t.Fatalf("图片节点认错了: %+v", s.ImageEndpoints()[0])
	}

	// 二、这才是真正要守的那条：配完之后媒体审核必须真的会执行。
	// 上一版正是这一步恒为 false，而所有单测和集成测试都没发现。
	if !MediaActive("default", "gpt-4o") {
		t.Fatal("配好图片节点后 MediaActive 仍为 false —— 功能在产品上不可达")
	}

	// 三、回显不能把 modality 弄丢。前端 GET 回来的就是这份，
	// 用户改个超时再保存，丢了的字段就永久消失了。
	redacted := system_setting.RedactModerationEndpoints(stored)
	if !strings.Contains(redacted, `"modality":"image"`) {
		t.Fatalf("回显丢了 modality: %s", redacted)
	}

	// 四、把回显的内容原样提交回去（用户只改了别的字段时的真实操作），
	// 配置不能坏——尤其图片节点不能退化成文本节点。
	saveAndLoad(t, redacted)
	if got := len(s.ImageEndpoints()); got != 1 {
		t.Fatalf("回显后原样保存一次，图片节点就没了（剩 %d 个）", got)
	}
}

func TestSavePathRejectsBrokenEndpoints(t *testing.T) {
	// 这些都是「保存成功但审核必然失败」的配置，必须在保存这一步就拒绝——
	// 拦截模式下它们的后果不是「这个节点不可用」，是全站拒绝。
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			"启用却没填地址",
			`[{"name":"sg2","model":"shieldgemma2","modality":"image","enabled":true}]`,
			"地址或模型名为空",
		},
		{
			"没填名称",
			`[{"base_url":"http://x","model":"m","modality":"image","enabled":true}]`,
			"没有填名称",
		},
		{
			"名称重复",
			`[{"name":"a","base_url":"http://x","model":"m","enabled":true},
			  {"name":"a","base_url":"http://y","model":"n","modality":"image","enabled":true}]`,
			"名称重复",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := system_setting.EncryptModerationEndpoints(c.raw)
			if err == nil {
				t.Fatal("这份配置必须被拒绝")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("错误信息应说清原因（期望含 %q）: %v", c.want, err)
			}
		})
	}
}

func TestSavePathRefusesCredentialWithoutKey(t *testing.T) {
	// 没配 MODERATION_ENCRYPT_KEY 时带凭证的保存必须失败，而不是「成功」一次。
	// 用随机密钥加密的值重启后解不开，而运营看到的是保存成功——
	// 这正是 common/obs_crypto.go:36 那条路的失效方式。
	if os.Getenv("MODERATION_ENCRYPT_KEY") != "" {
		t.Skip("本机配了 MODERATION_ENCRYPT_KEY，跳过")
	}
	if common.ModerationKeyReady() {
		t.Skip("密钥已就绪，跳过")
	}
	raw := `[{"name":"sg2","base_url":"http://x","model":"m","modality":"image",
	          "api_key":"sk-secret","enabled":true}]`
	_, err := system_setting.EncryptModerationEndpoints(raw)
	if err == nil {
		t.Fatal("缺少加密密钥时带凭证的保存必须被拒绝")
	}
	if !strings.Contains(err.Error(), "MODERATION_ENCRYPT_KEY") {
		t.Fatalf("错误信息要指明缺的是哪个环境变量: %v", err)
	}
}
