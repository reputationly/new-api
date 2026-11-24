package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupPointsAdminControllerDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.PointsLot{}, &model.FundEntry{}, &model.Log{}))

	orig, origLog := model.DB, model.LOG_DB
	origRedis := common.RedisEnabled
	origSQLite := common.UsingSQLite
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled = false
	common.UsingSQLite = true
	t.Cleanup(func() {
		model.DB, model.LOG_DB = orig, origLog
		common.RedisEnabled = origRedis
		common.UsingSQLite = origSQLite
	})

	ps := operation_setting.GetPointsSetting()
	origEnabled := ps.Enabled
	ps.Enabled = true
	t.Cleanup(func() { ps.Enabled = origEnabled })
	return db
}

func seedManagedUser(t *testing.T, id int, points int) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.User{
		Id: id, Username: "managed_" + strings.ReplaceAll(t.Name(), "/", "_"),
		Role: 1, Status: 1, Group: "default", PointsBalance: points,
	}).Error)
}

// callManageUser 以管理员身份执行一次 ManageUser 并返回响应体。
func callManageUser(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("id", 9999)
	c.Set("role", common.RoleRootUser)
	c.Set("username", "admin")
	c.Request = httptest.NewRequest(http.MethodPost, "/api/user/manage", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	ManageUser(c)
	return w
}

func TestManageUser_AddPointsRequiresExpireMode(t *testing.T) {
	setupPointsAdminControllerDB(t)
	seedManagedUser(t, 1, 0)

	w := callManageUser(t, `{"id":1,"action":"add_points","mode":"add","value":6849}`)
	require.Contains(t, w.Body.String(), `"success":false`)
	require.Contains(t, w.Body.String(), "请选择积分有效期")

	var balance int64
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 1).
		Select("points_balance").Scan(&balance).Error)
	require.Zero(t, balance, "被拒绝时不得改动余额")
}

func TestManageUser_AddPointsNeverCreatesPermanentLot(t *testing.T) {
	setupPointsAdminControllerDB(t)
	seedManagedUser(t, 2, 0)

	w := callManageUser(t, `{"id":2,"action":"add_points","mode":"add","value":6849,"points_expire_mode":"never"}`)
	require.Contains(t, w.Body.String(), `"success":true`)

	var lots []model.PointsLot
	require.NoError(t, model.DB.Where("user_id = ? AND source = ?", 2, model.FundSourceAdminAdjust).
		Find(&lots).Error)
	require.Len(t, lots, 1)
	require.EqualValues(t, 6849, lots[0].Amount)
	require.Zero(t, lots[0].ExpiresAt, "永不过期")
}

func TestManageUser_AddPointsDaysCreatesExpiringLot(t *testing.T) {
	setupPointsAdminControllerDB(t)
	seedManagedUser(t, 3, 0)

	w := callManageUser(t, `{"id":3,"action":"add_points","mode":"add","value":6849,"points_expire_mode":"days","points_expire_days":30}`)
	require.Contains(t, w.Body.String(), `"success":true`)

	var lots []model.PointsLot
	require.NoError(t, model.DB.Where("user_id = ? AND source = ?", 3, model.FundSourceAdminAdjust).
		Find(&lots).Error)
	require.Len(t, lots, 1)
	require.Equal(t, model.ExpireAtDays(common.GetTimestamp(), 30), lots[0].ExpiresAt)

	var expiresAt int64 = lots[0].ExpiresAt
	require.Equal(t, 23, time.Unix(expiresAt, 0).In(time.FixedZone("CST", 8*3600)).Hour())
	require.Equal(t, 59, time.Unix(expiresAt, 0).In(time.FixedZone("CST", 8*3600)).Minute())
}

func TestManageUser_AddPointsRejectsZeroDays(t *testing.T) {
	setupPointsAdminControllerDB(t)
	seedManagedUser(t, 4, 0)

	w := callManageUser(t, `{"id":4,"action":"add_points","mode":"add","value":6849,"points_expire_mode":"days","points_expire_days":0}`)
	require.Contains(t, w.Body.String(), `"success":false`)
	require.Contains(t, w.Body.String(), "有效期需在")
}

