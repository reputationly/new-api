package controller

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupRegisterTestDB 起一个内存 SQLite 并迁移 User 表,同时把注册所需的全局开关
// 调到「开放注册、不校验邮箱」的状态。
func setupRegisterTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	gin.SetMode(gin.TestMode)
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false

	prevRegister := common.RegisterEnabled
	prevPassword := common.PasswordRegisterEnabled
	prevEmail := common.EmailVerificationEnabled
	common.RegisterEnabled = true
	common.PasswordRegisterEnabled = true
	common.EmailVerificationEnabled = false
	t.Cleanup(func() {
		common.RegisterEnabled = prevRegister
		common.PasswordRegisterEnabled = prevPassword
		common.EmailVerificationEnabled = prevEmail
	})

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	model.DB = db
	model.LOG_DB = db
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})

	if err := db.AutoMigrate(&model.User{}, &model.Log{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// seedInviter 建一个邀请人,返回其 id 与 aff_code。
// AffCode 必须在 Create 之后再写死:User.Insert 会覆盖成随机值,所以这里直接建记录。
func seedInviter(t *testing.T, db *gorm.DB, affCode string) int {
	t.Helper()
	inviter := &model.User{
		Username: "inviter",
		Password: "hashed-placeholder",
		AffCode:  affCode,
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
	}
	if err := db.Create(inviter).Error; err != nil {
		t.Fatalf("seed inviter: %v", err)
	}
	return inviter.Id
}

// callRegister 用给定的 JSON body 打一次注册接口。
func callRegister(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest("POST", "/api/user/register", bytes.NewBufferString(body))
	c.Request.Header.Set("Content-Type", "application/json")
	Register(c)
	return rec
}

func fetchUser(t *testing.T, db *gorm.DB, username string) model.User {
	t.Helper()
	var u model.User
	if err := db.Where("username = ?", username).First(&u).Error; err != nil {
		t.Fatalf("load user %q: %v", username, err)
	}
	return u
}

// 端到端:前端发 aff_code → 后端把 InviterId 真的写进库。
//
// 这是整条邀请链路唯一有意义的验收标准。只断言「前端发出了字段」是不够的——
// 字段名写错时 HTTP 依然 200,邀请关系静默丢失,这正是 default 主题此前的 bug。
func TestRegisterRecordsInviterFromAffCode(t *testing.T) {
	db := setupRegisterTestDB(t)
	inviterID := seedInviter(t, db, "mwg1")

	rec := callRegister(t, `{"username":"invitee","password":"password123","aff_code":"mwg1"}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	invitee := fetchUser(t, db, "invitee")
	if invitee.InviterId != inviterID {
		t.Fatalf("InviterId = %d, want %d (邀请关系没落库)", invitee.InviterId, inviterID)
	}

	// 邀请人的 aff_count 也应该 +1。
	inviter := fetchUser(t, db, "inviter")
	if inviter.AffCount != 1 {
		t.Fatalf("inviter AffCount = %d, want 1", inviter.AffCount)
	}
}

// 反向锁定:发旧字段名 aff 时邀请关系记不上。
//
// 这条用例存在的意义是证明字段名不是可有可无的细节——default 主题此前发的就是 aff,
// 接口照样返回成功,但 InviterId 恒为 0。如果哪天有人把字段名改回去,这里会红。
func TestRegisterIgnoresLegacyAffFieldName(t *testing.T) {
	db := setupRegisterTestDB(t)
	seedInviter(t, db, "mwg1")

	rec := callRegister(t, `{"username":"invitee","password":"password123","aff":"mwg1"}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	invitee := fetchUser(t, db, "invitee")
	if invitee.InviterId != 0 {
		t.Fatalf("InviterId = %d, want 0 —— 后端只认 aff_code,发 aff 不该生效", invitee.InviterId)
	}
}

// 没有邀请码时正常注册,InviterId 为 0。
func TestRegisterWithoutAffCode(t *testing.T) {
	db := setupRegisterTestDB(t)

	rec := callRegister(t, `{"username":"solo","password":"password123"}`)
	if rec.Code != 200 {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	if solo := fetchUser(t, db, "solo"); solo.InviterId != 0 {
		t.Fatalf("InviterId = %d, want 0", solo.InviterId)
	}
}
