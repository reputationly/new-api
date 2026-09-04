package model

import (
	"os"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// 模型列表的展示顺序:优先级降序 → 名字小写后升序(判据见 LessByDisplayOrder)。
//
// 模型广场与体验区共用那个判据函数,所以不必再造一份前端夹具 —— 规则只有一处实现,
// 测一处即可。曾经两边各写一份、靠约定保持一致,前后错过两次。
var displayOrderFixture = []struct {
	model    string
	priority int64
}{
	{"own-gpustack", 100}, // 自建算力:优先级最高,排在最前
	{"own-mid", 50},       // 来自第二个分组,必须排进全局位置而不是追加在末尾
	{"Zebra", 0},          // 与下面的 apple 一起,守「小写后再比」
	{"apple", 0},          // 不转小写的话 'Z'(90) < 'a'(97),Zebra 会排到 apple 前面
	{"legacy-null", 0},    // 会被下面改成真 NULL,验证它仍按 0 计
}

// 期望顺序。
var displayOrderWant = []string{
	"own-gpustack", // 100
	"cross-group",  // 80,高优先级渠道挂在**目标分组之外**,仍按全局最大值排(见 seed)
	"own-mid",      // 50
	"apple",        // 以下同为 0,按小写名升序
	"legacy-null",
	"Zebra",
}

func seedDisplayOrder(t *testing.T, db *gorm.DB) {
	t.Helper()
	for i, f := range displayOrderFixture {
		g := "default"
		if f.model == "own-mid" {
			g = "premium" // 跨分组:一次查才排得进全局位置
		}
		p := f.priority
		if err := db.Create(&Ability{
			Group: g, Model: f.model, ChannelId: i + 1, Enabled: true, Priority: &p,
		}).Error; err != nil {
			t.Fatalf("create ability %s: %v", f.model, err)
		}
	}
	// 同一个模型多挂一个低优先级渠道:聚合取 MAX,不该被压下去。
	zero := int64(0)
	if err := db.Create(&Ability{
		Group: "default", Model: "own-gpustack", ChannelId: 90, Enabled: true, Priority: &zero,
	}).Error; err != nil {
		t.Fatalf("create second channel: %v", err)
	}
	// 停用的、以及**只**存在于非目标分组的,都不该出现,哪怕优先级最高。
	high := int64(999)
	db.Create(&Ability{Group: "default", Model: "disabled", ChannelId: 91, Enabled: false, Priority: &high})
	db.Create(&Ability{Group: "secret", Model: "hidden", ChannelId: 92, Enabled: true, Priority: &high})

	// 跨分组:模型在目标分组里只有 0 优先级渠道,高优先级那个挂在用户看不到的分组。
	// **优先级取全局最大值**,所以它按 80 排,而不是按 0 沉底。
	//
	// 这条守的是与模型广场的一致性:广场那份数据是包级全局缓存、没有"当前用户"可言,
	// 只能全局聚合;这边若改成只看目标分组,同一个模型在两页就会拿到不同名次 ——
	// 那正是本次改动要消除的分歧。改这条之前先想清楚广场那边怎么办。
	crossHigh, crossZero := int64(80), int64(0)
	db.Create(&Ability{Group: "default", Model: "cross-group", ChannelId: 93, Enabled: true, Priority: &crossZero})
	db.Create(&Ability{Group: "secret", Model: "cross-group", ChannelId: 94, Enabled: true, Priority: &crossHigh})

	// 历史遗留的 NULL 行只能这样造:priority 列带 DEFAULT 0,GORM 遇到 Priority 为 nil
	// 会省略该列、落到默认值(单条与批量 Create 都实测确认过),写不出 NULL。
	if err := db.Exec(`UPDATE abilities SET priority = NULL WHERE model = ?`, "legacy-null").Error; err != nil {
		t.Fatalf("set null: %v", err)
	}
}

func assertDisplayOrder(t *testing.T) {
	t.Helper()
	// 优先级来自定价缓存,种完数据必须让它重算,否则读到的是上一个用例的残留。
	InvalidatePricingCache()
	got := GetGroupsEnabledModelsOrdered([]string{"default", "premium"})
	if len(got) != len(displayOrderWant) {
		t.Fatalf("模型 = %v, want %v", got, displayOrderWant)
	}
	for i := range displayOrderWant {
		if got[i] != displayOrderWant[i] {
			t.Fatalf("顺序 = %v, want %v", got, displayOrderWant)
		}
	}
}

func TestGetGroupsEnabledModelsOrdered(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:ability_display_order?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.Exec(`DROP TABLE IF EXISTS abilities`)
	// Model / Vendor / Channel 一起建:优先级现在读 GetModelMaxPriorities 那份共享缓存,
	// 它由 updatePricing 构建,而后者会 join channels、查 models 与 vendors。缺表则整个
	// 刷新提前返回、映射为空,所有模型都按 0 排 —— 用例会退化成一句只验字母序的空断言。
	if err := db.AutoMigrate(&Ability{}, &Model{}, &Vendor{}, &Channel{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	t.Cleanup(func() {
		db.Exec(`DROP TABLE IF EXISTS abilities`)
		InvalidatePricingCache()
	})

	origDB, origCol := DB, commonGroupCol
	DB, commonGroupCol = db, "`group`"
	defer func() { DB, commonGroupCol = origDB, origCol }()

	seedDisplayOrder(t, db)
	assertDisplayOrder(t)

	if m := GetGroupsEnabledModelsOrdered(nil); len(m) != 0 {
		t.Fatalf("空分组应返回空切片, got %v", m)
	}
}

// PostgreSQL 版:同一批夹具、同一份期望。
//
// 守的是**跨库一致**。排序原本写在 SQL 的 ORDER BY 里,而字符串排序依 collation 而定:
// 拿线上 53 个真实模型名实测,SQLite 与 PostgreSQL 的结果有 53 行不同,加了 LOWER() 仍
// 差 18 行 —— 开发机验过的顺序在线上根本不成立。排序搬进 Go 之后两边才一致,这条用例
// 就是那个结论的回归防线。夹具里 Zebra / apple 的大小写正为此准备:SQLite 的 BINARY
// 把大写排在小写前、PG 的默认 collation 不是,若哪天有人把排序挪回 SQL,这两条在两个
// 库上会给出不同答案,至少有一边会红。
//
// 需要一个可写的 PG,通过 TEST_PG_DSN 提供;没有就跳过,不拖累无 PG 的开发机与 CI。
// 本地起法:
//
//	docker run -d --name pgtest -e POSTGRES_PASSWORD=test -e POSTGRES_DB=abtest \
//	  -p 127.0.0.1:55432:5432 postgres:15
//	TEST_PG_DSN='postgres://postgres:test@127.0.0.1:55432/abtest?sslmode=disable' \
//	  go test ./model/ -run TestGetGroupsEnabledModelsOrderedPostgres -v
func TestGetGroupsEnabledModelsOrderedPostgres(t *testing.T) {
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("需要 TEST_PG_DSN 指向一个可写的 PostgreSQL；见本用例注释")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	db.Exec(`DROP TABLE IF EXISTS abilities`)
	// Model / Vendor / Channel 一起建:优先级现在读 GetModelMaxPriorities 那份共享缓存,
	// 它由 updatePricing 构建,而后者会 join channels、查 models 与 vendors。缺表则整个
	// 刷新提前返回、映射为空,所有模型都按 0 排 —— 用例会退化成一句只验字母序的空断言。
	if err := db.AutoMigrate(&Ability{}, &Model{}, &Vendor{}, &Channel{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	t.Cleanup(func() {
		db.Exec(`DROP TABLE IF EXISTS abilities`)
		InvalidatePricingCache()
	})

	origDB, origCol := DB, commonGroupCol
	DB, commonGroupCol = db, `"group"`
	defer func() { DB, commonGroupCol = origDB, origCol }()

	seedDisplayOrder(t, db)
	assertDisplayOrder(t)
}

// 模型广场那份列表也必须按同一判据排好 —— 排序发生在 updatePricing 构建 pricingMap 时
// (model/pricing.go),不再由前端做。
//
// **这条守的是那句 sort 调用本身**:删掉它,列表顺序就退回 map 遍历序(Go 的 map 遍历
// 是随机的),表现为模型广场每刷新一次顺序都不一样,而没有任何报错。前端已经不再排序,
// 也就没有第二道防线了。
//
// 顺带守住排序与 PricingVersion 赋值的**先后**:那句标记打在 pricingMap[0] 上,若排在
// 排序之前,标记会跟着首元素换位置。
func TestPricingListIsSortedByDisplayOrder(t *testing.T) {
	db, err := gorm.Open(
		sqlite.Open("file:pricing_display_order?mode=memory&cache=shared"),
		&gorm.Config{},
	)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// Channel 必须一起建:GetAllEnableAbilityWithChannels 会 join channels,缺表则整个
	// updatePricing 提前返回、列表为空,用例会变成一句什么都没验的空断言。
	if err := db.AutoMigrate(&Ability{}, &Model{}, &Vendor{}, &Channel{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	t.Cleanup(func() {
		db.Exec(`DROP TABLE IF EXISTS abilities`)
		InvalidatePricingCache()
	})

	origDB, origCol := DB, commonGroupCol
	DB, commonGroupCol = db, "`group`"
	defer func() { DB, commonGroupCol = origDB, origCol }()

	// **模型要够多、且要反复重建**。pricingMap 是遍历 map 构建的(for model := range
	// modelGroupsMap),而 Go 的 map 遍历顺序是随机的 —— 只放三个模型时,即使把排序整个
	// 删掉,也有 1/6 的概率蒙对,用例就成了靠运气过的假测试(实测确认过)。
	// 六个模型 + 重复五次,蒙对的概率是 (1/720)^5,可以当作零。
	//
	// 名字刻意与优先级反着排:只按名字排会得到相反的顺序,分得出"到底按什么排的"。
	high, mid, zero := int64(100), int64(50), int64(0)
	seed := []struct {
		model    string
		priority int64
	}{
		{"zzz-own", high},
		{"yyy-own2", high},
		{"xxx-mid", mid},
		{"aaa-third", zero},
		{"Mmm-third", zero},
		{"bbb-third", zero},
	}
	for i, sd := range seed {
		p := sd.priority
		if err := db.Create(&Ability{
			Group: "default", Model: sd.model, ChannelId: i + 1, Enabled: true, Priority: &p,
		}).Error; err != nil {
			t.Fatalf("create %s: %v", sd.model, err)
		}
	}
	want := []string{
		"yyy-own2",  // 100，同分按小写名升序，y < z
		"zzz-own",   // 100
		"xxx-mid",   // 50
		"aaa-third", // 以下同为 0
		"bbb-third",
		"Mmm-third",
	}

	for round := 0; round < 5; round++ {
		InvalidatePricingCache()
		got := GetPricing()
		names := make([]string, 0, len(got))
		for _, p := range got {
			names = append(names, p.ModelName)
		}
		if len(names) != len(want) {
			t.Fatalf("第 %d 轮 模型 = %v, want %v", round, names, want)
		}
		for i := range want {
			if names[i] != want[i] {
				t.Fatalf("第 %d 轮 顺序 = %v, want %v", round, names, want)
			}
		}
		// 版本标记必须仍在首元素上,即排序发生在那句赋值**之前**。
		if got[0].PricingVersion == "" {
			t.Fatalf("第 %d 轮 PricingVersion 不在首元素上，排序与赋值的先后反了", round)
		}
	}
}
