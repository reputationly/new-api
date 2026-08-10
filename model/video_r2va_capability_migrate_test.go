package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
)

// TestMigrateVideoR2VACapabilityRename 验证「参考生视频」旧标签的一次性改名。
//
// 这个词曾是「视频编辑」的旧名,2026-08 起改指独立的新 tab(MiniMax H3 Ref2VA)。
// 存量配置留着它的模型会同时从「视频编辑」tab 消失、又错误地出现在「参考生视频」tab
// ——后者更危险:Bernini 视频编辑模型跑到那里,提交就是拿 task_type=r2va 打给一个
// 不认识它的模型。故重点断言的是**改完之后 capabilities 里不再有这个词**。
func TestMigrateVideoR2VACapabilityRename(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:video_r2va_capability_migrate?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&Option{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	// 迁移函数走包级 DB，临时指过去，跑完还原。
	prev := DB
	DB = db
	defer func() { DB = prev }()

	// bernini-legacy   ：只有旧标签 —— 最常见的存量形态
	// bernini-both     ：新旧都有 —— 改名不能改出重复项
	// bernini-spaced   ：旧标签带空格 —— 运营手输的配置里常见
	// wan2.2-t2v       ：无关标签 —— 不该被动
	const raw = `{"models":{` +
		`"bernini-legacy":{"capabilities":["参考生视频"],"maxInputMB":50},` +
		`"bernini-both":{"capabilities":["视频编辑","参考生视频","文生视频"]},` +
		`"bernini-spaced":{"capabilities":[" 参考生视频 "]},` +
		`"wan2.2-t2v":{"capabilities":["文生视频","关键帧"]}}}`
	if err := db.Create(&Option{Key: "VideoModelConfig", Value: raw}).Error; err != nil {
		t.Fatalf("seed option: %v", err)
	}

	if err := migrateVideoR2VACapabilityRename(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	capsOf := func(t *testing.T, model string) []string {
		t.Helper()
		var opt Option
		if err := db.Where(Option{Key: "VideoModelConfig"}).First(&opt).Error; err != nil {
			t.Fatalf("read option: %v", err)
		}
		var cfg map[string]interface{}
		if err := common.UnmarshalJsonStr(opt.Value, &cfg); err != nil {
			t.Fatalf("unmarshal config: %v", err)
		}
		models, ok := cfg["models"].(map[string]interface{})
		if !ok {
			t.Fatalf("models missing in %s", opt.Value)
		}
		m, ok := models[model].(map[string]interface{})
		if !ok {
			t.Fatalf("model %s missing in %s", model, opt.Value)
		}
		raw, _ := m["capabilities"].([]interface{})
		out := make([]string, 0, len(raw))
		for _, c := range raw {
			s, _ := c.(string)
			out = append(out, s)
		}
		return out
	}
	equal := func(got, want []string) bool {
		if len(got) != len(want) {
			return false
		}
		for i := range got {
			if got[i] != want[i] {
				return false
			}
		}
		return true
	}

	if got := capsOf(t, "bernini-legacy"); !equal(got, []string{"视频编辑"}) {
		t.Fatalf("bernini-legacy caps = %v, want [视频编辑]", got)
	}
	// 已有正名时旧标签直接删,不能追加成 ["视频编辑","视频编辑","文生视频"]。
	if got := capsOf(t, "bernini-both"); !equal(got, []string{"视频编辑", "文生视频"}) {
		t.Fatalf("bernini-both caps = %v, want [视频编辑 文生视频]", got)
	}
	if got := capsOf(t, "bernini-spaced"); !equal(got, []string{"视频编辑"}) {
		t.Fatalf("bernini-spaced caps = %v, want [视频编辑] (旧标签带空格也要认)", got)
	}
	if got := capsOf(t, "wan2.2-t2v"); !equal(got, []string{"文生视频", "关键帧"}) {
		t.Fatalf("wan2.2-t2v caps = %v, want 原样不动", got)
	}
	// 核心保证:全库不再有模型带着这个词去命中新 tab。
	for _, m := range []string{"bernini-legacy", "bernini-both", "bernini-spaced", "wan2.2-t2v"} {
		for _, c := range capsOf(t, m) {
			if c == "参考生视频" {
				t.Fatalf("%s 仍带旧标签「参考生视频」,会错误命中 r2va tab", m)
			}
		}
	}

	// 幂等:标记落库后重跑不再改写 —— 本轮之后运营给真正的 H3 模型配的
	// 「参考生视频」是新语义,绝不能被这个迁移改成「视频编辑」。
	var opt Option
	if err := db.Where(Option{Key: "VideoModelConfig"}).First(&opt).Error; err != nil {
		t.Fatalf("read option: %v", err)
	}
	opt.Value = `{"models":{"minimax-h3-ref2va":{"capabilities":["参考生视频"]}}}`
	if err := db.Save(&opt).Error; err != nil {
		t.Fatalf("save option: %v", err)
	}
	if err := migrateVideoR2VACapabilityRename(); err != nil {
		t.Fatalf("migrate again: %v", err)
	}
	if got := capsOf(t, "minimax-h3-ref2va"); !equal(got, []string{"参考生视频"}) {
		t.Fatalf("重跑后 caps = %v, want [参考生视频] (marker 守卫,新语义不能被改名)", got)
	}
}
