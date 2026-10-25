package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/ovcaps"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// `GET /v1/ov/capabilities` 的**整条链**用例：令牌鉴权 → 生效分组 → 渠道能力 →
// 令牌白名单 → 目录拼装 → JSON。
//
// 单元测试（`service/ovcaps`）只喂了一个 Scope，证明不了"上下文里的分组/白名单
// 真的接上了"。这里用内存 SQLite 起真库、真中间件，把这几个环节串起来。
//
// 用到的既有事实（读代码确认）：
//   - `TokenAuth` 从 `Authorization: Bearer sk-<key>` 取 key，去掉 `sk-` 后按 `-` 截断；
//   - 令牌分组为空时回落到用户分组，并写进 `context key "group"`；
//   - `ModelLimitsEnabled` 打开时，白名单以 `map[string]bool` 挂在 `token_model_limit`；
//   - 聚合模型没有渠道 ability，靠"每一段都有渠道"判定。
func TestOvCapabilitiesEndToEnd(t *testing.T) {
	db := setupOvCapabilitiesDB(t)

	// 三条聚合流水线（出厂配置）：帧族 2K、参考族 2K、图片增强。
	withAggregateConfig(t, common.DefaultAggregateModelConfig)

	// 只给"裸模型"建 ability —— 聚合模型本来就不在 abilities 表里。
	for _, name := range []string{
		"qwen-image-pro", "qwen-image", "qwen-image-edit",
		"minimax-h3-fl2va", "minimax-h3-ref2va", "swiftvr",
		"minimax-music3", "indextts-2.5",
	} {
		require.NoError(t, db.Create(&model.Ability{Group: "default", Model: name, ChannelId: 1, Enabled: true}).Error)
	}

	engine := ovCapabilitiesEngine()

	t.Run("全量令牌：四个模态都在，聚合模型按分段可用性报出", func(t *testing.T) {
		body := getOvCapabilities(t, engine, createOvToken(t, db, "ovfull", false, ""))
		require.NotEmpty(t, body.Version)
		models := map[string]string{}
		for _, capability := range body.Capabilities {
			models[capability.Model] = capability.Operation
		}
		require.Equal(t, ovcaps.OperationGenerateImage, models["qwen-image-pro-enhanced"])
		require.Equal(t, ovcaps.OperationGenerateVideo, models["minimax-h3-2k"])
		require.Equal(t, ovcaps.OperationGenerateVideo, models["minimax-h3-ref-2k"])
		require.Equal(t, ovcaps.OperationGenerateSpeech, models["indextts-2.5"])
		require.Equal(t, ovcaps.OperationGenerateMusic, models["minimax-music3"])
		// 图片聚合的生成段在，超分段也在 —— 这条流水线兑现得了它承诺的东西。
		defaults := 0
		for _, capability := range body.Capabilities {
			if capability.Default {
				defaults++
			}
		}
		require.GreaterOrEqual(t, defaults, 3, "每个操作应有一条默认")
	})

	t.Run("令牌白名单只放开图片聚合：其余一条都不报", func(t *testing.T) {
		body := getOvCapabilities(t, engine, createOvToken(t, db, "ovlimit", true, "qwen-image-pro-enhanced"))
		require.Len(t, body.Capabilities, 1)
		require.Equal(t, "qwen-image-pro-enhanced", body.Capabilities[0].Model)
	})

	t.Run("生成段没有渠道时，聚合模型整条消失", func(t *testing.T) {
		// 停掉帧族的生成段：`minimax-h3-2k` 这条流水线跑不起来了。
		require.NoError(t, db.Model(&model.Ability{}).
			Where("model = ?", "minimax-h3-fl2va").Update("enabled", false).Error)
		t.Cleanup(func() {
			_ = db.Model(&model.Ability{}).Where("model = ?", "minimax-h3-fl2va").Update("enabled", true).Error
		})

		body := getOvCapabilities(t, engine, createOvToken(t, db, "ovhalf", false, ""))
		for _, capability := range body.Capabilities {
			require.NotEqual(t, "minimax-h3-2k", capability.Model, "生成段没渠道时不该报出这条聚合模型")
		}
		// 参考族是另一条流水线，不受影响。
		found := false
		for _, capability := range body.Capabilities {
			if capability.Model == "minimax-h3-ref-2k" {
				found = true
			}
		}
		require.True(t, found, "参考族不该被牵连")
	})

	t.Run("目录版本只跟内容有关", func(t *testing.T) {
		token := createOvToken(t, db, "ovver", false, "")
		first := getOvCapabilities(t, engine, token)
		second := getOvCapabilities(t, engine, token)
		require.Equal(t, first.Version, second.Version)
		require.NotEmpty(t, first.Version)
	})

	t.Run("没有令牌是 401，不是 404", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		engine.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/v1/ov/capabilities", nil))
		require.Equal(t, http.StatusUnauthorized, recorder.Code)
	})
}

