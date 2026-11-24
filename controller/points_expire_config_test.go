package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// AC23：§八 的全部有效期字段都要能通过现有管理接口写入并持久化，越界值要被拒绝。
//
// 一审发现的问题正是「字段声明了、界面也有，但根本没接进写路径」：
// 兑换码创建/编辑不复制、`Redemption.Update()` 的 Select 不含它、套餐的更新白名单
// 不含它——表现是保存成功但刷新后又变回 0。

func setupExpireConfigDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + strings.ReplaceAll(t.Name(), "/", "_") + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&model.Option{}, &model.Redemption{}, &model.TopupPackage{}, &model.User{}, &model.Log{},
	))

	orig, origLog := model.DB, model.LOG_DB
	origRedis := common.RedisEnabled
	origSQLite := common.UsingSQLite
	origOptionMap := common.OptionMap
	model.DB, model.LOG_DB = db, db
	common.RedisEnabled = false
	common.UsingSQLite = true
	// UpdateOption 会写 common.OptionMap；测试环境没有 InitOptionMap，需先备好
	common.OptionMap = map[string]string{}
	t.Cleanup(func() {
		model.DB, model.LOG_DB = orig, origLog
		common.RedisEnabled = origRedis
		common.UsingSQLite = origSQLite
		common.OptionMap = origOptionMap
	})

	ps := operation_setting.GetPointsSetting()
	origEnabled := ps.Enabled
	ps.Enabled = true
	t.Cleanup(func() { ps.Enabled = origEnabled })
	return db
}

func postJSON(t *testing.T, handler gin.HandlerFunc, body string, ctxKeys map[string]int) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	for k, v := range ctxKeys {
		c.Set(k, v)
	}
	c.Request = httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	return w
}

func putJSON(t *testing.T, handler gin.HandlerFunc, body string, ctxKeys map[string]int) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	for k, v := range ctxKeys {
		c.Set(k, v)
	}
	c.Request = httptest.NewRequest(http.MethodPut, "/x", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	handler(c)
	return w
}

// --- 积分设置 / 签到设置：经 option 接口写入并持久化 ---

func optionValueFromDB(t *testing.T, key string) string {
	t.Helper()
	var opt model.Option
	require.NoError(t, model.DB.Where("key = ?", key).First(&opt).Error)
	return opt.Value
}

func TestExpireConfig_OptionFieldsPersist(t *testing.T) {
	setupExpireConfigDB(t)

	cases := []struct {
		key   string
		value int
	}{
		{"points_setting.new_user_expire_days", 30},
		{"points_setting.kyc_verified_expire_days", 90},
		{"points_setting.kyc_inviter_expire_days", 180},
		{"checkin_setting.points_expire_weeks", 2},
	}
	for _, c := range cases {
		w := putJSON(t, UpdateOption,
			`{"key":"`+c.key+`","value":`+strconv.Itoa(c.value)+`}`,
			map[string]int{"id": 1})
		require.Contains(t, w.Body.String(), `"success":true`, "%s 应可写入", c.key)
		require.Equal(t, strconv.Itoa(c.value), optionValueFromDB(t, c.key), "%s 应持久化", c.key)
	}
}

func TestExpireConfig_OptionFieldsRejectOutOfRange(t *testing.T) {
	setupExpireConfigDB(t)

	bad := []struct {
		body string
		why  string
	}{
		{`{"key":"points_setting.new_user_expire_days","value":-1}`, "负数"},
		{`{"key":"points_setting.new_user_expire_days","value":3651}`, "超过天数上限"},
		{`{"key":"points_setting.kyc_verified_expire_days","value":99999}`, "超过天数上限"},
		{`{"key":"points_setting.kyc_inviter_expire_days","value":-5}`, "负数"},
		{`{"key":"checkin_setting.points_expire_weeks","value":53}`, "超过周数上限"},
		{`{"key":"checkin_setting.points_expire_weeks","value":-1}`, "负数"},
		{`{"key":"points_setting.new_user_expire_days","value":"abc"}`, "非整数"},
	}
	for _, b := range bad {
		w := putJSON(t, UpdateOption, b.body, map[string]int{"id": 1})
		require.Contains(t, w.Body.String(), `"success":false`, "%s 应被拒绝", b.why)
		require.Contains(t, w.Body.String(), "积分有效期", "%s 应给出可读提示", b.why)
	}

	// 边界值 0 与上限本身必须可写入（0 = 永不过期，不是非法值）
	for _, v := range []int{0, operation_setting.MaxPointsExpireDays} {
		w := putJSON(t, UpdateOption,
			`{"key":"points_setting.new_user_expire_days","value":`+strconv.Itoa(v)+`}`,
			map[string]int{"id": 1})
		require.Contains(t, w.Body.String(), `"success":true`, "边界值 %d 应可写入", v)
	}
	// 周数上限本身同样可写入
	w := putJSON(t, UpdateOption,
		`{"key":"checkin_setting.points_expire_weeks","value":`+strconv.Itoa(operation_setting.MaxPointsExpireWeeks)+`}`,
		map[string]int{"id": 1})
	require.Contains(t, w.Body.String(), `"success":true`)
}

