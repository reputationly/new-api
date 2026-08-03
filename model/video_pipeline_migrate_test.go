package model

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
)

// TestMigrateVideoModelConfigPipelineFlag 验证「自建流水线」标记的一次性迁移：
// 判据是渠道归属（模型挂在 type=GPUStackPlus 的渠道上）而非尺寸档位——自建的
// i2v/数字人等模型根本不配 sizes，按尺寸判会漏标、升级后丢掉插帧与配音能力。
// 同时验证第三方模型不被误标、已有显式取值不覆盖、标记落库后重跑不再改写。
func TestMigrateVideoModelConfigPipelineFlag(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:video_pipeline_migrate?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&Option{}, &Channel{}, &Ability{}); err != nil {
		t.Fatalf("AutoMigrate: %v", err)
	}
	// 迁移函数走包级 DB，临时指过去，跑完还原。
	prev := DB
	DB = db
	defer func() { DB = prev }()

	// 两个渠道：1=自建 gpustackplus，2=第三方。
	selfHostedType := constant.ChannelTypeGPUStackPlus
	thirdPartyType := constant.ChannelTypeOpenAI
	for _, ch := range []Channel{
		{Id: 1, Type: selfHostedType, Name: "self-hosted"},
		{Id: 2, Type: thirdPartyType, Name: "third-party"},
	} {
		if err := db.Create(&ch).Error; err != nil {
			t.Fatalf("seed channel: %v", err)
		}
	}
	// wan2.2-i2v 故意标成禁用：渠道临时禁用不该让它漏标。
	for _, ab := range []Ability{
		{Group: "default", Model: "wan2.2-t2v", ChannelId: 1, Enabled: true},
		{Group: "default", Model: "wan2.2-i2v", ChannelId: 1, Enabled: false},
		{Group: "default", Model: "sora-2", ChannelId: 2, Enabled: true},
	} {
		if err := db.Create(&ab).Error; err != nil {
			t.Fatalf("seed ability: %v", err)
		}
	}

	// wan2.2-i2v 没有 sizes（输出跟随输入），sora-2 反而配了 480+1080：
	// 正是旧的尺寸启发式两头判错的样本。
	const raw = `{"default":{"sizes":["720P"]},"models":{` +
		`"wan2.2-t2v":{"sizes":["854x480","1080P"],"capabilities":["文生视频"]},` +
		`"wan2.2-i2v":{"capabilities":["图生视频"],"maxInputMB":50},` +
		`"sora-2":{"sizes":["854x480","1080P"],"capabilities":["文生视频"]},` +
		`"explicit-off":{"sizes":["854x480","1080P"],"pipeline":false}}}`
	if err := db.Create(&Option{Key: "VideoModelConfig", Value: raw}).Error; err != nil {
		t.Fatalf("seed option: %v", err)
	}

	if err := migrateVideoModelConfigPipelineFlag(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	pipelineOf := func(t *testing.T, model string) interface{} {
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
		return m["pipeline"]
	}

	// 挂在自建渠道上 → 打标
	if got := pipelineOf(t, "wan2.2-t2v"); got != true {
		t.Fatalf("wan2.2-t2v pipeline = %v, want true", got)
	}
	// 自建但没有 sizes（i2v 输出跟随输入）→ 仍要打标，否则升级后丢掉插帧/配音
	if got := pipelineOf(t, "wan2.2-i2v"); got != true {
		t.Fatalf("wan2.2-i2v pipeline = %v, want true (no sizes, self-hosted)", got)
	}
	// 第三方模型即便配了 480+1080 也不打标 → 1080P 原样透传
	if got := pipelineOf(t, "sora-2"); got != nil {
		t.Fatalf("sora-2 pipeline = %v, want nil (third-party)", got)
	}
	// 运营已显式关掉的不覆盖（该模型名没有任何 ability，本就不该被标）
	if got := pipelineOf(t, "explicit-off"); got != false {
		t.Fatalf("explicit-off pipeline = %v, want false", got)
	}

	// 幂等：运营事后取消勾选，重启不该被改回来
	var opt Option
	if err := db.Where(Option{Key: "VideoModelConfig"}).First(&opt).Error; err != nil {
		t.Fatalf("read option: %v", err)
	}
	opt.Value = `{"models":{"wan2.2-t2v":{"sizes":["854x480","1080P"],"pipeline":false}}}`
	if err := db.Save(&opt).Error; err != nil {
		t.Fatalf("save option: %v", err)
	}
	if err := migrateVideoModelConfigPipelineFlag(); err != nil {
		t.Fatalf("migrate again: %v", err)
	}
	if got := pipelineOf(t, "wan2.2-t2v"); got != false {
		t.Fatalf("wan2.2-t2v pipeline after rerun = %v, want false (marker guards rerun)", got)
	}
}
