package model

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
)

// 本包的用例必须在「密钥已配置」的前提下跑，否则留存相关的断言全是空过的：
// 没有密钥时 ContentEnc 恒为空，「observe 不留全文」这种断言不需要代码正确就能通过。
// 密钥读取走 sync.Once，只能在任何用例触发它之前设好，所以放在 init 里。
//
// 「没有密钥」那一支在 service/moderation 的测试进程里验（那里不设这个环境变量）。
func init() {
	os.Setenv("MODERATION_ENCRYPT_KEY",
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
}

// 内容留存是这个功能里唯一有合规后果的部分，此前却是唯一没有测试的部分。
// 外部检视抓出的两条 P1（pass 记录明文留预览、密钥缺失静默丢原文）都出在这里。
// 下面的用例就是为了让同类问题下次在 CI 里就死掉，而不是靠人再读一遍注释。

// TestSetModerationContentRetentionByAction 锁死「哪种判定留哪些内容形态」。
func TestSetModerationContentRetentionByAction(t *testing.T) {
	const plain = "这是一段完整的待审文本"

	cases := []struct {
		action      string
		wantPreview bool
	}{
		// pass 的用途是量抽样率和总量，ContentHash 已经够了。留预览等于把正常
		// 用户的 prompt 头部明文存进一张无需审计就能列出来的表——按默认 1% 抽样，
		// 每 100 个干净请求就漏一个。
		{ModerationActionPass, false},
		// 其余几类的内容本来就是要给人看的，看不到就判不了误杀。
		{ModerationActionBlock, true},
		{ModerationActionReview, true},
		{ModerationActionMask, true},
		{ModerationActionError, true},
	}

	for _, c := range cases {
		m := &ModerationLog{Action: c.action, Enforced: true}
		m.SetModerationContent(plain, plain)

		// ContentHash 对所有判定都要有：重发判定和 hash 黑名单都指望它。
		if m.ContentHash == "" {
			t.Errorf("action=%s: ContentHash 为空", c.action)
		}
		if got := m.Preview != ""; got != c.wantPreview {
			t.Errorf("action=%s: Preview 非空=%v, want %v（Preview=%q）",
				c.action, got, c.wantPreview, m.Preview)
		}
	}
}

// TestPassRecordCarriesNoContent pass 记录不得携带原文的任何可读形态。
func TestPassRecordCarriesNoContent(t *testing.T) {
	const secret = "sk-live-0123456789abcdef 联系电话 13800138000"

	m := &ModerationLog{Action: ModerationActionPass}
	m.SetModerationContent(secret, secret)

	// 这三个字段都会随列表接口原样返回，且列表接口不写审计。
	for name, v := range map[string]string{
		"Preview":    m.Preview,
		"Detail":     m.Detail,
		"ContentEnc": m.ContentEnc,
	} {
		if v != "" {
			t.Errorf("pass 记录的 %s 不应有值，实际 %q", name, v)
		}
	}
	if m.ContentHash == "" {
		t.Error("pass 记录仍需 ContentHash")
	}
}

// TestBlockPreviewIsTruncatedNotRedacted 说明 Preview 的真实语义。
//
// 它是「原文前 160 字符」，不是脱敏结果——注释一度写成「脱敏预览」，
// 而实现从来只做截断。这个用例把实际语义钉住，免得注释再次跑到实现前面去。
func TestBlockPreviewIsTruncatedNotRedacted(t *testing.T) {
	plain := strings.Repeat("赌", previewLimit+50)

	m := &ModerationLog{Action: ModerationActionBlock, Enforced: true}
	m.SetModerationContent(plain, plain)

	runes := []rune(m.Preview)
	// 截断后会补一个省略号。
	if len(runes) != previewLimit+1 {
		t.Fatalf("Preview 长度 %d rune, want %d", len(runes), previewLimit+1)
	}
	// 按字节截断会把多字节字符切成半个，存进 DB 是乱码，运营看不出误杀。
	if !strings.HasPrefix(m.Preview, strings.Repeat("赌", previewLimit)) {
		t.Error("Preview 应当是原文前 160 个完整 rune")
	}
}

// TestObserveOnlyBlockKeepsNoFullContent observe 模式下判了 block 但没真拦，不留全文。
//
// 请求正常返回了结果，把它的完整 prompt 加密留存 180 天说不过去。
// 预览仍然保留——观察期要看误杀，160 字符是那个取舍的下限。
func TestObserveOnlyBlockKeepsNoFullContent(t *testing.T) {
	if !common.ModerationKeyReady() {
		t.Fatal("测试密钥未生效，ContentEnc 恒为空，本用例会空过")
	}

	// 对照组先立起来：没有它，下面的断言在「加密整个坏掉」时也会通过。
	enforced := &ModerationLog{Action: ModerationActionBlock, Enforced: true}
	enforced.SetModerationContent("违规内容", "违规内容")
	if enforced.ContentEnc == "" {
		t.Fatal("真拦下来的请求必须留全文密文")
	}

	observed := &ModerationLog{Action: ModerationActionBlock, Enforced: false}
	observed.SetModerationContent("违规内容", "违规内容")

	if observed.ContentEnc != "" {
		t.Errorf("未执行的判定不应留全文密文，实际 %q", observed.ContentEnc)
	}
	if observed.Preview == "" {
		t.Error("未执行的判定仍要留预览，否则观察期判不了误杀")
	}
	if observed.ContentHash == "" {
		t.Error("ContentHash 与是否执行无关")
	}
}

// TestRecordAuditLogWithAdminInfoPropagatesFailure 审计写入失败必须能被调用方看见。
//
// 旧的 RecordLogWithAdminInfo 只 SysLog 不返回错误，导致「查看原文」在日志库
// 故障期间会变成一次无痕访问——而那条痕是这个权限的全部约束。
func TestRecordAuditLogWithAdminInfoPropagatesFailure(t *testing.T) {
	adminInfo := map[string]interface{}{"action": "moderation_log_view_content"}

	// 成功路径先立个对照，否则下面的失败断言可能只是因为参数本身就不合法。
	if err := RecordAuditLogWithAdminInfo(1, LogTypeManage, "审计对照", adminInfo); err != nil {
		t.Fatalf("正常情况下不应报错: %v", err)
	}

	// 摘掉日志表模拟日志库故障。
	if err := LOG_DB.Migrator().DropTable(&Log{}); err != nil {
		t.Fatalf("drop logs 表失败: %v", err)
	}
	t.Cleanup(func() {
		if err := LOG_DB.AutoMigrate(&Log{}); err != nil {
			t.Fatalf("恢复 logs 表失败: %v", err)
		}
	})

	if err := RecordAuditLogWithAdminInfo(1, LogTypeManage, "审计留痕", adminInfo); err == nil {
		t.Error("日志库故障时 RecordAuditLogWithAdminInfo 必须返回错误，否则调用方无从知道这次访问没留痕")
	}
	// 旧函数保持吞错误的既有语义，不能因为这次改动让一堆非审计调用方开始 panic。
	RecordLogWithAdminInfo(1, LogTypeManage, "普通操作日志", adminInfo)
}

// TestCleanupModerationLogsRetentionTiers 分档清理：Pass 留 3 天，其余留 180 天。
//
// 清理是物理删除，删过头没有第二份可恢复，所以这里连边界也一起钉住。
func TestCleanupModerationLogsRetentionTiers(t *testing.T) {
	if err := DB.AutoMigrate(&ModerationLog{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if err := DB.Where("1 = 1").Delete(&ModerationLog{}).Error; err != nil {
		t.Fatalf("清表失败: %v", err)
	}

	day := int64(86400)
	now := time.Now().Unix()
	seed := []struct {
		tag    string
		action string
		age    int64 // 天
		want   bool  // 是否应当被保留
	}{
		{"pass-新", ModerationActionPass, 1, true},
		{"pass-超期", ModerationActionPass, 5, false},
		{"block-90天", ModerationActionBlock, 90, true}, // 旧默认值是 90 天，改成 180 后这条必须还在
		{"block-179天", ModerationActionBlock, 179, true},
		{"block-超期", ModerationActionBlock, 181, false},
		{"review-超期", ModerationActionReview, 181, false},
		{"error-新", ModerationActionError, 100, true},
	}
	for _, s := range seed {
		if err := DB.Create(&ModerationLog{
			RequestId: "retention-" + s.tag,
			Action:    s.action,
			CreatedAt: now - s.age*day,
		}).Error; err != nil {
			t.Fatalf("造数据 %s: %v", s.tag, err)
		}
	}

	if err := CleanupModerationLogs(); err != nil {
		t.Fatalf("清理失败: %v", err)
	}

	for _, s := range seed {
		var n int64
		DB.Model(&ModerationLog{}).Where("request_id = ?", "retention-"+s.tag).Count(&n)
		if got := n > 0; got != s.want {
			t.Errorf("%s（%s，%d 天前）保留=%v, want %v", s.tag, s.action, s.age, got, s.want)
		}
	}
}

// TestGetModerationLogsNeverReturnsCiphertext 列表接口不得把密文带出来。
//
// 这个接口的整个设计前提就是它看不到密文，取原文只能走带审计的独立接口。
// 老实现 SELECT * 之后再把字段擦掉，密文照样穿过网络和内存；这里连查询投影
// 都不许包含它，顺便验 has_content 是库里算出来的。
func TestGetModerationLogsNeverReturnsCiphertext(t *testing.T) {
	if err := DB.AutoMigrate(&ModerationLog{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if err := DB.Where("1 = 1").Delete(&ModerationLog{}).Error; err != nil {
		t.Fatalf("清表失败: %v", err)
	}

	now := time.Now().Unix()
	seed := []struct {
		requestId  string
		contentEnc string
		wantHas    bool
	}{
		{"list-with-content", "modenc:ZmFrZS1jaXBoZXJ0ZXh0", true},
		{"list-no-content", "", false},
	}
	for _, s := range seed {
		if err := DB.Create(&ModerationLog{
			RequestId:  s.requestId,
			Action:     ModerationActionBlock,
			Enforced:   true,
			Words:      "赌博",
			Preview:    "预览文本",
			ContentEnc: s.contentEnc,
			CreatedAt:  now,
		}).Error; err != nil {
			t.Fatalf("造数据 %s: %v", s.requestId, err)
		}
	}

	for _, s := range seed {
		logs, total, err := GetModerationLogs(ModerationLogQuery{RequestId: s.requestId, PageSize: 10})
		if err != nil {
			t.Fatalf("查 %s: %v", s.requestId, err)
		}
		if total != 1 || len(logs) != 1 {
			t.Fatalf("%s: total=%d len=%d, want 1/1", s.requestId, total, len(logs))
		}
		got := logs[0]
		if got.ContentEnc != "" {
			t.Errorf("%s: 列表接口返回了密文 %q", s.requestId, got.ContentEnc)
		}
		if got.HasContent != s.wantHas {
			t.Errorf("%s: HasContent=%v, want %v", s.requestId, got.HasContent, s.wantHas)
		}
		// 其余列必须照常返回，别为了不泄露密文把整行投影投丢了。
		if got.Words != "赌博" || got.Preview != "预览文本" || !got.Enforced {
			t.Errorf("%s: 其它列丢失 words=%q preview=%q enforced=%v",
				s.requestId, got.Words, got.Preview, got.Enforced)
		}
	}
}

// TestRecordModerationLogPersists 异步落库链路本身的冒烟测试。
// 队列 → worker → DB 这条路此前没有任何覆盖，写坏了只会表现为「记录莫名其妙没了」。
func TestRecordModerationLogPersists(t *testing.T) {
	if err := DB.AutoMigrate(&ModerationLog{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}

	const requestId = "test-req-persist-1"
	RecordModerationLog(&ModerationLog{
		RequestId: requestId,
		Action:    ModerationActionBlock,
		Source:    ModerationSourceSelf,
		Words:     "赌博",
	})

	// 落库是异步的，轮询等它到位。
	var got ModerationLog
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if err := DB.Where("request_id = ?", requestId).First(&got).Error; err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if got.Id == 0 {
		t.Fatal("审核记录没有落库")
	}
	if got.Words != "赌博" {
		t.Errorf("Words = %q, want 赌博", got.Words)
	}
	if got.CreatedAt == 0 {
		t.Error("CreatedAt 应由 RecordModerationLog 补上")
	}
}

// TestSetModerationContentStoresRawNotNormalized 留证内容必须是原文，不是检测输入。
//
// Normalize 会转小写、折叠空白、把西里尔同形字换成拉丁字母——喂检测器正合适，
// 但存进 Preview/ContentEnc 就成了「取证材料是我们自己改写过的版本」，
// 而页面上那一列写的是「原文」。hash 反过来必须用归一化那份，否则
// §11 的 hash 黑名单会因为一个零宽字符就失配。
func TestSetModerationContentStoresRawNotNormalized(t *testing.T) {
	if !common.ModerationKeyReady() {
		t.Fatal("测试密钥未生效，本用例会空过")
	}

	const raw = "I  Like\nGAMBLING"
	const normalized = "i like gambling"

	m := &ModerationLog{Action: ModerationActionBlock, Enforced: true}
	m.SetModerationContent(raw, normalized)

	if m.Preview != raw {
		t.Errorf("Preview = %q, want 原文 %q", m.Preview, raw)
	}
	plain, err := common.DecryptModerationContent(m.ContentEnc)
	if err != nil {
		t.Fatalf("解密失败: %v", err)
	}
	if plain != raw {
		t.Errorf("解出来的原文 = %q, want %q", plain, raw)
	}
	// hash 走归一化那份：换个大小写/空白不该算成另一条内容。
	if m.ContentHash != common.HashModerationContent(normalized) {
		t.Error("ContentHash 应当基于归一化文本，否则同内容的变体会各算一个 hash")
	}
}

// TestWordsFilterMatchesWordContainingComma 词条自带逗号时仍要能按词筛出来。
//
// 敏感词是运营自由填写的，英文短语带逗号很常见。用逗号做分隔符会把一条规则
// 劈成两个词，列表页显示出不存在的规则，按词筛选也再匹配不上——
// 而「是哪一条词拦的」正是这一列存在的全部理由。
func TestWordsFilterMatchesWordContainingComma(t *testing.T) {
	if err := DB.AutoMigrate(&ModerationLog{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if err := DB.Where("1 = 1").Delete(&ModerationLog{}).Error; err != nil {
		t.Fatalf("清表失败: %v", err)
	}

	const tricky = "hello, world"
	if err := DB.Create(&ModerationLog{
		RequestId: "words-comma-1",
		Action:    ModerationActionBlock,
		Enforced:  true,
		Words:     tricky + ModerationWordsSep + "赌博",
		CreatedAt: time.Now().Unix(),
	}).Error; err != nil {
		t.Fatalf("造数据: %v", err)
	}

	for _, word := range []string{tricky, "赌博"} {
		logs, total, err := GetModerationLogs(ModerationLogQuery{Word: word, PageSize: 10})
		if err != nil {
			t.Fatalf("按 %q 查: %v", word, err)
		}
		if total != 1 || len(logs) != 1 {
			t.Errorf("按 %q 查到 %d 条, want 1", word, total)
		}
	}
	// 精确匹配：拿词条的一部分不该命中，否则「赌博」会把「反赌博」也捞出来。
	if _, total, err := GetModerationLogs(ModerationLogQuery{Word: "hello", PageSize: 10}); err != nil {
		t.Fatalf("查 hello: %v", err)
	} else if total != 0 {
		t.Errorf("部分匹配不该命中，实际查到 %d 条", total)
	}
}

// TestGetModerationLogsCapsPageSize page_size 必须有上界。
//
// common.GetPageQuery 把 ?page_size= 原样透传，不做任何限制，而这个接口每行
// 都带内容预览。仓库里同类管理端列表都封 100，这里之前漏了。
func TestGetModerationLogsCapsPageSize(t *testing.T) {
	if err := DB.AutoMigrate(&ModerationLog{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if err := DB.Where("1 = 1").Delete(&ModerationLog{}).Error; err != nil {
		t.Fatalf("清表失败: %v", err)
	}

	now := time.Now().Unix()
	for i := 0; i < 120; i++ {
		if err := DB.Create(&ModerationLog{
			Action:    ModerationActionBlock,
			Enforced:  true,
			CreatedAt: now,
		}).Error; err != nil {
			t.Fatalf("造数据: %v", err)
		}
	}

	logs, total, err := GetModerationLogs(ModerationLogQuery{PageSize: 1000000})
	if err != nil {
		t.Fatalf("查询: %v", err)
	}
	if total != 120 {
		t.Fatalf("total = %d, want 120", total)
	}
	if len(logs) > 100 {
		t.Errorf("返回 %d 行，page_size 没有封顶——客户端可以一次把整表拉走", len(logs))
	}
}

// TestWordsFilterEscapesLikeWildcards 词条含 LIKE 元字符时仍是精确匹配。
//
// whereDelimitedContains 的四个分支里有三个是 LIKE。不转义 % 和 _ 的话，
// 它宣称的「精确匹配」在这两个字符上就是假的：搜 "100%" 会把任何以 100 开头的
// 词都捞出来。敏感词由运营自由填写，这两个字符都不罕见。
func TestWordsFilterEscapesLikeWildcards(t *testing.T) {
	if err := DB.AutoMigrate(&ModerationLog{}); err != nil {
		t.Fatalf("建表失败: %v", err)
	}
	if err := DB.Where("1 = 1").Delete(&ModerationLog{}).Error; err != nil {
		t.Fatalf("清表失败: %v", err)
	}

	now := time.Now().Unix()
	// 每条记录一个词，避免分支之间互相干扰。
	for _, w := range []string{"100%", "100abc", "a_b", "axb", "!bang"} {
		if err := DB.Create(&ModerationLog{
			RequestId: "like-" + w,
			Action:    ModerationActionBlock,
			Enforced:  true,
			Words:     w + ModerationWordsSep + "赌博",
			CreatedAt: now,
		}).Error; err != nil {
			t.Fatalf("造数据 %s: %v", w, err)
		}
	}

	cases := []struct {
		query string
		want  string // 期望命中的那条记录的 request_id
	}{
		// % 必须当字面量：不转义的话 "100%" 会同时命中 100abc。
		{"100%", "like-100%"},
		{"100abc", "like-100abc"},
		// _ 是单字符通配：不转义的话 "a_b" 会命中 axb。
		{"a_b", "like-a_b"},
		{"axb", "like-axb"},
		// 转义符本身出现在值里也要能查——它必须先被转义成 "!!"。
		{"!bang", "like-!bang"},
	}
	for _, c := range cases {
		logs, total, err := GetModerationLogs(ModerationLogQuery{Word: c.query, PageSize: 10})
		if err != nil {
			t.Fatalf("按 %q 查: %v", c.query, err)
		}
		if total != 1 {
			ids := make([]string, 0, len(logs))
			for _, l := range logs {
				ids = append(ids, l.RequestId)
			}
			t.Errorf("按 %q 查到 %d 条 %v, want 1 条（%s）", c.query, total, ids, c.want)
			continue
		}
		if logs[0].RequestId != c.want {
			t.Errorf("按 %q 命中 %s, want %s", c.query, logs[0].RequestId, c.want)
		}
	}
}
