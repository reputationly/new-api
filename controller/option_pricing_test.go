package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestPricingOptionFinalState(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	prevDB, prevOptions := model.DB, common.OptionMap
	prevTimeRatio := ratio_setting.GroupTimeRatio2JSONString()
	model.DB, common.OptionMap = db, map[string]string{}
	t.Cleanup(func() {
		// Clear the runtime settings written by this test before restoring the DB.
		_ = model.UpdateOptions(map[string]string{
			"billing_setting.billing_mode": "{}",
			"billing_setting.billing_expr": "{}",
			"ModelSourceRate":              "{}",
		}, pricingStateKeys, validatePricingFinalState)
		require.NoError(t, ratio_setting.UpdateGroupTimeRatioByJSONString(prevTimeRatio))
		model.DB, common.OptionMap = prevDB, prevOptions
		_ = sqlDB.Close()
	})

	const modeKey, exprKey, rateKey = "billing_setting.billing_mode", "billing_setting.billing_expr", "ModelSourceRate"
	const tiered, expr, rate = `{"m":"tiered_expr"}`, `{"m":"v2:tier(\"base\", p * 1.5 * fx + c * 7.5)"}`, `{"m":7.2}`
	router := gin.New()
	router.POST("/batch", UpdatePricingOptions)
	router.PUT("/option", UpdateOption)
	request := func(method, path string, payload any) *httptest.ResponseRecorder {
		body, e := common.Marshal(payload)
		if e != nil {
			panic(e)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, path, strings.NewReader(string(body))))
		return w
	}
	send := func(method, path string, payload any) bool {
		w := request(method, path, payload)
		var result struct{ Success bool }
		if e := common.Unmarshal(w.Body.Bytes(), &result); e != nil {
			panic(e)
		}
		return result.Success
	}
	batch := func(options map[string]string) bool {
		return send(http.MethodPost, "/batch", optionBatchRequest{Options: options})
	}
	put := func(key, value string) bool {
		return send(http.MethodPut, "/option", OptionUpdateRequest{Key: key, Value: value})
	}
	require.False(t, batch(map[string]string{modeKey: tiered}))
	require.False(t, put(modeKey, tiered))
	require.False(t, batch(map[string]string{rateKey: rate, exprKey: "{"}))
	require.False(t, put(exprKey, "{"))
	require.False(t, batch(map[string]string{rateKey: rate, "VideoPricingConfig": "{"}))
	require.False(t, batch(map[string]string{"GroupTimeRatio": "{"}))
	badRatio := `{"gpt-image-1":"2"}`
	for _, w := range []*httptest.ResponseRecorder{
		request(http.MethodPost, "/batch", optionBatchRequest{Options: map[string]string{"ModelRatio": badRatio}}),
		request(http.MethodPut, "/option", OptionUpdateRequest{Key: "ModelRatio", Value: badRatio}),
	} {
		require.Equal(t, http.StatusOK, w.Code)
		require.Contains(t, w.Body.String(), `"success":false`)
		require.Contains(t, w.Body.String(), "ModelRatio 不是合法的 JSON 数值表")
	}
	var count int64
	require.NoError(t, db.Model(&model.Option{}).Count(&count).Error)
	require.Zero(t, count)

	require.True(t, put(rateKey, rate))
	// The authoritative lookup must work even when this process has not applied the rate.
	require.NoError(t, billing_setting.UpdateModelSourceRateByJSONString("{}"))
	require.True(t, put(exprKey, expr))
	require.True(t, put(modeKey, tiered))
	for _, clear := range []string{"", "null", "{}"} {
		require.False(t, put(exprKey, clear))
		require.False(t, batch(map[string]string{exprKey: clear}))
		require.False(t, put(rateKey, clear))
	}
	require.True(t, batch(map[string]string{modeKey: "{}", exprKey: " "}))
	var row model.Option
	require.NoError(t, db.First(&row, &model.Option{Key: exprKey}).Error)
	require.Equal(t, "{}", row.Value)
	require.Empty(t, billing_setting.GetBillingExprCopy())

	results := make(chan bool, 2)
	go func() { results <- batch(map[string]string{rateKey: "{}"}) }()
	go func() { results <- batch(map[string]string{exprKey: expr}) }()
	require.NotEqual(t, <-results, <-results)

	const timeRatioKey = "GroupTimeRatio"
	const timeRatio = `{"windows":{"off_peak":{"start":"09:00","end":"10:00"}}}`
	require.True(t, batch(map[string]string{timeRatioKey: timeRatio, exprKey: "{}"}))
	var timeRow model.Option
	require.NoError(t, db.First(&timeRow, &model.Option{Key: timeRatioKey}).Error)
	require.Equal(t, timeRatio, timeRow.Value)
	require.Contains(t, ratio_setting.GetGroupTimeRatioCopy().Windows, "off_peak")
	require.True(t, put(timeRatioKey, "{}"))
	require.Empty(t, ratio_setting.GetGroupTimeRatioCopy().Windows)
}
