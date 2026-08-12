package common

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/constant"
)

// 图片/视频"模型尺寸/参数"配置(超管在系统设置里维护,存 OptionMap 的
// ImageModelSizeConfig / VideoModelConfig 两个 JSON 键)。
//
// **sizes 只驱动前端体验区的可选值,不做后端接口校验。** 早前版本拿它当接口白名单,
// 但配置值与请求值存在语义层级错配:运营填的是档位词或宽高比("720P"/"16:9"),
// API 客户端发的是引擎真正接受的精确像素("720x1280"),纯字符串比较永远对不上,
// 合法请求被一律拒成 400。引擎自身会拒绝它不支持的尺寸,网关这层重复拦截收益极小、
// 误伤成本极高,故已移除。durations(整数秒,单位统一、不存在错配)仍作为校验来源。
//
// JSON 结构(与前端 parseImageSizeConfig / parseVideoModelConfig 对应):
//
//	Image: { "default":[...], "models": { "name": {"sizes":[],"capabilities":[]} | [sizes...] } }
//	Video: { "default": {"sizes":[],"durations":[]},
//	         "models": { "name": {"sizes":[],"durations":[],"capabilities":[]} } }
//
// 注意:default 段仅供前端兜底,后端**不**用它做校验(未配置的模型不加限制)。
//
// ── tab 子层(2026-08)──────────────────────────────────────────────────
// 一个模型常同时挂多个体验区玩法(如既文生视频又图生视频),但两个玩法的参数并不通用:
// 文生视频有尺寸/宽高比,图生视频画幅跟随输入图、只需要上传上限。改造前所有参数都只按
// 模型名存一份,结果是给一个玩法配的时长白名单会连带卡住另一个玩法的请求。
// 现在参数按 tab 分格存放:
//
//	models[name].tabs[<tab key>] = { 该 tab 用得到的字段 }
//
// 本文件所有护栏的读取优先级统一为:
//
//	tabs[PlaygroundTabForTaskType(task_type)] → 模型级 → (前端才用的)default
//
// task_type 解析不出 tab(sr / v2m / tts 等,见 constant/playground_tab.go)时跳过第一级,
// 退回模型级 —— 即改造前的语义,不会比原来更严。老配置没有 tabs 键,同样自然落到模型级。

var digitsPrefixRe = regexp.MustCompile(`^\d+`)
var pOnlyRe = regexp.MustCompile(`^\d+p$`)

// normalizeSizeToken 与前端 normalizeVideoSize 对齐:小写、去空格、分隔符统一为 x,
// 纯 "\d+p" 形态转大写(如 720p -> 720P)。图片尺寸("1024x1024")同样适用。
func normalizeSizeToken(s string) string {
	v := strings.ToLower(strings.TrimSpace(s))
	v = strings.ReplaceAll(v, " ", "")
	for _, sep := range []string{"×", "✕", "╳", "*"} {
		v = strings.ReplaceAll(v, sep, "x")
	}
	if pOnlyRe.MatchString(v) {
		return strings.ToUpper(v)
	}
	return v
}

// toStringList 把 JSON 数组([]any of string/number)转成去空字符串列表。
func toStringList(v any) []string {
	arr, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		var s string
		switch t := item.(type) {
		case string:
			s = strings.TrimSpace(t)
		case float64:
			s = strconv.FormatFloat(t, 'f', -1, 64)
		}
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func modelEntryDurations(entry any) []string {
	if e, ok := entry.(map[string]any); ok {
		return toStringList(e["durations"])
	}
	return nil
}

// modelEntryTabDurations 取 models[name].tabs[tab].durations;无该 tab 或未配返回 nil,
// 由调用方降级到模型级。
func modelEntryTabDurations(entry any, tab string) []string {
	if tab == "" {
		return nil
	}
	e, ok := entry.(map[string]any)
	if !ok {
		return nil
	}
	tabs, ok := e["tabs"].(map[string]any)
	if !ok {
		return nil
	}
	tabEntry, ok := tabs[tab].(map[string]any)
	if !ok {
		return nil
	}
	return toStringList(tabEntry["durations"])
}

// modelsMap 从原始配置里取 models 子对象。
func modelsMap(raw string) map[string]any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var cfg map[string]any
	if err := UnmarshalJsonStr(raw, &cfg); err != nil {
		return nil
	}
	models, _ := cfg["models"].(map[string]any)
	return models
}

