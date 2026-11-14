package controller

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupAssistantFeedbackControllerDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&model.User{}, &model.AssistantFeedback{}, &model.Log{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	orig := model.DB
	model.DB = db
	origLog := model.LOG_DB
	model.LOG_DB = db
	origRedis := common.RedisEnabled
	common.RedisEnabled = false
	t.Cleanup(func() {
		model.DB = orig
		model.LOG_DB = origLog
		common.RedisEnabled = origRedis
	})
	return db
}

func TestSubmitAssistantFeedbackHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupAssistantFeedbackControllerDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("id", 7)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/feedback",
		strings.NewReader(`{"channel":"rating","rating":"like","category":"回答准确","content":"很好","session_id":"sess-1","message_id":"msg-1","request_id":"req-submit-1"}`))
	c.Request.Header.Set("Content-Type", "application/json")
	SubmitAssistantFeedback(c)

	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `"success":true`) {
		t.Fatalf("unexpected body: %s", w.Body.String())
	}

	var saved model.AssistantFeedback
	if err := model.DB.First(&saved).Error; err != nil {
		t.Fatalf("load saved feedback: %v", err)
	}
	if saved.UserId != 7 || saved.Channel != model.AssistantFeedbackChannelRating ||
		saved.Rating != model.AssistantFeedbackRatingLike || saved.Content != "很好" ||
		saved.RequestId != "req-submit-1" {
		t.Fatalf("unexpected saved feedback: %+v", saved)
	}
}

func TestSubmitAssistantFeedbackHandlerRejectsInvalid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupAssistantFeedbackControllerDB(t)

	cases := []struct {
		name string
		body string
	}{
		{"非 JSON", `not-json`},
		{"缺少类型", `{"rating":"like"}`},
		{"对话评价缺少评价", `{"channel":"rating","content":"很好"}`},
		{"意见反馈内容为空", `{"channel":"general"}`},
		{"context 不是对象", `{"channel":"general","content":"hi","context":[]}`},
		{"context 非法 JSON", `{"channel":"general","content":"hi","context":{"a":}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set("id", 7)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/feedback", strings.NewReader(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			SubmitAssistantFeedback(c)

			if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"success":false`) {
				t.Fatalf("status=%d body=%s, want 200 success:false", w.Code, w.Body.String())
			}
		})
	}
}

func TestAdminGetAssistantFeedbackHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupAssistantFeedbackControllerDB(t)

	if err := db.Create(&model.User{Id: 8, Username: "bob", AffCode: "bob0001", Role: 1, Status: 1}).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := model.CreateAssistantFeedback(8, model.AssistantFeedbackInput{
		Channel:   model.AssistantFeedbackChannelGeneral,
		Content:   "希望支持导出对话记录",
		RequestId: "req-list-1",
	}); err != nil {
		t.Fatalf("create feedback: %v", err)
	}
	if _, err := model.CreateAssistantFeedback(8, model.AssistantFeedbackInput{
		Channel: model.AssistantFeedbackChannelRating,
		Rating:  model.AssistantFeedbackRatingDislike,
		Content: "答案不准确",
	}); err != nil {
		t.Fatalf("create feedback: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet,
		"/api/user/feedback/admin/assistant?channel=general&page=1&page_size=10", nil)
	AdminGetAssistantFeedback(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{`"success":true`, `"total":1`, `"username":"bob"`, "希望支持导出对话记录"} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
}

func TestAdminGetAssistantFeedbackHandlerRejectsInvalidFilter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupAssistantFeedbackControllerDB(t)

	for _, query := range []string{"channel=nope", "rating=nope"} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/api/user/feedback/admin/assistant?"+query, nil)
		AdminGetAssistantFeedback(c)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"success":false`) {
			t.Fatalf("query=%s status=%d body=%s, want 200 success:false", query, w.Code, w.Body.String())
		}
	}
}

func TestSubmitAssistantFeedbackHandlerRejectsOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupAssistantFeedbackControllerDB(t)

	body := `{"channel":"general","content":"` + strings.Repeat("a", 140*1024) + `"}`

	cases := []struct {
		name          string
		contentLength int64
	}{
		{"声明超大 Content-Length", int64(len(body))},
		{"chunked 未声明长度", -1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Set("id", 7)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/feedback", io.NopCloser(strings.NewReader(body)))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Request.ContentLength = tc.contentLength
			SubmitAssistantFeedback(c)

			if w.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("status = %d, want 413 (body=%s)", w.Code, w.Body.String())
			}
		})
	}

	var count int64
	if err := model.DB.Model(&model.AssistantFeedback{}).Count(&count).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Fatalf("oversized request must not persist, got %d rows", count)
	}
}

func TestSubmitAssistantFeedbackHandlerRejectsNonJSONContentType(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupAssistantFeedbackControllerDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("id", 7)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/feedback",
		strings.NewReader(`{"channel":"general","content":"hi"}`))
	c.Request.Header.Set("Content-Type", "text/plain")
	SubmitAssistantFeedback(c)

	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"success":false`) {
		t.Fatalf("status=%d body=%s, want 200 success:false", w.Code, w.Body.String())
	}
}

