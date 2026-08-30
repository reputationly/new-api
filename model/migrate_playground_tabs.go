package model

import (
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// 体验区配置「按 tab 拆分」的一次性迁移。
//
// 改造前四份 ModelConfig 只有模型级参数:models[name].{sizes,durations,maxInputMB,...}。
// 一个模型挂多个能力标签时,这一份参数被所有玩法共用 —— 给文生视频配的时长白名单会
// 连带限制图生视频,数字人配了尺寸但页面根本不显示尺寸选择器。改造后每个 tab 有自己
// 的一格 models[name].tabs[<tab key>],运营按玩法独立调整。
//
// 本迁移把存量的模型级值按该模型声明的 capabilities 扇出到对应 tab —— 只复制该 tab
// 声明用得到的字段(见下面的 fields),用不到的不落键,免得旧的无效值又被带进新界面。
// 模型级的平铺字段原样保留:它既是 tabs 未覆盖时的兜底,也是解析不出 tab 的直连请求
// (如语音四玩法共用的 task_type=tts)的取值来源。
//
// 顺带把手机端的显隐从代码搬进配置:改造前 web/mobile 里硬编码了一张 MOBILE_HIDDEN
// 表,升级后改由 PlaygroundTabConfig 的 mobile 字段驱动,这里把原表的语义种进配置,
// 保证升级前后手机端看到的 tab 不变。
//
// 幂等:一次性标记守卫,标记存在即跳过 —— 否则运营事后调整过的 tabs 会在下次重启被
// 模型级旧值覆盖回去。已有 tabs 子层的模型也单独跳过。无该配置或解析失败则只落标记,
// 绝不破坏原值。直接写 Option 行(migrateDB 早于 InitOptionMap,稍后加载即拿到新值)。

// playgroundTabSpec 是前端 PLAYGROUND_CATEGORIES 的服务端镜像,只取迁移用得到的三项。
// 新增玩法时与 web/classic/src/constants/playgroundAdmin.constants.js 同步。
type playgroundTabSpec struct {
	key        string   // tabs 子层的键
	capability string   // 老配置里 capabilities 数组的取值,用于反查
	aliases    []string // 重命名前的老能力标签,与前端 VIDEO_CAPABILITY_LEGACY_ALIASES 同步
	fields     []string // 该 tab 真正用得到的字段,只扇出这些

	// seedTaskType:该 tab 覆盖多个门面 task_type 时(「关键帧」= flf2v/i2v),迁移期按
	// 旧判据把玩法固化成显式声明。
	//
	// 不能留空让请求期再判:请求期的解析链已不等价于名字推断 —— 有了候选集之后名字只在
	// 「它的答案恰好落在候选集 ∩ 输入形态里」时才作裁决(见 taskTypeOfRequest)。名字毫无
	// 标识的存量关键帧模型,inferTaskType 兜底给 t2v,不在 {flf2v,i2v} 里,直连发两张图
	// 就会打 400,而改造前是判 i2v 的。
	//
	// 迁移这一刻旧判据是无歧义的全函数,固化下来既保证升级前后行为不变,也让这个此前
	// 隐形的判断出现在管理页上,运营看得见、改得动。
	seedTaskType func(modelName string) string
}

// 旧判据:名字含 flf2v 即首尾帧,否则按只吃首帧的 i2v(改造前 isFlf2vModel 的原文)。
func seedKeyframeTaskType(modelName string) string {
	if strings.Contains(strings.ToLower(modelName), "flf2v") {
		return "flf2v"
	}
	return "i2v"
}

// 按 option 键分组:「视频配音」的入口在语音页,模型却配在 VideoModelConfig
// (对应前端的 storeIn),故这里按存储位置而非页面分类归类。
var playgroundTabsByStoreKey = map[string][]playgroundTabSpec{
	"ImageModelSizeConfig": {
		{key: "text2image", capability: "文生图", fields: []string{"sizes"}},
		{key: "image2image", capability: "图生图", fields: []string{"sizes"}},
		// 图像编辑/局部重绘/扩图/高清放大暂无独立 tab,其 capabilities 原样保留。
	},
	"VideoModelConfig": {
		{key: "text2video", capability: "文生视频", fields: []string{"sizes", "durations", "aspectRatios"}},
		{key: "image2video", capability: "图生视频", fields: []string{"durations", "maxInputMB"}},
		{key: "flf2v", capability: "关键帧", aliases: []string{"首尾帧"}, fields: []string{"durations", "maxInputMB"}, seedTaskType: seedKeyframeTaskType},
		{key: "s2v", capability: "数字人", aliases: []string{"音频驱动"}, fields: []string{"maxInputMB", "maxAudioSec"}},
		{key: "vace", capability: "视频编辑", aliases: []string{"参考生视频"}, fields: []string{"durations", "maxInputMB"}},
		{key: "dub", capability: "视频配音", aliases: []string{"视频配乐"}, fields: []string{"maxInputMB"}},
	},
	"AudioModelConfig": {
		{key: "emotion", capability: "情感合成", fields: []string{"maxChars", "refAudioMaxMB"}},
		{key: "synthesis", capability: "语音合成", fields: []string{"maxChars", "refAudioMaxMB"}},
		{key: "dialogue", capability: "双人对话", fields: []string{"maxChars", "refAudioMaxMB"}},
		{key: "design", capability: "声音设计", fields: []string{"maxChars"}},
	},
	"MusicModelConfig": {
		{key: "t2m", capability: "文生音乐", fields: []string{"maxChars", "translation"}},
		{key: "cover", capability: "音乐改编", fields: []string{"maxChars", "refAudioMaxMB"}},
		{key: "repaint", capability: "音乐重绘", fields: []string{"maxChars", "refAudioMaxMB"}},
	},
}

// 改造前 web/mobile/src/hooks/useVisibleModes.js 里硬编码的手机端隐藏项:这些玩法
// 要么要多文件上传、要么控件在小屏排不开,只在网页端开放。搬进配置后运营可自行调整。
var playgroundMobileHiddenSeed = map[string][]string{
	"video": {"vace"},
	"music": {"cover", "repaint", "svs"},
}

func migratePlaygroundTabConfig() error {
	const markerKey = "playground_tab_config_migrated"
	var marker Option
	if err := DB.Where(Option{Key: markerKey}).First(&marker).Error; err == nil {
		return nil // 已迁移
	}
	// 配置改写 + 写标记放同一事务:崩溃不会留下"已改未标"(下次重跑覆盖运营改动)或
	// "已标未改"(旧值永不扇出)的半状态。
	return DB.Transaction(func(tx *gorm.DB) error {
		for storeKey, tabs := range playgroundTabsByStoreKey {
			if err := fanOutModelConfigTabs(tx, storeKey, tabs); err != nil {
				return err
			}
		}
		if err := seedPlaygroundMobileVisibility(tx); err != nil {
			return err
		}
		return tx.Create(&Option{Key: markerKey, Value: "true"}).Error
	})
}

// fanOutModelConfigTabs 把一份 ModelConfig 里每个模型的平铺参数,按其 capabilities
// 扇出到对应 tab 的一格。
func fanOutModelConfigTabs(tx *gorm.DB, storeKey string, tabs []playgroundTabSpec) error {
	var option Option
	if err := tx.Where(Option{Key: storeKey}).First(&option).Error; err != nil {
		return nil // 没配过这份配置,无可迁移
	}
	raw := strings.TrimSpace(option.Value)
	if raw == "" {
		return nil
	}
	var cfg map[string]any
	if common.UnmarshalJsonStr(raw, &cfg) != nil {
		return nil // 解析失败:宁可不迁移,也不破坏原值
	}
	models, ok := cfg["models"].(map[string]any)
	if !ok {
		return nil
	}
	// 老标签一并认:2026-07 那轮能力标签重命名(首尾帧→关键帧、音频驱动→数字人……)后,
	// 旧配置里存的还是老名字。不认的话这些模型扇不出 tabs,新管理页按 tabs 键列行,
	// 它们就整个从后台消失了(体验区侧靠前端的 LEGACY_ALIASES 还能看到,更难排查)。
	byCapability := make(map[string]playgroundTabSpec, len(tabs))
	for _, t := range tabs {
		byCapability[t.capability] = t
		for _, alias := range t.aliases {
			byCapability[alias] = t
		}
	}
	changed := false
	for name, v := range models {
		m, ok := v.(map[string]any)
		if !ok {
			continue // 旧形态(图像配置的值曾是尺寸数组):没有 capabilities,无从扇出
		}
		if _, exists := m["tabs"]; exists {
			continue // 已有 tab 级配置,不覆盖
		}
		caps, _ := m["capabilities"].([]any)
		out := make(map[string]any)
		for _, c := range caps {
			capability, _ := c.(string)
			spec, ok := byCapability[strings.TrimSpace(capability)]
			if !ok {
				continue // 该能力无对应 tab(如图像编辑),capabilities 里原样保留
			}
			// 空对象也要落:它是「这个模型挂在这个 tab 下」的声明本身,
			// 新版 capabilities 正是由 tabs 的键派生的。
			entry := make(map[string]any)
			for _, f := range spec.fields {
				if fv, exists := m[f]; exists {
					entry[f] = fv
				}
			}
			if spec.seedTaskType != nil {
				entry["taskType"] = spec.seedTaskType(name)
			}
			out[spec.key] = entry
		}
		if len(out) == 0 {
			continue
		}
		m["tabs"] = out
		changed = true
		common.SysLog(fmt.Sprintf("%s: fanned out %s to tabs %v", storeKey, name, tabKeys(out)))
	}
	if !changed {
		return nil
	}
	encoded, err := common.Marshal(cfg)
	if err != nil {
		return err
	}
	option.Value = string(encoded)
	return tx.Save(&option).Error
}

func tabKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// seedPlaygroundMobileVisibility 把原先硬编码的手机端隐藏表写进 PlaygroundTabConfig。
// 老配置里 tab 的值是布尔(true/false=网页端是否开启),新版是对象;这里只对需要隐藏的
// 那几个 tab 升格成对象并补 mobile:false,其余保持布尔不动 —— 前端 getTabDisplay 两种
// 形态都认,没必要整体重写。
func seedPlaygroundMobileVisibility(tx *gorm.DB) error {
	var option Option
	err := tx.Where(Option{Key: "PlaygroundTabConfig"}).First(&option).Error
	cfg := map[string]any{}
	if err == nil {
		if raw := strings.TrimSpace(option.Value); raw != "" {
			if common.UnmarshalJsonStr(raw, &cfg) != nil {
				return nil // 解析失败:不动原值
			}
		}
	}
	for category, hidden := range playgroundMobileHiddenSeed {
		catCfg, _ := cfg[category].(map[string]any)
		if catCfg == nil {
			catCfg = map[string]any{}
		}
		for _, tabKey := range hidden {
			// 保留原有的网页端开关(布尔或对象里的 enabled),只叠加 mobile:false。
			entry := map[string]any{"enabled": true, "mobile": false}
			switch old := catCfg[tabKey].(type) {
			case bool:
				entry["enabled"] = old
			case map[string]any:
				for k, v := range old {
					entry[k] = v
				}
				entry["mobile"] = false
			}
			catCfg[tabKey] = entry
		}
		cfg[category] = catCfg
	}
	encoded, merr := common.Marshal(cfg)
	if merr != nil {
		return merr
	}
	if err == nil {
		option.Value = string(encoded)
		return tx.Save(&option).Error
	}
	return tx.Create(&Option{Key: "PlaygroundTabConfig", Value: string(encoded)}).Error
}
