package model

import (
	"bytes"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

func TestLoadOptionsWarnsAboutLegacyQuotaDisplayOnlyAtStartup(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&Option{}))
	require.NoError(t, db.Create(&Option{Key: "general_setting.quota_display_type", Value: "USD"}).Error)
	previousDB := DB
	DB = db
	t.Cleanup(func() { DB = previousDB })
	logged := legacyQuotaDisplayWarningLogged.Swap(false)
	t.Cleanup(func() { legacyQuotaDisplayWarningLogged.Store(logged) })

	var output bytes.Buffer
	common.LogWriterMu.Lock()
	previousWriter := gin.DefaultWriter
	gin.DefaultWriter = &output
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter = previousWriter
		common.LogWriterMu.Unlock()
	})

	loadOptionsFromDatabase(false)
	require.False(t, legacyQuotaDisplayWarningLogged.Load())
	loadOptionsFromDatabase(true)
	loadOptionsFromDatabase(true)
	require.Equal(t, operation_setting.QuotaDisplayTypeCNY, operation_setting.GetQuotaDisplayType())
	common.LogWriterMu.Lock()
	warnings := output.String()
	common.LogWriterMu.Unlock()
	require.Equal(t, 1, strings.Count(warnings, `WARNING: ignoring legacy general_setting.quota_display_type="USD"`))
}

// OptionValuesFromDatabase 存在的唯一理由：多副本下本副本内存里的设置可能还是旧的。
//
// 这里构造的正是那个现场 —— 内存里是旧值、库里是新值；取出来的必须是库里那份。
// 没有它的话，设置页在「保存成功 → 刷新 → 请求落到别的副本」时会显示旧值，
// 看起来就像保存被还原了（而库里一直是对的）。
func TestOptionValuesFromDatabasePrefersStoredValue(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:option_values_from_db?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&Option{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	prevDB := DB
	DB = db
	defer func() { DB = prevDB }()

	// 内存 = 旧值（这个副本还没同步到）；库 = 新值（另一个副本刚写进去的）。
	prevMap := common.OptionMap
	common.OptionMap = map[string]string{
		"HiloCatalog": "旧值",
		// 只在内存里有、从没存过库的键（例如运行期算出来的默认值）：
		// 读库不能把它弄丢。
		"OnlyInMemory": "内存默认值",
	}
	defer func() { common.OptionMap = prevMap }()

	if err := db.Create(&Option{Key: "HiloCatalog", Value: "新值"}).Error; err != nil {
		t.Fatalf("seed option: %v", err)
	}
	// 旧版本写过、这个版本已经不认的键：不能因为它在库里就冒到设置页去。
	if err := db.Create(&Option{Key: "RetiredKey", Value: "x"}).Error; err != nil {
		t.Fatalf("seed retired option: %v", err)
	}

	values := OptionValuesFromDatabase()

	if values["HiloCatalog"] != "新值" {
		t.Errorf("HiloCatalog = %q，应当以库里那份为准（%q）", values["HiloCatalog"], "新值")
	}
	if values["OnlyInMemory"] != "内存默认值" {
		t.Errorf("OnlyInMemory = %q，只存在于内存的键不该被读库弄丢", values["OnlyInMemory"])
	}
	if _, ok := values["RetiredKey"]; ok {
		t.Error("库里已下线的键被塞进了设置页的响应里")
	}
}
