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

// TestDialectSurvivesTheSavePath dialect 必须走通「提交 → 校验加密 → 落库 → 读回 → 回显」
// 这条链路，并真的换掉判定器。
//
// 与上面那条 modality 测试同一个理由：dialect 在链路上任何一环丢掉，表现都是
// **审核看起来在跑、实际每次判定都 ActionError**，而 FailOpen 默认开着，
// 于是静默全量放行。配置页上却显示选的是新协议。
func TestDialectSurvivesTheSavePath(t *testing.T) {
	s := system_setting.GetModerationSettings()
	origEndpoints, origMode := s.Endpoints, s.Mode
	t.Cleanup(func() { s.Endpoints, s.Mode = origEndpoints, origMode })
	s.Mode = system_setting.ModerationModeBlocking

	raw := `[{"name":"zs","base_url":"http://127.0.0.1:8000","model":"Zhongsen-Text-8b",
	          "modality":"text","dialect":"zhongsen-text","timeout_ms":3000,
	          "input_limit":1500,"enabled":true}]`
	stored := saveAndLoad(t, raw)

	if got := s.TextDialect(); got != system_setting.DialectZhongsenText {
		t.Fatalf("生效的文本协议应为 zhongsen-text，得到 %q —— dialect 在保存链路上丢了", got)
	}
	// 真正要守的那条：判定器必须换成对应的解析器。
	if got := resolveTextDialect(s.TextDialect()).Name(); got != system_setting.DialectZhongsenText {
		t.Fatalf("判定器没换过来，得到 %q", got)
	}

	// 回显不能丢 dialect：前端 GET 回来的就是这份，用户改个超时再保存，
	// 丢了的字段会永久消失——而回落值是 qwen3guard，于是协议被静默换回旧的。
	redacted := system_setting.RedactModerationEndpoints(stored)
	if !strings.Contains(redacted, `"dialect":"zhongsen-text"`) {
		t.Fatalf("回显丢了 dialect: %s", redacted)
	}
	saveAndLoad(t, redacted)
	if got := s.TextDialect(); got != system_setting.DialectZhongsenText {
		t.Fatalf("回显后原样保存一次，协议就退回了 %q", got)
	}
}

// TestLegacyEndpointsKeepQwen3Guard 存量配置（没有 dialect 字段）必须继续走 qwen3guard。
//
// 这是升级安全性的底线：接 dialect 之前所有节点跑的都是 Qwen3Guard，
// 零值回落到别的协议就等于一次升级静默换掉了全站的审核模型。
func TestLegacyEndpointsKeepQwen3Guard(t *testing.T) {
	s := system_setting.GetModerationSettings()
	origEndpoints := s.Endpoints
	t.Cleanup(func() { s.Endpoints = origEndpoints })

	// 接 dialect 之前落库的形态：没有 dialect 键。
	raw := `[
		{"name":"guard","base_url":"http://127.0.0.1:8000","model":"qwen3guard",
		 "modality":"text","timeout_ms":3000,"input_limit":24000,"enabled":true},
		{"name":"sg2","base_url":"http://127.0.0.1:8001","model":"shieldgemma2",
		 "modality":"image","timeout_ms":10000,"enabled":true}
	]`
	saveAndLoad(t, raw)

	if got := s.TextDialect(); got != system_setting.DialectQwen3Guard {
		t.Fatalf("存量文本节点应回落到 qwen3guard，得到 %q", got)
	}
	if got := s.ImageDialect(); got != system_setting.DialectShieldGemma2 {
		t.Fatalf("存量图片节点应回落到 shieldgemma2，得到 %q", got)
	}
}

// TestSavePathRejectsMixedDialects 同模态下启用节点的协议必须唯一。
//
// 混用不是「更灵活」，是判定不可复现：节点轮换决定了同一段文本这次可能由
// qwen3guard 判、下次由 zhongsen 判，而两者的严格度语义根本不同。
func TestSavePathRejectsMixedDialects(t *testing.T) {
	raw := `[
		{"name":"old","base_url":"http://x","model":"qwen3guard",
		 "modality":"text","dialect":"qwen3guard","enabled":true},
		{"name":"new","base_url":"http://y","model":"Zhongsen-Text-8b",
		 "modality":"text","dialect":"zhongsen-text","enabled":true}
	]`
	_, err := system_setting.EncryptModerationEndpoints(raw)
	if err == nil {
		t.Fatal("同模态混用协议必须被拒绝")
	}
	// 报错必须告诉运营怎么办，否则「切换模型」这个最常见的操作会卡在这里。
	if !strings.Contains(err.Error(), "先停用") {
		t.Fatalf("错误信息要给出切换方式: %v", err)
	}

	// 反面：**停用的节点不参与这条检查**。切换模型的正常操作就是
	// 「新节点先配好停用着 → 停用旧节点 → 启用新节点」，
	// 如果停用的也算进来，这条路就走不通了。
	staged := `[
		{"name":"old","base_url":"http://x","model":"qwen3guard",
		 "modality":"text","dialect":"qwen3guard","enabled":true},
		{"name":"new","base_url":"http://y","model":"Zhongsen-Text-8b",
		 "modality":"text","dialect":"zhongsen-text","enabled":false}
	]`
	if _, err := system_setting.EncryptModerationEndpoints(staged); err != nil {
		t.Fatalf("停用的新协议节点不该被拒（切换模型必经的中间态）: %v", err)
	}

	// 不同模态之间互不干扰：文本走 zhongsen、图片走 shieldgemma 是过渡期的常态
	// （本期只做了文本侧）。
	crossModality := `[
		{"name":"zs","base_url":"http://x","model":"Zhongsen-Text-8b",
		 "modality":"text","dialect":"zhongsen-text","enabled":true},
		{"name":"sg2","base_url":"http://y","model":"shieldgemma2",
		 "modality":"image","dialect":"shieldgemma2","enabled":true}
	]`
	if _, err := system_setting.EncryptModerationEndpoints(crossModality); err != nil {
		t.Fatalf("不同模态用不同协议是合法的: %v", err)
	}
}