// --- 兑换码：创建与编辑都要落库 ---

func TestExpireConfig_RedemptionPointsExpireDaysPersists(t *testing.T) {
	setupExpireConfigDB(t)

	w := postJSON(t, AddRedemption,
		`{"name":"积分码","count":1,"quota":68493,"reward_type":"points","points_expire_days":45}`,
		map[string]int{"id": 1})
	require.Contains(t, w.Body.String(), `"success":true`)

	var created model.Redemption
	require.NoError(t, model.DB.Order("id").First(&created).Error)
	require.Equal(t, 45, created.PointsExpireDays, "创建时必须落库")

	// 编辑：这次的坑就在 `Redemption.Update()` 的 Select 白名单不含该列
	w = putJSON(t, UpdateRedemption,
		`{"id":`+strconv.Itoa(created.Id)+`,"name":"积分码","quota":68493,"expired_time":0,"points_expire_days":7}`,
		map[string]int{"id": 1})
	require.Contains(t, w.Body.String(), `"success":true`)

	var updated model.Redemption
	require.NoError(t, model.DB.Where("id = ?", created.Id).First(&updated).Error)
	require.Equal(t, 7, updated.PointsExpireDays, "编辑后必须落库（原先会被 Update 的 Select 丢掉）")
}

func TestExpireConfig_RedemptionRejectsOutOfRange(t *testing.T) {
	setupExpireConfigDB(t)

	for _, b := range []string{
		`{"name":"积分码","count":1,"quota":68493,"reward_type":"points","points_expire_days":-1}`,
		`{"name":"积分码","count":1,"quota":68493,"reward_type":"points","points_expire_days":3651}`,
	} {
		w := postJSON(t, AddRedemption, b, map[string]int{"id": 1})
		require.Contains(t, w.Body.String(), `"success":false`, "越界应被拒绝：%s", b)
		require.Contains(t, w.Body.String(), "兑入积分有效期")
	}

	// 边界 0 与上限可写入
	for _, days := range []int{0, operation_setting.MaxPointsExpireDays} {
		w := postJSON(t, AddRedemption,
			`{"name":"积分码","count":1,"quota":68493,"reward_type":"points","points_expire_days":`+
				strconv.Itoa(days)+`}`, map[string]int{"id": 1})
		require.Contains(t, w.Body.String(), `"success":true`, "边界值 %d 应可写入", days)
	}
}

// --- 充值套餐：创建与编辑都要落库 ---

func TestExpireConfig_TopupPackagePointsExpireDaysPersists(t *testing.T) {
	setupExpireConfigDB(t)

	w := postJSON(t, CreateTopupPackage,
		`{"title":"套餐A","price_amount":100,"currency":"CNY","grant_points":500,"grant_points_expire_days":60}`,
		map[string]int{"id": 1})
	require.Contains(t, w.Body.String(), `"success":true`)

	var pkg model.TopupPackage
	require.NoError(t, model.DB.Order("id").First(&pkg).Error)
	require.Equal(t, 60, pkg.GrantPointsExpireDays, "创建时必须落库")

	// 编辑：这次的坑就在 topupPackageUpdateSelectFields() 不含该列
	w = putJSON(t, UpdateTopupPackage,
		`{"id":`+strconv.Itoa(pkg.Id)+`,"title":"套餐A","price_amount":100,"currency":"CNY","grant_points":500,"grant_points_expire_days":15}`,
		map[string]int{"id": 1})
	require.Contains(t, w.Body.String(), `"success":true`)

	var updated model.TopupPackage
	require.NoError(t, model.DB.Where("id = ?", pkg.Id).First(&updated).Error)
	require.Equal(t, 15, updated.GrantPointsExpireDays, "编辑后必须落库（原先会被更新白名单丢掉）")
}

func TestExpireConfig_TopupPackageRejectsOutOfRange(t *testing.T) {
	setupExpireConfigDB(t)

	for _, b := range []string{
		`{"title":"套餐A","price_amount":100,"grant_points":500,"grant_points_expire_days":-1}`,
		`{"title":"套餐A","price_amount":100,"grant_points":500,"grant_points_expire_days":3651}`,
	} {
		w := postJSON(t, CreateTopupPackage, b, map[string]int{"id": 1})
		require.Contains(t, w.Body.String(), `"success":false`, "越界应被拒绝：%s", b)
		require.Contains(t, w.Body.String(), "赠送积分有效期")
	}

	// 更新白名单必须包含该列，否则「编辑时静默丢字段」会复发
	found := false
	for _, f := range model.TopupPackageUpdateSelectFieldsForTest() {
		if f == "grant_points_expire_days" {
			found = true
		}
	}
	require.True(t, found, "topupPackageUpdateSelectFields() 必须包含 grant_points_expire_days")
}