func TestAssistantFeedbackContextStoredAndDetailOnly(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupAssistantFeedbackControllerDB(t)

	if err := db.Create(&model.User{Id: 7, Username: "alice", AffCode: "alice01", Role: 1, Status: 1}).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}

	submit := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(submit)
	c.Set("id", 7)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/feedback", strings.NewReader(`{
		"channel":"rating","rating":"dislike","content":"答案不对","request_id":"req-ctx-1",
		"context":{
			"system_prompt":"SNAPSHOT_SYSTEM",
			"messages":[{"role":"user","content":"SNAPSHOT_USER"}],
			"request_params":{"temperature":0.7}
		}
	}`))
	c.Request.Header.Set("Content-Type", "application/json")
	SubmitAssistantFeedback(c)
	if submit.Code != http.StatusCreated {
		t.Fatalf("submit status=%d body=%s", submit.Code, submit.Body.String())
	}

	var saved model.AssistantFeedback
	if err := db.First(&saved).Error; err != nil {
		t.Fatalf("load saved feedback: %v", err)
	}
	if !strings.Contains(saved.ContextJson, "SNAPSHOT_SYSTEM") {
		t.Fatalf("context not stored: %q", saved.ContextJson)
	}

	// 列表只给 has_context，不返回快照正文。
	list := httptest.NewRecorder()
	lc, _ := gin.CreateTestContext(list)
	lc.Request = httptest.NewRequest(http.MethodGet, "/api/user/feedback/admin/assistant?page=1&page_size=10", nil)
	AdminGetAssistantFeedback(lc)
	if list.Code != http.StatusOK {
		t.Fatalf("list status=%d body=%s", list.Code, list.Body.String())
	}
	if strings.Contains(list.Body.String(), "SNAPSHOT_SYSTEM") {
		t.Fatalf("list must not expose context: %s", list.Body.String())
	}
	if !strings.Contains(list.Body.String(), `"has_context":true`) {
		t.Fatalf("list missing has_context: %s", list.Body.String())
	}

	// 详情返回快照，并写一条管理员审计日志。
	detail := httptest.NewRecorder()
	dc, _ := gin.CreateTestContext(detail)
	dc.Set("id", 999)
	dc.Params = gin.Params{{Key: "id", Value: strconv.Itoa(saved.Id)}}
	dc.Request = httptest.NewRequest(http.MethodGet, "/api/user/feedback/admin/assistant/"+strconv.Itoa(saved.Id), nil)
	AdminGetAssistantFeedbackDetail(dc)
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status=%d body=%s", detail.Code, detail.Body.String())
	}
	if !strings.Contains(detail.Body.String(), "SNAPSHOT_SYSTEM") || !strings.Contains(detail.Body.String(), "SNAPSHOT_USER") {
		t.Fatalf("detail missing context: %s", detail.Body.String())
	}
	if !strings.Contains(detail.Body.String(), `"request_id":"req-ctx-1"`) {
		t.Fatalf("detail missing request id: %s", detail.Body.String())
	}

	var logCount int64
	if err := db.Model(&model.Log{}).Where("content LIKE ?", "%查看小助手反馈上下文%").Count(&logCount).Error; err != nil {
		t.Fatalf("count audit log: %v", err)
	}
	if logCount == 0 {
		t.Fatalf("viewing context must write an audit log")
	}
}

func TestAdminGetAssistantFeedbackDetailNotFound(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupAssistantFeedbackControllerDB(t)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("id", 1)
	c.Params = gin.Params{{Key: "id", Value: "999"}}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/user/feedback/admin/assistant/999", nil)
	AdminGetAssistantFeedbackDetail(c)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status=%d body=%s, want 404", w.Code, w.Body.String())
	}
}

func TestAdminGetAssistantFeedbackFilterByRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupAssistantFeedbackControllerDB(t)

	if err := db.Create(&model.User{Id: 8, Username: "bob", AffCode: "bob0001", Role: 1, Status: 1}).Error; err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := model.CreateAssistantFeedback(8, model.AssistantFeedbackInput{
		Channel:   model.AssistantFeedbackChannelRating,
		Rating:    model.AssistantFeedbackRatingDislike,
		RequestId: "req-filter-1",
	}); err != nil {
		t.Fatalf("create feedback: %v", err)
	}

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet,
		"/api/user/feedback/admin/assistant?page=1&page_size=10&request_id=req-filter-1", nil)
	AdminGetAssistantFeedback(c)

	if w.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{`"success":true`, `"total":1`, `"request_id":"req-filter-1"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("body missing %q: %s", want, body)
		}
	}
}