// ---- 视频参数配置 ----

func normalizeDurationSet(list []string) []string {
	out := make([]string, 0, len(list))
	for _, d := range list {
		if t := strings.TrimSpace(d); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// durationMatches 先按 trim 后字符串精确匹配(体验区下拉即取配置原值),再退化到
// 前导整数相等(兼容 "5" / "5s" / "5秒" 与请求的整数秒),减少误拒。
func durationMatches(allowed []string, candidates []string) bool {
	for _, want := range candidates {
		w := strings.TrimSpace(want)
		if w == "" {
			continue
		}
		wi := digitsPrefixRe.FindString(w)
		for _, a := range allowed {
			if a == w {
				return true
			}
			if wi != "" && digitsPrefixRe.FindString(a) == wi {
				return true
			}
		}
	}
	return false
}

// VideoDurationsAllowedForModel 返回该模型在本次玩法(task_type → tab)下配置的允许
// 时长集及是否已配置(非空)。tab 未配则降级到模型级。
// 只看 durations——sizes 不参与后端校验(见文件头说明)。
func VideoDurationsAllowedForModel(taskType string, candidates ...string) (durations []string, configured bool) {
	OptionMapRWMutex.RLock()
	raw := OptionMap["VideoModelConfig"]
	OptionMapRWMutex.RUnlock()
	models := modelsMap(raw)
	if models == nil {
		return nil, false
	}
	tab := constant.PlaygroundTabForTaskType(taskType)
	for _, name := range candidates {
		entry, ok := models[name]
		if !ok {
			continue
		}
		if d := normalizeDurationSet(modelEntryTabDurations(entry, tab)); len(d) > 0 {
			return d, true
		}
		if d := normalizeDurationSet(modelEntryDurations(entry)); len(d) > 0 {
			return d, true
		}
	}
	return nil, false
}

// ValidateVideoDurationForModel 校验视频时长:模型未配置 durations 则放行;配置了
// 则要求请求值落在允许集内。seconds 无值时跳过(无值可校验)。
// 尺寸不在此校验——运营配的档位词/宽高比与客户端发的精确像素无法字符串比较,
// 拦截只会误伤合法请求,交由引擎自行拒绝。
func ValidateVideoDurationForModel(taskType string, seconds int, secondsStr string, candidates ...string) error {
	allowedDurations, configured := VideoDurationsAllowedForModel(taskType, candidates...)
	if !configured {
		return nil
	}
	var cands []string
	if seconds > 0 {
		cands = append(cands, strconv.Itoa(seconds))
	}
	if strings.TrimSpace(secondsStr) != "" {
		cands = append(cands, secondsStr)
	}
	if len(cands) > 0 && !durationMatches(allowedDurations, cands) {
		return fmt.Errorf("模型 %s 不支持时长 %s,仅支持: %s",
			firstNonEmptyStr(candidates...), strings.Join(cands, "/"), strings.Join(allowedDurations, ", "))
	}
	return nil
}

// playgroundConfigKeys 是四份体验区模型配置的 option 键。查候选集要全扫:模型属于哪个
// 大类由它配在哪份里决定,调用方(relay 适配器)并不知道。
var playgroundConfigKeys = []string{
	"ImageModelSizeConfig",
	"VideoModelConfig",
	"AudioModelConfig",
	"MusicModelConfig",
}

// PlaygroundTaskTypeCandidates 返回该模型在体验区配置里「声明服务」的 task_type 候选集
// (已排序去重);模型未配进任何一份配置时返回 nil。
//
// 为什么需要它:玩法是**请求**的属性,不是**模型**的属性。一个部署同时服务文生/图生/
// 首尾帧是我们期望的部署形态(省显存),这时模型名里不可能编码出"这一次是哪种玩法"——
// 靠名字里的 token 猜(见 gpustackplus.inferTaskType)本质上是把 GPUStack 的部署命名
// 当成了跨系统 API 契约,而部署名是运营随手起的,没有任何地方能强制。
//
// 候选集把范围先框住(该模型只可能是这几种玩法),再由请求的输入形态收敛到一个。
//
// 取值规则,逐个 tab:
//   - tabs[x].taskType 显式声明了 → 只取它(用于收敛一个 tab 内部的多个 task_type,
//     如关键帧格声明本模型是 flf2v 还是 i2v);
//   - 否则 → 取该 tab 覆盖的全部 task_type(见 constant.PlaygroundTaskTypesForTab)。
func PlaygroundTaskTypeCandidates(candidates ...string) []string {
	set := map[string]struct{}{}
	OptionMapRWMutex.RLock()
	raws := make([]string, 0, len(playgroundConfigKeys))
	for _, key := range playgroundConfigKeys {
		raws = append(raws, OptionMap[key])
	}
	OptionMapRWMutex.RUnlock()

	for _, raw := range raws {
		models := modelsMap(raw)
		if models == nil {
			continue
		}
		for _, name := range candidates {
			entry, ok := models[strings.TrimSpace(name)]
			if !ok {
				continue
			}
			e, ok := entry.(map[string]any)
			if !ok {
				continue
			}
			tabs, ok := e["tabs"].(map[string]any)
			if !ok {
				continue
			}
			for tabKey, tabEntry := range tabs {
				if te, ok := tabEntry.(map[string]any); ok {
					if declared, ok := te["taskType"].(string); ok && strings.TrimSpace(declared) != "" {
						set[strings.ToLower(strings.TrimSpace(declared))] = struct{}{}
						continue
					}
				}
				for _, tt := range constant.PlaygroundTaskTypesForTab(tabKey) {
					set[tt] = struct{}{}
				}
			}
		}
	}
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for tt := range set {
		out = append(out, tt)
	}
	sort.Strings(out)
	return out
}

func firstNonEmptyStr(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return "model"
}

// AudioMaxCharsForModel 返回该模型合成文本的字数上限(0=不限制)及是否配置了 AudioModelConfig。
// 优先 tab 级,其次模型级,再次全局 default;都无返回 configured=false。
// 注:语音四个玩法共用 task_type=tts,解析不出 tab,实际总是走模型级(见 constant/playground_tab.go)。
func AudioMaxCharsForModel(taskType string, candidates ...string) (maxChars int, configured bool) {
	OptionMapRWMutex.RLock()
	raw := OptionMap["AudioModelConfig"]
	OptionMapRWMutex.RUnlock()
	if strings.TrimSpace(raw) == "" {
		return 0, false
	}
	var cfg struct {
		Default struct {
			MaxChars *int `json:"maxChars"`
		} `json:"default"`
		Models map[string]struct {
			MaxChars *int `json:"maxChars"`
			Tabs     map[string]struct {
				MaxChars *int `json:"maxChars"`
			} `json:"tabs"`
		} `json:"models"`
	}
	if err := UnmarshalJsonStr(raw, &cfg); err != nil {
		return 0, false
	}
	tab := constant.PlaygroundTabForTaskType(taskType)
	for _, name := range candidates {
		m, ok := cfg.Models[name]
		if !ok {
			continue
		}
		if t, ok := m.Tabs[tab]; tab != "" && ok && t.MaxChars != nil {
			return *t.MaxChars, true
		}
		if m.MaxChars != nil {
			return *m.MaxChars, true
		}
	}
	if cfg.Default.MaxChars != nil {
		return *cfg.Default.MaxChars, true
	}
	return 0, false
}

// ValidateAudioTextForModel 校验合成文本长度:未配置或上限=0 放行;否则要求字符数不超过上限。
// 按 rune 计数(与前端 text.length 对中文一致)。
func ValidateAudioTextForModel(taskType, text string, candidates ...string) error {
	maxChars, configured := AudioMaxCharsForModel(taskType, candidates...)
	if !configured || maxChars <= 0 {
		return nil
	}
	if n := len([]rune(text)); n > maxChars {
		return fmt.Errorf("模型 %s 合成文本超过字数上限 %d(当前 %d)",
			firstNonEmptyStr(candidates...), maxChars, n)
	}
	return nil
}

// VideoMaxInputBytesForModel 返回该视频模型输入文件大小上限(字节;0=不限)及是否已配置。
// 优先 tab 级,其次模型级,再次全局 default。适用于吃上传的能力(i2v/flf2v/s2v/sr/vace/v2a),
// 服务端物化时兜底(前端限制可被直连绕过)。
func VideoMaxInputBytesForModel(taskType string, candidates ...string) (maxBytes int64, configured bool) {
	OptionMapRWMutex.RLock()
	raw := OptionMap["VideoModelConfig"]
	OptionMapRWMutex.RUnlock()
	if strings.TrimSpace(raw) == "" {
		return 0, false
	}
	var cfg struct {
		Default struct {
			MaxInputMB *int `json:"maxInputMB"`
		} `json:"default"`
		Models map[string]struct {
			MaxInputMB *int `json:"maxInputMB"`
			Tabs       map[string]struct {
				MaxInputMB *int `json:"maxInputMB"`
			} `json:"tabs"`
		} `json:"models"`
	}
	if err := UnmarshalJsonStr(raw, &cfg); err != nil {
		return 0, false
	}
	tab := constant.PlaygroundTabForTaskType(taskType)
	for _, name := range candidates {
		m, ok := cfg.Models[name]
		if !ok {
			continue
		}
		if t, ok := m.Tabs[tab]; tab != "" && ok && t.MaxInputMB != nil {
			return int64(*t.MaxInputMB) * 1024 * 1024, true
		}
		if m.MaxInputMB != nil {
			return int64(*m.MaxInputMB) * 1024 * 1024, true
		}
	}
	if cfg.Default.MaxInputMB != nil {
		return int64(*cfg.Default.MaxInputMB) * 1024 * 1024, true
	}
	return 0, false
}

// VideoMaxAudioSecForModel 返回该视频模型驱动音频的时长上限(秒;0=不限)及是否已配置。
// 优先按模型,其次全局 default。与 maxInputMB 是两个正交的轴:1 MB 的 mp3 可能有 60 秒,
// 10 MB 的 wav 可能只有 10 秒,体积上限挡不住时长。
//
// 它只对数字人(s2v)有实际意义:该任务的输出时长 = min(驱动音频时长, video_duration,
// 参考视频时长),引擎不读 target_video_length。音频越长生成越久,过长会让引擎 OOM 或
// 长时间占卡,故需要这道闸门。
//
// 本值同时是两处的来源:物化层按音频真实时长拒绝超限输入(newVideoMaterializer),
// 以及请求体里下发给引擎的 video_duration 上限(buildRequest 的 s2v 分支)——一个配置
// 管两头,避免"放行了却被引擎截断"。
func VideoMaxAudioSecForModel(taskType string, candidates ...string) (maxSec float64, configured bool) {
	OptionMapRWMutex.RLock()
	raw := OptionMap["VideoModelConfig"]
	OptionMapRWMutex.RUnlock()
	if strings.TrimSpace(raw) == "" {
		return 0, false
	}
	var cfg struct {
		Default struct {
			MaxAudioSec *float64 `json:"maxAudioSec"`
		} `json:"default"`
		Models map[string]struct {
			MaxAudioSec *float64 `json:"maxAudioSec"`
			Tabs        map[string]struct {
				MaxAudioSec *float64 `json:"maxAudioSec"`
			} `json:"tabs"`
		} `json:"models"`
	}
	if err := UnmarshalJsonStr(raw, &cfg); err != nil {
		return 0, false
	}
	tab := constant.PlaygroundTabForTaskType(taskType)
	for _, name := range candidates {
		m, ok := cfg.Models[name]
		if !ok {
			continue
		}
		if t, ok := m.Tabs[tab]; tab != "" && ok && t.MaxAudioSec != nil {
			return *t.MaxAudioSec, true
		}
		if m.MaxAudioSec != nil {
			return *m.MaxAudioSec, true
		}
	}
	if cfg.Default.MaxAudioSec != nil {
		return *cfg.Default.MaxAudioSec, true
	}
	return 0, false
}

// 引擎族标识:自建视频链路上不同引擎对参数的读法差别很大(帧数约定、时长字段、
// 画布推导),必须能在 adaptor 里区分。已知值见下方常量。
const VideoEngineMinimaxH3 = "minimax-h3"

// VideoEngineFamilyForModel 返回该模型声明的引擎族(VideoModelConfig.models[name].engine),
// 未声明返回空串。
//
// **判据必须是配置声明,不能用模型名 substring** —— 这条是踩过的坑:前端拿到的是对外
// 模型名、后端拿到的是渠道重定向后的上游名,靠名字判断两边必然分叉(同 tabs[x].taskType
// 那次改造的教训,见 web/classic/src/constants/videoPlayground.constants.js 的注释)。
// 所以这里接受多个候选名(公开名 + 上游名),任一命中即可。
//
// 与 inferTaskType 里的 fl2va/ref2va 名字分支是两回事,别混淆:那条只服务「没配进体验区
// 的纯直连模型」的兜底推断,配了体验区的根本走不到。引擎族则是**每次请求都要知道**的,
// 不能靠兜底。
//
// 注意本函数是**模型级**,没有 tab 层:一个部署跑的是哪个引擎与用户选哪个玩法无关。
func VideoEngineFamilyForModel(candidates ...string) string {
	OptionMapRWMutex.RLock()
	raw := OptionMap["VideoModelConfig"]
	OptionMapRWMutex.RUnlock()
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	var cfg struct {
		Models map[string]struct {
			Engine string `json:"engine"`
		} `json:"models"`
	}
	if err := UnmarshalJsonStr(raw, &cfg); err != nil {
		return ""
	}
	for _, name := range candidates {
		m, ok := cfg.Models[name]
		if !ok {
			continue
		}
		if e := strings.ToLower(strings.TrimSpace(m.Engine)); e != "" {
			return e
		}
	}
	return ""
}

// VideoInferenceStepsForModel 返回该模型声明的采样步数
// (VideoModelConfig.models[name].defaultSteps),未声明或非正数返回 0。
//
// 存在的理由是蒸馏模型:同一个引擎族下,基座与蒸馏版的标定步数可以差一个数量级
// (H3 基座 20 步,Turbo8 蒸馏版 8 步)。而引擎族是**每次请求都要下发的**整形依据 ——
// 蒸馏模型必须照样声明 engine 才能拿到时长下发、17n+5 帧栅格、aspect_ratio 归一和那道
// 时长白名单加固,于是它也一并吃到引擎族的默认步数。步数若只能按引擎族给一个常量,
// 结局是二选一:要么声明 engine 让蒸馏版被强塞 20 步(速度优势全丢,还会跑到远超标定
// 步数导致画面劣化),要么不声明 engine 换取实例侧 deploy-config 的 8 步生效、代价是
// 上面那四项整形全部失效。所以步数必须与引擎族正交,单独按模型配。
//
// 与 engine 同为**模型级**,没有 tab 层:部署跑多少步与用户选哪个玩法无关。
func VideoInferenceStepsForModel(candidates ...string) int {
	OptionMapRWMutex.RLock()
	raw := OptionMap["VideoModelConfig"]
	OptionMapRWMutex.RUnlock()
	if strings.TrimSpace(raw) == "" {
		return 0
	}
	var cfg struct {
		Models map[string]struct {
			DefaultSteps *int `json:"defaultSteps"`
		} `json:"models"`
	}
	if err := UnmarshalJsonStr(raw, &cfg); err != nil {
		return 0
	}
	for _, name := range candidates {
		m, ok := cfg.Models[name]
		if !ok {
			continue
		}
		if m.DefaultSteps != nil && *m.DefaultSteps > 0 {
			return *m.DefaultSteps
		}
	}
	return 0
}

// AudioRefAudioMaxBytesForModel 返回该模型参考音大小上限(字节;0=不限制)及是否已配置。
// 优先 tab 级,其次模型级,再次全局 default。用于服务端物化参考音时兜底(前端上传限制可被直连绕过)。
// 注:语音四个玩法共用 task_type=tts,解析不出 tab,实际总是走模型级。
func AudioRefAudioMaxBytesForModel(taskType string, candidates ...string) (maxBytes int64, configured bool) {
	OptionMapRWMutex.RLock()
	raw := OptionMap["AudioModelConfig"]
	OptionMapRWMutex.RUnlock()
	if strings.TrimSpace(raw) == "" {
		return 0, false
	}
	var cfg struct {
		Default struct {
			RefAudioMaxMB *int `json:"refAudioMaxMB"`
		} `json:"default"`
		Models map[string]struct {
			RefAudioMaxMB *int `json:"refAudioMaxMB"`
			Tabs          map[string]struct {
				RefAudioMaxMB *int `json:"refAudioMaxMB"`
			} `json:"tabs"`
		} `json:"models"`
	}
	if err := UnmarshalJsonStr(raw, &cfg); err != nil {
		return 0, false
	}
	tab := constant.PlaygroundTabForTaskType(taskType)
	for _, name := range candidates {
		m, ok := cfg.Models[name]
		if !ok {
			continue
		}
		if t, ok := m.Tabs[tab]; tab != "" && ok && t.RefAudioMaxMB != nil {
			return int64(*t.RefAudioMaxMB) * 1024 * 1024, true
		}
		if m.RefAudioMaxMB != nil {
			return int64(*m.RefAudioMaxMB) * 1024 * 1024, true
		}
	}
	if cfg.Default.RefAudioMaxMB != nil {
		return int64(*cfg.Default.RefAudioMaxMB) * 1024 * 1024, true
	}
	return 0, false
}

// ---- 音乐参数配置(ACE-Step 等文生音乐引擎,存 OptionMap 的 MusicModelConfig 键) ----
//
// JSON 结构(与前端 parseMusicModelConfig 对应):
//
//	Music: { "default": {"maxChars":int,"refAudioMaxMB":int},
//	         "models": { "name": {"capabilities":[],"maxChars":int,"refAudioMaxMB":int} } }
//
// capabilities ∈ 文生音乐(t2m)/音乐改编(cover)/音乐重绘(repaint),供前端体验区按能力过滤模型 + tab。

// MusicMaxCharsForModel 返回该音乐模型歌词/描述文本的字数上限(0=不限制)及是否配置了 MusicModelConfig。
// 优先 tab 级,其次模型级,再次全局 default;都无返回 configured=false。
func MusicMaxCharsForModel(taskType string, candidates ...string) (maxChars int, configured bool) {
	OptionMapRWMutex.RLock()
	raw := OptionMap["MusicModelConfig"]
	OptionMapRWMutex.RUnlock()
	if strings.TrimSpace(raw) == "" {
		return 0, false
	}
	var cfg struct {
		Default struct {
			MaxChars *int `json:"maxChars"`
		} `json:"default"`
		Models map[string]struct {
			MaxChars *int `json:"maxChars"`
			Tabs     map[string]struct {
				MaxChars *int `json:"maxChars"`
			} `json:"tabs"`
		} `json:"models"`
	}
	if err := UnmarshalJsonStr(raw, &cfg); err != nil {
		return 0, false
	}
	tab := constant.PlaygroundTabForTaskType(taskType)
	for _, name := range candidates {
		m, ok := cfg.Models[name]
		if !ok {
			continue
		}
		if t, ok := m.Tabs[tab]; tab != "" && ok && t.MaxChars != nil {
			return *t.MaxChars, true
		}
		if m.MaxChars != nil {
			return *m.MaxChars, true
		}
	}
	if cfg.Default.MaxChars != nil {
		return *cfg.Default.MaxChars, true
	}
	return 0, false
}

// ValidateMusicTextForModel 校验歌词/描述文本长度:未配置或上限=0 放行;否则要求字符数不超过上限。
// 按 rune 计数(与前端 text.length 对中文一致)。
func ValidateMusicTextForModel(taskType, text string, candidates ...string) error {
	maxChars, configured := MusicMaxCharsForModel(taskType, candidates...)
	if !configured || maxChars <= 0 {
		return nil
	}
	if n := len([]rune(text)); n > maxChars {
		return fmt.Errorf("模型 %s 文本超过字数上限 %d(当前 %d)",
			firstNonEmptyStr(candidates...), maxChars, n)
	}
	return nil
}

// MusicRefAudioMaxBytesForModel 返回该音乐模型参考音/源音大小上限(字节;0=不限制)及是否已配置。
// 优先 tab 级,其次模型级,再次全局 default。用于 cover/repaint/svs 服务端物化时兜底
// (前端上传限制可被直连绕过)。
func MusicRefAudioMaxBytesForModel(taskType string, candidates ...string) (maxBytes int64, configured bool) {
	OptionMapRWMutex.RLock()
	raw := OptionMap["MusicModelConfig"]
	OptionMapRWMutex.RUnlock()
	if strings.TrimSpace(raw) == "" {
		return 0, false
	}
	var cfg struct {
		Default struct {
			RefAudioMaxMB *int `json:"refAudioMaxMB"`
		} `json:"default"`
		Models map[string]struct {
			RefAudioMaxMB *int `json:"refAudioMaxMB"`
			Tabs          map[string]struct {
				RefAudioMaxMB *int `json:"refAudioMaxMB"`
			} `json:"tabs"`
		} `json:"models"`
	}
	if err := UnmarshalJsonStr(raw, &cfg); err != nil {
		return 0, false
	}
	tab := constant.PlaygroundTabForTaskType(taskType)
	for _, name := range candidates {
		m, ok := cfg.Models[name]
		if !ok {
			continue
		}
		if t, ok := m.Tabs[tab]; tab != "" && ok && t.RefAudioMaxMB != nil {
			return int64(*t.RefAudioMaxMB) * 1024 * 1024, true
		}
		if m.RefAudioMaxMB != nil {
			return int64(*m.RefAudioMaxMB) * 1024 * 1024, true
		}
	}
	if cfg.Default.RefAudioMaxMB != nil {
		return int64(*cfg.Default.RefAudioMaxMB) * 1024 * 1024, true
	}
	return 0, false
}

// MusicVideoMaxBytesForModel 返回该音乐模型视频输入大小上限(字节;0=不限制)及是否已配置。
// 优先按模型,其次全局 default。用于 AudioX 视频→音乐(v2m/tv2m)服务端物化
// 时兜底——这些模型归「音乐」大类,其视频上限配在 MusicModelConfig.videoMaxMB(而非
// VideoModelConfig),直连 /pg/videos 也走这里,防绕过。
// 注:v2a(视频配乐)已改判视频大类,走 VideoMaxInputBytesForModel,不经此处。
func MusicVideoMaxBytesForModel(candidates ...string) (maxBytes int64, configured bool) {
	OptionMapRWMutex.RLock()
	raw := OptionMap["MusicModelConfig"]
	OptionMapRWMutex.RUnlock()
	if strings.TrimSpace(raw) == "" {
		return 0, false
	}
	var cfg struct {
		Default struct {
			VideoMaxMB *int `json:"videoMaxMB"`
		} `json:"default"`
		Models map[string]struct {
			VideoMaxMB *int `json:"videoMaxMB"`
		} `json:"models"`
	}
	if err := UnmarshalJsonStr(raw, &cfg); err != nil {
		return 0, false
	}
	for _, name := range candidates {
		if m, ok := cfg.Models[name]; ok && m.VideoMaxMB != nil {
			return int64(*m.VideoMaxMB) * 1024 * 1024, true
		}
	}
	if cfg.Default.VideoMaxMB != nil {
		return int64(*cfg.Default.VideoMaxMB) * 1024 * 1024, true
	}
	return 0, false
}

var wxhRe = regexp.MustCompile(`^(\d+)x(\d+)$`)

// DimsFromSize 解析 "WxH"(容忍 × ✕ * 等分隔符与空格)为像素宽高;无法解析
// (如 "720P"、空串)返回 ok=false。用于把用户选的绝对尺寸透传给引擎,而不是
// 只保留宽高比——否则引擎会按 aspect_ratio 的离散分辨率表出固定尺寸,忽略用户选择。
func DimsFromSize(size string) (w, h int, ok bool) {
	m := wxhRe.FindStringSubmatch(normalizeSizeToken(size))
	if m == nil {
		return 0, 0, false
	}
	w, _ = strconv.Atoi(m[1])
	h, _ = strconv.Atoi(m[2])
	if w <= 0 || h <= 0 {
		return 0, 0, false
	}
	return w, h, true
}

// AspectRatioFromSize 把 "WxH" 化简为 "W:H"(约分);无法解析(如 "720P")返回空串。
func AspectRatioFromSize(size string) string {
	m := wxhRe.FindStringSubmatch(normalizeSizeToken(size))
	if m == nil {
		return ""
	}
	w, _ := strconv.Atoi(m[1])
	h, _ := strconv.Atoi(m[2])
	if w <= 0 || h <= 0 {
		return ""
	}
	g := w
	for b := h; b != 0; {
		g, b = b, g%b
	}
	return fmt.Sprintf("%d:%d", w/g, h/g)
}

var aspectRatioRe = regexp.MustCompile(`^(\d+):(\d+)$`)

// NormalizeAspectRatio 去除比例串中的空格，得到规范 "a:b"（如 "16 : 9" → "16:9"）。
// 判断(IsAspectRatio)与向上游转发必须共用它，避免"通过判断的值"与"实际发出的值"不一致。
func NormalizeAspectRatio(s string) string {
	return strings.ReplaceAll(strings.TrimSpace(s), " ", "")
}

// IsAspectRatio 判断字符串是否为纯 "a:b" 宽高比格式(a、b 为正整数)。
// 用于区分"宽高比"与"精确像素(WxH)"两种尺寸输入。
func IsAspectRatio(s string) bool {
	m := aspectRatioRe.FindStringSubmatch(NormalizeAspectRatio(s))
	if m == nil {
		return false
	}
	return m[1] != "0" && m[2] != "0"
}