type ovCapabilitiesBody struct {
	Version      string                 `json:"version"`
	Capabilities []ovcaps.Capability    `json:"capabilities"`
	Error        map[string]interface{} `json:"error"`
}

func ovCapabilitiesEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	group := engine.Group("/v1/ov")
	// 与 router/ov-router.go 里的顺序一致；限流在鉴权之前，但这里不装限流器 ——
	// 它有自己的用例（router/ov_router_test.go）。
	group.Use(middleware.RouteTag("ov"))
	group.Use(middleware.TokenAuth())
	group.Use(middleware.KYCRequired())
	group.GET("/capabilities", GetOvCapabilities)
	return engine
}

func getOvCapabilities(t *testing.T, engine *gin.Engine, tokenKey string) ovCapabilitiesBody {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/v1/ov/capabilities", nil)
	request.Header.Set("Authorization", "Bearer sk-"+tokenKey)
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusOK, recorder.Code, "响应体：%s", recorder.Body.String())
	var body ovCapabilitiesBody
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &body))
	return body
}

func createOvToken(t *testing.T, db *gorm.DB, key string, limitsEnabled bool, limits string) string {
	t.Helper()
	token := &model.Token{
		UserId:             1,
		Key:                key,
		Status:             common.TokenStatusEnabled,
		Name:               key,
		CreatedTime:        common.GetTimestamp(),
		AccessedTime:       common.GetTimestamp(),
		ExpiredTime:        -1,
		UnlimitedQuota:     true,
		ModelLimitsEnabled: limitsEnabled,
		ModelLimits:        limits,
	}
	require.NoError(t, db.Create(token).Error)
	return key
}

func setupOvCapabilitiesDB(t *testing.T) *gorm.DB {
	t.Helper()
	// `commonKeyCol` / `commonGroupCol` 是 `model.InitDB()` 里按库型设的（`group` / `key`
	// 在部分库里是保留字）。这里复用同包已有的初始化助手，否则 token 查询会拼出
	// `WHERE  = ?` 这种空列名。
	initModelListColumnNames(t)
	gin.SetMode(gin.TestMode)
	common.UsingSQLite = true
	common.UsingMySQL = false
	common.UsingPostgreSQL = false
	common.RedisEnabled = false
	common.KYCEnabled = false
	t.Cleanup(func() { common.KYCEnabled = false })

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}, &model.Ability{}, &model.Token{}))
	require.NoError(t, db.Create(&model.User{
		Id:       1,
		Username: "ov-capabilities",
		Password: "0123456789",
		Status:   common.UserStatusEnabled,
		Role:     common.RoleCommonUser,
		Group:    "default",
		AffCode:  "ovcap",
	}).Error)
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func withAggregateConfig(t *testing.T, raw string) {
	t.Helper()
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = map[string]string{}
	}
	previous, existed := common.OptionMap["AggregateModelConfig"]
	common.OptionMap["AggregateModelConfig"] = raw
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if existed {
			common.OptionMap["AggregateModelConfig"] = previous
			return
		}
		delete(common.OptionMap, "AggregateModelConfig")
	})
}