// TestSavePathRejectsDialectModalityMismatch 协议必须与模态匹配。
func TestSavePathRejectsDialectModalityMismatch(t *testing.T) {
	// 文本节点配了图片协议：请求形状和解析方式都会错，每次判定都失败。
	raw := `[{"name":"a","base_url":"http://x","model":"m",
	          "modality":"text","dialect":"shieldgemma2","enabled":true}]`
	_, err := system_setting.EncryptModerationEndpoints(raw)
	if err == nil {
		t.Fatal("文本节点配图片协议必须被拒绝")
	}
	if !strings.Contains(err.Error(), "不适用") {
		t.Fatalf("错误信息应说明协议与模态不匹配: %v", err)
	}

	// 彻底不存在的协议名同样要拒——多半是手改 options 表或前端传错。
	bogus := `[{"name":"a","base_url":"http://x","model":"m",
	            "modality":"text","dialect":"llamaguard","enabled":true}]`
	if _, err := system_setting.EncryptModerationEndpoints(bogus); err == nil {
		t.Fatal("未知协议名必须被拒绝")
	}

	// 停用的节点也要校验协议合法性：它不参与调用，但**一旦被启用就会**，
	// 而那时的报错会出现在一个完全不相关的保存操作上。
	disabled := `[{"name":"a","base_url":"http://x","model":"m",
	               "modality":"text","dialect":"llamaguard","enabled":false}]`
	if _, err := system_setting.EncryptModerationEndpoints(disabled); err == nil {
		t.Fatal("停用节点的协议名非法同样要在保存时拒绝")
	}
}

// TestSavePathRejectsUnimplementedImageDialect 没有解析实现的协议不能存进配置。
//
// 这是 Codex review 抓到的那个洞的端到端回归：ZSWS 的常量和覆盖表先于实现就位，
// 期间它一度可以被选中并保存。保存成功的后果是给 ZSWS 发 ShieldGemma 的三条策略
// 请求 → 每次 ActionError → FailOpen 默认 true → 图片审核静默停摆，
// 而「测试连接」走的还是 ShieldGemma 协议，照样报绿。
func TestSavePathRejectsUnimplementedImageDialect(t *testing.T) {
	raw := `[{"name":"zsws","base_url":"http://x","model":"ZSWS-Multimodal-4b",
	          "modality":"image","dialect":"zsws-multimodal","enabled":true}]`
	_, err := system_setting.EncryptModerationEndpoints(raw)
	if err == nil {
		t.Fatal("图片侧还没有 dialect 分派，zsws-multimodal 必须在保存时就被拒绝")
	}
	if !strings.Contains(err.Error(), "不适用") {
		t.Fatalf("错误信息应说明该协议当前不可选: %v", err)
	}
}

// TestImageVerdictCarriesDialect 图片判定要能归因到模型。
//
// 空值会让所有图片记录在换模型前后长得一模一样（provider 都是 "L2"），
// 而 detail.dialect 这一列存在的全部理由就是按模型分组算准召。
func TestImageVerdictCarriesDialect(t *testing.T) {
	s := system_setting.GetModerationSettings()
	origEndpoints := s.Endpoints
	t.Cleanup(func() { s.Endpoints = origEndpoints })

	saveAndLoad(t, `[{"name":"sg2","base_url":"http://x","model":"shieldgemma2",
	                  "modality":"image","dialect":"shieldgemma2","enabled":true}]`)

	m := shieldGemmaModerator{
		strictness: system_setting.StrictnessStandard,
		policy: &system_setting.ModerationPolicy{
			Categories: map[string]string{
				system_setting.CategorySexual: system_setting.CategoryActionBlock,
			},
		},
		dialect: s.ImageDialect(),
	}

	// 命中与未命中两条路都要带上 dialect：pass 记录同样要能按模型统计。
	if v := m.verdictFromScores(imageScores{"sexual": 0.99}); v.Dialect != system_setting.DialectShieldGemma2 {
		t.Fatalf("拦截判定应带 dialect，得到 %q", v.Dialect)
	}
	if v := m.verdictFromScores(imageScores{"sexual": 0.01}); v.Dialect != system_setting.DialectShieldGemma2 {
		t.Fatalf("放行判定应带 dialect，得到 %q", v.Dialect)
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
