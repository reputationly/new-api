package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// withModerationDB 给本文件的用例挂一个内存库。
// service 包默认没有 DB（model.DB 为 nil，落库 worker 直接空转），
// 而「记了几条」正是这里唯一要断言的东西，不落库就没法验。
func withModerationDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	// 每条连接对应一个独立的 :memory: 库。连接池一开多，AutoMigrate 建的表
	// 和后面查询用的连接就不是同一个库了，表现为「表不存在」。
	// 这个坑 model/task_cas_test.go:34 已经踩过一次，同样的处理。
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get sql.DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)

	if err := db.AutoMigrate(&model.ModerationLog{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	prev := model.DB
	model.DB = db
	t.Cleanup(func() { model.DB = prev })
	return db
}

func countUpstreamRecords(t *testing.T, db *gorm.DB, requestId string) int64 {
	t.Helper()
	// 落库是异步的：先等到至少一条，再给后续可能的重复留一点窗口。
	var n int64
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		db.Model(&model.ModerationLog{}).Where("request_id = ?", requestId).Count(&n)
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	db.Model(&model.ModerationLog{}).Where("request_id = ?", requestId).Count(&n)
	return n
}

func testRelayInfo(requestId string) *relaycommon.RelayInfo {
	// ChannelId 挂在内嵌的 ChannelMeta 上，字面量里设不了，本用例也不断言它。
	return &relaycommon.RelayInfo{
		UserId:          7,
		TokenId:         8,
		UsingGroup:      "default",
		RequestId:       requestId,
		OriginModelName: "gemini-2.5-pro",
	}
}

// TestRecordUpstreamRejectionWritesRecord 基本行为：上游给了拒绝理由就落一条 upstream 记录。
func TestRecordUpstreamRejectionWritesRecord(t *testing.T) {
	db := withModerationDB(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	const reqId = "upstream-basic-1"
	RecordUpstreamRejection(c, testRelayInfo(reqId), "gemini_block_reason=SAFETY")

	if n := countUpstreamRecords(t, db, reqId); n != 1 {
		t.Fatalf("落库条数 = %d, want 1", n)
	}

	var got model.ModerationLog
	if err := db.Where("request_id = ?", reqId).First(&got).Error; err != nil {
		t.Fatalf("查记录: %v", err)
	}
	if got.Source != model.ModerationSourceUpstream {
		t.Errorf("Source = %q, want upstream", got.Source)
	}
	if got.Action != model.ModerationActionBlock {
		t.Errorf("Action = %q, want block", got.Action)
	}
	// 上游给的是各家自己的理由码，原样留在 Detail 里，不硬映射到我们那九类。
	if got.Detail == "" || got.Categories != "" {
		t.Errorf("Detail=%q Categories=%q: 理由码应进 Detail，Categories 留空", got.Detail, got.Categories)
	}
	// 到这一步请求已经发给上游了，原始 prompt 不在手边，不该编一份出来。
	if got.Preview != "" || got.ContentEnc != "" {
		t.Error("upstream 记录不应携带任何内容")
	}
}

// TestRecordUpstreamRejectionIsIdempotentPerRequest 同一请求只记一次。
//
// 收口有两处（成功走 PostTextConsumeQuota，失败走 relay 的错误 defer），
// 重试链路上还可能出现「前一次被拒、后一次成功」让理由残留在 context 里。
// 记两次的话，「上游拒了多少次」这个数就直接错了——而那正是这张表要回答的问题。
func TestRecordUpstreamRejectionIsIdempotentPerRequest(t *testing.T) {
	db := withModerationDB(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	const reqId = "upstream-idem-1"
	info := testRelayInfo(reqId)

	RecordUpstreamRejection(c, info, "gemini_block_reason=SAFETY")
	RecordUpstreamRejection(c, info, "gemini_block_reason=SAFETY")
	// 换个理由也不行：同一次请求就是一次拒绝。
	RecordUpstreamRejection(c, info, "gemini_empty_candidates")

	if n := countUpstreamRecords(t, db, reqId); n != 1 {
		t.Fatalf("同一请求落库 %d 条, want 1", n)
	}
}

// TestRecordUpstreamRejectionSkipsEmptyReason 没有拒绝理由时什么都不做。
func TestRecordUpstreamRejectionSkipsEmptyReason(t *testing.T) {
	db := withModerationDB(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	const reqId = "upstream-empty-1"
	RecordUpstreamRejection(c, testRelayInfo(reqId), "")
	// 顺便确认它没有把闸门标记提前点掉：真拒绝来了还得能记。
	RecordUpstreamRejection(c, testRelayInfo(reqId), "claude_stop_reason=refusal")

	if n := countUpstreamRecords(t, db, reqId); n != 1 {
		t.Fatalf("落库条数 = %d, want 1", n)
	}
}

// TestUpstreamRejectStageClassification 只有已登记的违规信号才成为记录，且阶段要分对。
//
// gemini_empty_candidates 是上游返回空响应且没给 block reason——供应商故障，
// 不是用户违规。记成 block 等于把上游抽风算进违规率。
// OpenAI 的 content_filter 和 Claude 的 refusal 判的是**输出**，
// 只有 Gemini 的 PromptFeedback 才是输入侧。
func TestUpstreamRejectStageClassification(t *testing.T) {
	cases := []struct {
		reason    string
		wantStage string
		wantOK    bool
	}{
		{"gemini_block_reason=SAFETY", "prompt", true},
		{"gemini_block_reason=OTHER", "prompt", true},
		{"openai_finish_reason=content_filter", "output", true},
		{"claude_stop_reason=refusal", "output", true},
		// 技术性失败，不是违规判定。
		{"gemini_empty_candidates", "", false},
		// 没登记过的信号一律不记，宁缺毋滥。
		{"some_future_provider_reason", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		stage, ok := upstreamRejectStage(c.reason)
		if ok != c.wantOK || stage != c.wantStage {
			t.Errorf("upstreamRejectStage(%q) = (%q, %v), want (%q, %v)",
				c.reason, stage, ok, c.wantStage, c.wantOK)
		}
	}
}

// TestRecordUpstreamRejectionIgnoresTechnicalFailure 上游故障不落审核记录。
func TestRecordUpstreamRejectionIgnoresTechnicalFailure(t *testing.T) {
	db := withModerationDB(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	const reqId = "upstream-technical-1"
	RecordUpstreamRejection(c, testRelayInfo(reqId), "gemini_empty_candidates")

	var n int64
	time.Sleep(300 * time.Millisecond)
	db.Model(&model.ModerationLog{}).Where("request_id = ?", reqId).Count(&n)
	if n != 0 {
		t.Errorf("上游技术性失败落了 %d 条审核记录，应为 0", n)
	}
}

// TestRecordUpstreamRejectionStageIsOutputForRefusals 输出侧拒绝记成 stage=output。
func TestRecordUpstreamRejectionStageIsOutputForRefusals(t *testing.T) {
	db := withModerationDB(t)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	const reqId = "upstream-output-1"
	RecordUpstreamRejection(c, testRelayInfo(reqId), "openai_finish_reason=content_filter")

	if n := countUpstreamRecords(t, db, reqId); n != 1 {
		t.Fatalf("落库条数 = %d, want 1", n)
	}
	var got model.ModerationLog
	if err := db.Where("request_id = ?", reqId).First(&got).Error; err != nil {
		t.Fatalf("查记录: %v", err)
	}
	if got.Stage != "output" {
		t.Errorf("Stage = %q, want output", got.Stage)
	}
	if !got.Enforced {
		t.Error("上游真的拒了，Enforced 应为 true")
	}
}
