package model

import (
	"errors"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// 小助手反馈：校验规则与后台列表筛选。Two 个独立测试各开一个内存库，避免共享状态。
func setupAssistantFeedbackTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&User{}, &AssistantFeedback{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	orig := DB
	DB = db
	t.Cleanup(func() { DB = orig })
	return db
}

func TestCreateAssistantFeedbackValidation(t *testing.T) {
	setupAssistantFeedbackTestDB(t)

	cases := []struct {
		name  string
		input AssistantFeedbackInput
		want  error
	}{
		{
			name:  "缺少类型",
			input: AssistantFeedbackInput{Rating: AssistantFeedbackRatingLike},
			want:  ErrAssistantFeedbackInvalidChannel,
		},
		{
			name:  "对话评价缺少评价",
			input: AssistantFeedbackInput{Channel: AssistantFeedbackChannelRating},
			want:  ErrAssistantFeedbackRatingRequired,
		},
		{
			name: "对话评价评价值非法",
			input: AssistantFeedbackInput{
				Channel: AssistantFeedbackChannelRating,
				Rating:  "neutral",
			},
			want: ErrAssistantFeedbackInvalidRating,
		},
		{
			name:  "意见反馈内容为空",
			input: AssistantFeedbackInput{Channel: AssistantFeedbackChannelGeneral},
			want:  ErrAssistantFeedbackContentRequired,
		},
		{
			name:  "意见反馈内容为空白",
			input: AssistantFeedbackInput{Channel: AssistantFeedbackChannelGeneral, Content: "   "},
			want:  ErrAssistantFeedbackContentRequired,
		},
		{
			name: "反馈内容超长",
			input: AssistantFeedbackInput{
				Channel: AssistantFeedbackChannelGeneral,
				Content: strings.Repeat("字", AssistantFeedbackMaxContentLen+1),
			},
			// 具体文案里带字段名与上限，这里只断言可展示的错误，不绑定措辞。
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fb, err := CreateAssistantFeedback(1, tc.input)
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
				return
			}
			if tc.name == "反馈内容超长" {
				if err == nil || !strings.Contains(err.Error(), "反馈内容长度") {
					t.Fatalf("err = %v, want 反馈内容长度错误", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if fb == nil || fb.Id == 0 {
				t.Fatalf("created feedback missing id: %+v", fb)
			}
		})
	}
}

func TestCreateAssistantFeedbackNormalizesAndDropsGeneralRating(t *testing.T) {
	setupAssistantFeedbackTestDB(t)

	fb, err := CreateAssistantFeedback(3, AssistantFeedbackInput{
		Channel:   " rating ",
		Rating:    " dislike ",
		Category:  " 回答不准确 ",
		Content:   "  引用文档不存在  ",
		SessionId: "sess-1",
		MessageId: "msg-1",
	})
	if err != nil {
		t.Fatalf("CreateAssistantFeedback: %v", err)
	}
	if fb.Channel != AssistantFeedbackChannelRating || fb.Rating != AssistantFeedbackRatingDislike {
		t.Fatalf("channel/rating not normalized: %+v", fb)
	}
	if fb.Content != "引用文档不存在" || fb.Category != "回答不准确" {
		t.Fatalf("text not trimmed: content=%q category=%q", fb.Content, fb.Category)
	}

	general, err := CreateAssistantFeedback(3, AssistantFeedbackInput{
		Channel: AssistantFeedbackChannelGeneral,
		Rating:  AssistantFeedbackRatingDislike,
		Content: "希望增加导出功能",
	})
	if err != nil {
		t.Fatalf("CreateAssistantFeedback(general): %v", err)
	}
	if general.Rating != "" {
		t.Fatalf("general feedback should drop rating, got %q", general.Rating)
	}
}

func TestGetAssistantFeedbackAdminList(t *testing.T) {
	db := setupAssistantFeedbackTestDB(t)

	if err := db.Create(&User{Id: 7, Username: "alice", AffCode: "alice01", Role: 1, Status: 1}).Error; err != nil {
		t.Fatalf("create alice: %v", err)
	}
	if err := db.Create(&User{Id: 8, Username: "bob", AffCode: "bob0001", Role: 1, Status: 1}).Error; err != nil {
		t.Fatalf("create bob: %v", err)
	}

	if _, err := CreateAssistantFeedback(7, AssistantFeedbackInput{
		Channel:   AssistantFeedbackChannelRating,
		Rating:    AssistantFeedbackRatingDislike,
		Category:  "回答不准确",
		Content:   "答案引用了不存在的文件",
		SessionId: "sess-alice",
		Model:     "deepseek-v4-1-flash",
	}); err != nil {
		t.Fatalf("create alice feedback: %v", err)
	}
	if _, err := CreateAssistantFeedback(8, AssistantFeedbackInput{
		Channel: AssistantFeedbackChannelGeneral,
		Content: "希望支持导出对话记录",
	}); err != nil {
		t.Fatalf("create bob feedback: %v", err)
	}

	all, total, err := GetAssistantFeedbackAdminList(AssistantFeedbackAdminFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if total != 2 || len(all) != 2 {
		t.Fatalf("total=%d len=%d, want 2/2", total, len(all))
	}
	// 按创建时间倒序：后插入的 bob 在前；同秒时由 id 兜底。
	if all[0].Username != "bob" || all[1].Username != "alice" {
		t.Fatalf("unexpected order/usernames: %+v", []string{all[0].Username, all[1].Username})
	}
	if all[0].Content != "希望支持导出对话记录" || all[1].SessionId != "sess-alice" {
		t.Fatalf("row fields not populated: %+v", all)
	}

	cases := []struct {
		name   string
		filter AssistantFeedbackAdminFilter
		want   int
	}{
		{"按类型", AssistantFeedbackAdminFilter{Channel: AssistantFeedbackChannelGeneral, Page: 1, PageSize: 10}, 1},
		{"按评价", AssistantFeedbackAdminFilter{Rating: AssistantFeedbackRatingDislike, Page: 1, PageSize: 10}, 1},
		{"按用户ID", AssistantFeedbackAdminFilter{UserId: 8, Page: 1, PageSize: 10}, 1},
		{"按用户名", AssistantFeedbackAdminFilter{Username: "ali", Page: 1, PageSize: 10}, 1},
		{"按关键词命中内容", AssistantFeedbackAdminFilter{Keyword: "导出", Page: 1, PageSize: 10}, 1},
		{"按关键词命中会话", AssistantFeedbackAdminFilter{Keyword: "sess-alice", Page: 1, PageSize: 10}, 1},
		{"无匹配", AssistantFeedbackAdminFilter{Keyword: "zzz-not-found", Page: 1, PageSize: 10}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, gotTotal, err := GetAssistantFeedbackAdminList(tc.filter)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if gotTotal != int64(tc.want) || len(rows) != tc.want {
				t.Fatalf("total=%d len=%d, want %d", gotTotal, len(rows), tc.want)
			}
		})
	}

	secondPage, total, err := GetAssistantFeedbackAdminList(AssistantFeedbackAdminFilter{Page: 2, PageSize: 1})
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if total != 2 || len(secondPage) != 1 || secondPage[0].Username != "alice" {
		t.Fatalf("pagination broken: total=%d rows=%+v", total, secondPage)
	}
}

func TestCreateAssistantFeedbackDailyLimit(t *testing.T) {
	db := setupAssistantFeedbackTestDB(t)

	rows := make([]AssistantFeedback, 0, AssistantFeedbackMaxDailyPerUser)
	for i := 0; i < AssistantFeedbackMaxDailyPerUser; i++ {
		rows = append(rows, AssistantFeedback{
			UserId:  9,
			Channel: AssistantFeedbackChannelGeneral,
			Content: "今日已有反馈",
		})
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("seed daily rows: %v", err)
	}

	_, err := CreateAssistantFeedback(9, AssistantFeedbackInput{
		Channel: AssistantFeedbackChannelGeneral,
		Content: "再来一条",
	})
	if !errors.Is(err, ErrAssistantFeedbackDailyLimit) {
		t.Fatalf("err = %v, want ErrAssistantFeedbackDailyLimit", err)
	}

	// 额度按 user_id 隔离：另一个用户仍可提交。
	if _, err := CreateAssistantFeedback(10, AssistantFeedbackInput{
		Channel: AssistantFeedbackChannelGeneral,
		Content: "另一个用户",
	}); err != nil {
		t.Fatalf("other user should still submit: %v", err)
	}
}

func TestCreateAssistantFeedbackStripsNUL(t *testing.T) {
	setupAssistantFeedbackTestDB(t)

	fb, err := CreateAssistantFeedback(11, AssistantFeedbackInput{
		Channel:  AssistantFeedbackChannelGeneral,
		Category: "task\x00-result",
		Content:  "前半段\x00后半段",
	})
	if err != nil {
		t.Fatalf("CreateAssistantFeedback: %v", err)
	}
	if fb.Content != "前半段后半段" || fb.Category != "task-result" {
		t.Fatalf("NUL not stripped: content=%q category=%q", fb.Content, fb.Category)
	}
}

func TestCreateAssistantFeedbackContextStoredAndDetailOnly(t *testing.T) {
	setupAssistantFeedbackTestDB(t)

	fb, err := CreateAssistantFeedback(12, AssistantFeedbackInput{
		Channel: AssistantFeedbackChannelRating,
		Rating:  AssistantFeedbackRatingDislike,
		Content: "答案不对",
		Context: []byte(`{
			"system_prompt":"SNAPSHOT_SYSTEM",
			"messages":[{"role":"user","content":"SNAPSHOT_USER"}],
			"request_params":{"temperature":0.7,"api_key":"sk-abcdefghijklmnopqrstuvwxyz"},
			"authorization":"Bearer abcdefghijklmnopqrstuvwxyz"
		}`),
	})
	if err != nil {
		t.Fatalf("CreateAssistantFeedback: %v", err)
	}
	if !strings.Contains(fb.ContextJson, "SNAPSHOT_SYSTEM") {
		t.Fatalf("context not saved: %q", fb.ContextJson)
	}
	if strings.Contains(fb.ContextJson, "sk-abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("sk- key not redacted: %q", fb.ContextJson)
	}
	if strings.Contains(fb.ContextJson, "Bearer abcdefghijklmnopqrstuvwxyz") {
		t.Fatalf("Bearer token not redacted: %q", fb.ContextJson)
	}

	// 列表投影不能带 context_json，只带 has_context。
	rows, _, err := GetAssistantFeedbackAdminList(AssistantFeedbackAdminFilter{Page: 1, PageSize: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 || rows[0].HasContextFlag == 0 || rows[0].ContextJson != "" {
		t.Fatalf("list must expose only has_context: %+v", rows[0])
	}

	// 详情接口（模型层）返回完整快照。
	detail, err := GetAssistantFeedbackById(fb.Id)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if detail.ContextJson != fb.ContextJson {
		t.Fatalf("detail context mismatch: %q vs %q", detail.ContextJson, fb.ContextJson)
	}

}

func TestCreateAssistantFeedbackContextValidation(t *testing.T) {
	setupAssistantFeedbackTestDB(t)

	cases := []struct {
		name    string
		context []byte
		want    error
	}{
		{"数组", []byte(`[]`), ErrAssistantFeedbackContextInvalid},
		{"字符串", []byte(`"x"`), ErrAssistantFeedbackContextInvalid},
		{"非法 JSON", []byte(`{"a":}`), ErrAssistantFeedbackContextInvalid},
		{"超过 60KB", []byte(`{"a":"` + strings.Repeat("x", AssistantFeedbackMaxContextBytes) + `"}`), ErrAssistantFeedbackContextTooLarge},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := CreateAssistantFeedback(13, AssistantFeedbackInput{
				Channel: AssistantFeedbackChannelGeneral,
				Content: "反馈",
				Context: tc.context,
			})
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}

	// null / 空对象视为没有快照。
	for _, raw := range []string{"null", "{}", "  "} {
		fb, err := CreateAssistantFeedback(13, AssistantFeedbackInput{
			Channel: AssistantFeedbackChannelGeneral,
			Content: "反馈",
			Context: []byte(raw),
		})
		if err != nil {
			t.Fatalf("context=%q: %v", raw, err)
		}
		if fb.ContextJson != "" {
			t.Fatalf("context=%q should be empty, got %q", raw, fb.ContextJson)
		}
	}
}

func TestAssistantFeedbackRequestID(t *testing.T) {
	setupAssistantFeedbackTestDB(t)

	fb, err := CreateAssistantFeedback(20, AssistantFeedbackInput{
		Channel:   AssistantFeedbackChannelRating,
		Rating:    AssistantFeedbackRatingLike,
		RequestId: "  req-abc-123  ",
	})
	if err != nil {
		t.Fatalf("CreateAssistantFeedback: %v", err)
	}
	if fb.RequestId != "req-abc-123" {
		t.Fatalf("request id not trimmed: %q", fb.RequestId)
	}

	rows, total, err := GetAssistantFeedbackAdminList(AssistantFeedbackAdminFilter{
		RequestId: "req-abc-123", Page: 1, PageSize: 10,
	})
	if err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("request id filter failed: total=%d len=%d err=%v", total, len(rows), err)
	}
	if rows[0].RequestId != "req-abc-123" {
		t.Fatalf("list missing request id: %+v", rows[0])
	}

	// keyword 也能搜到 Request ID
	rows, _, err = GetAssistantFeedbackAdminList(AssistantFeedbackAdminFilter{Keyword: "abc-123", Page: 1, PageSize: 10})
	if err != nil || len(rows) != 1 {
		t.Fatalf("keyword request id failed: len=%d err=%v", len(rows), err)
	}

	if _, err := CreateAssistantFeedback(20, AssistantFeedbackInput{
		Channel:   AssistantFeedbackChannelGeneral,
		Content:   "x",
		RequestId: strings.Repeat("r", AssistantFeedbackMaxRequestIdLen+1),
	}); err == nil || !strings.Contains(err.Error(), "Request ID长度") {
		t.Fatalf("oversized request id should be rejected: %v", err)
	}
}