func TestManageUser_OverrideRejectsNegative(t *testing.T) {
	setupPointsAdminControllerDB(t)
	seedManagedUser(t, 5, 1000)

	w := callManageUser(t, `{"id":5,"action":"add_points","mode":"override","value":-1,"points_expire_mode":"never"}`)
	require.Contains(t, w.Body.String(), `"success":false`)
	require.Contains(t, w.Body.String(), "积分余额不能为负")

	var balance int64
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 5).
		Select("points_balance").Scan(&balance).Error)
	require.EqualValues(t, 1000, balance)
}

func TestManageUser_OverrideUpCreatesLotAndDownOnlyReducesBalance(t *testing.T) {
	setupPointsAdminControllerDB(t)
	seedManagedUser(t, 6, 1000)

	// 调高：多出的部分必须建批次（否则下次 sync 会把它当成 surplus）
	w := callManageUser(t, `{"id":6,"action":"add_points","mode":"override","value":3000,"points_expire_mode":"never"}`)
	require.Contains(t, w.Body.String(), `"success":true`)

	var lots []model.PointsLot
	require.NoError(t, model.DB.Where("user_id = ? AND source = ?", 6, model.FundSourceAdminAdjust).
		Find(&lots).Error)
	require.Len(t, lots, 1)
	require.EqualValues(t, 2000, lots[0].Amount)

	// 调低：只减余额，不新增批次
	w = callManageUser(t, `{"id":6,"action":"add_points","mode":"override","value":500,"points_expire_mode":"never"}`)
	require.Contains(t, w.Body.String(), `"success":true`)

	var balance int64
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", 6).
		Select("points_balance").Scan(&balance).Error)
	require.EqualValues(t, 500, balance)

	var adjustLots int64
	require.NoError(t, model.DB.Model(&model.PointsLot{}).
		Where("user_id = ? AND source = ?", 6, model.FundSourceAdminAdjust).Count(&adjustLots).Error)
	require.EqualValues(t, 1, adjustLots, "调低不得新增批次")

	// 流水净额必须等于余额变化：+2000（调高）−2500（调低）= −500。
	// 旧实现是提交后 best-effort 补记，可能与余额不一致；现在同事务写入。
	var ledger int64
	require.NoError(t, model.DB.Model(&model.FundEntry{}).
		Where("user_id = ? AND account = ?", 6, model.FundAccountPoints).
		Select("COALESCE(SUM(quota_delta), 0)").Scan(&ledger).Error)
	require.EqualValues(t, -500, ledger)
}

func TestHandleFundOperation_GiftRequiresExpireMode(t *testing.T) {
	setupPointsAdminControllerDB(t)
	seedManagedUser(t, 7, 0)

	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Set("id", 9999)
	c.Set("username", "admin")
	handleFundOperation(c, &model.User{Id: 7}, ManageRequest{
		Action: "fund_op", Op: FundOpGift, Value: 6849, Remark: "活动赠送",
	})
	require.Contains(t, w.Body.String(), `"success":false`)
	require.Contains(t, w.Body.String(), "请选择积分有效期")

	// 带 never 则成功，并建出永不过期批次
	w2 := httptest.NewRecorder()
	c2, _ := gin.CreateTestContext(w2)
	c2.Set("id", 9999)
	c2.Set("username", "admin")
	handleFundOperation(c2, &model.User{Id: 7}, ManageRequest{
		Action: "fund_op", Op: FundOpGift, Value: 6849, Remark: "活动赠送",
		PointsExpireMode: "never",
	})
	require.Contains(t, w2.Body.String(), `"success":true`)

	var lots []model.PointsLot
	require.NoError(t, model.DB.Where("user_id = ? AND source = ?", 7, model.FundSourceAdminGift).
		Find(&lots).Error)
	require.Len(t, lots, 1)
	require.EqualValues(t, 6849, lots[0].Amount)
}
