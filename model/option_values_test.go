package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

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
