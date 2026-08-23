package gpustackplus

import (
	"encoding/json" // 仅取 json.Number 类型;marshal/unmarshal 一律走 common(见 CLAUDE.md Rule 1)
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// MiniMax H3 专属的请求整形。
//
// 单独成文件的理由:H3 与本渠道原有的 LightX2V 系(wan / seedvr2 / infinitetalk)在三处
// 根本约定上不同,混在 adaptor.go 里会让两套约定互相污染 ——
//
//  1. **帧数约定**:wan 是 4n+1 @16fps(target_video_length),H3 是 17n+5 @24fps 且时长走
//     extra_params.duration(float 秒);
//  2. **时长语义**:InfiniteTalk 的 video_duration 是"输出时长上限",H3 的音频是音色样本、
//     与输出时长无关;
//  3. **画布推导**:wan 用 target_shape,H3 要 width/height + 具名 aspect_ratio。
//
// 以下常量与算法全部核实自 vllm-omni 的
// vllm_omni/diffusion/models/minimax_h3/pipeline_minimax_h3.py,非文档推测。
const (
	// _resolve_output_canvas 的面积上限(pipeline:105 MINIMAX_H3_OUTPUT_MAX_PIXELS)。
	h3MaxOutputPixels = 768 * 1344 // 1_032_192

	// 画布两轴都必须对齐到 32(pipeline:889-890 的 int(x)//32*32)。
	h3CanvasMultiple = 32

	// 基座的生产档步数,**仅在模型没配 defaultSteps 时兜底**。引擎自身兜底是 50
	// (pipeline 的 `num_inference_steps or 50`),即 2.5 倍耗时(480p:70s → 117-165s)。
	//
	// 注:vllm-omni #57 之后 deploy-configs/minimax_h3_a100_40g.yaml 的
	// default_sampling_params 也设了 20,但**我们仍要显式下发** —— 部署档 §3 那条裸 CLI
	// 启动命令不带 --deploy-config(引擎会回落 50 步),不该把"跑多快"押在对方的启动参数上。
	//
	// ⚠️ 别再把它当成"H3 就该跑 20 步"的常量往回改成硬编码:蒸馏版(Turbo8)标定 8 步,
	// 按引擎族一刀切就等于逼蒸馏模型在"丢速度"和"丢请求整形"之间二选一。
	// 详见 common.VideoInferenceStepsForModel 的注释。
	h3DefaultInferenceSteps = 20

	// 时长硬区间(pipeline MINIMAX_H3_MIN/MAX_OUTPUT_SECONDS)。超界引擎 400。
	h3MinDurationSec = 4.0
	h3MaxDurationSec = 15.0

	// t2va 缺省宽高比。取 16:9 不是随手挑的:引擎的 Ref2VA 分支缺省就是它
	// (_resolve_minimax_h3_aspect_ratio 的注释 "Ref2VA defaults to 16:9"),
	// 两处取同一个值,同一个模型才不会因为玩法不同出不同画幅。
	h3DefaultAspectRatio = "16:9"
)

// h3NamedAspectRatios 是 H3 认的六个具名比例(pipeline:109-116
// MINIMAX_H3_SUPPORTED_ASPECT_RATIOS)。
//
// ⚠️ t2va **只**接受这六个具名值:不在表内的字符串引擎会退到 float() 解析,失败即 400。
// 所以**绝不能用像素尺寸反推比例**下发 —— common.AspectRatioFromSize("832x480") 走 gcd
// 约分得到的是 "26:15",不是 "16:9"(832×480 真实比例 1.733,而 16:9 = 1.778;对齐到 32
// 的过程本身就改变了比例),下发过去必被拒。
var h3NamedAspectRatios = map[string]float64{
	"21:9": 21.0 / 9.0,
	"16:9": 16.0 / 9.0,
	"4:3":  4.0 / 3.0,
	"1:1":  1.0,
	"3:4":  3.0 / 4.0,
	"9:16": 9.0 / 16.0,
}

// h3IsNamedAspectRatio 判断是否为 H3 认的具名比例。
func h3IsNamedAspectRatio(s string) bool {
	_, ok := h3NamedAspectRatios[common.NormalizeAspectRatio(strings.ToLower(strings.TrimSpace(s)))]
	return ok
}

// h3ShapeRatioTolerance 是 target_shape 反推具名比例时允许的相对误差。
//
// 3% 这个数不是拍的:体验区的 target_shape 表只对 5 个比例给了手调固定值,21:9 走的是
// 「按 ~720p 面积等比算再对齐到 16 的倍数」那条路(aspectRatioToShape),对齐后得
// [624,1472],1472/624=2.359 对真值 21/9=2.333 偏 1.1% —— 容差小于它就把 21:9 漏掉了。
// 上限则由具名表里最挨近的两档决定:1:1(1.0) 与 3:4(0.75) 相距 25%,3% 离误判还远。
const h3ShapeRatioTolerance = 0.03

// h3AspectRatioFromTargetShape 从 wan 的 target_shape:[height,width] 反推 H3 的具名比例,
// 推不出(解析失败 / 偏离所有具名值超过容差)返回空串。
//
// 只反推**比例**,不反推画布 —— 那些像素值是 wan 的 720p 级固定表,既非 32 的倍数也不是
// 用户选的档位,当画布用必出错档(见 h3NormalizeAspectRatio 的注释)。比例则是无损的:
// 表里的值本来就是按比例算出来的,除回去就还原。
func h3AspectRatioFromTargetShape(v any) string {
	shape, ok := v.([]any)
	if !ok || len(shape) < 2 {
		return ""
	}
	h, okH := h3ToFloat(shape[0])
	w, okW := h3ToFloat(shape[1])
	if !okH || !okW || h <= 0 || w <= 0 {
		return ""
	}
	got := w / h
	best, bestErr := "", math.Inf(1)
	for name, want := range h3NamedAspectRatios {
		if e := math.Abs(got-want) / want; e < bestErr {
			best, bestErr = name, e
		}
	}
	if bestErr > h3ShapeRatioTolerance {
		return ""
	}
	return best
}

// h3ToFloat 把 JSON 反序列化出来的数字取成 float64。metadata 走 map[string]any,
// 数字落地成 float64;直连调用方经其他路径进来也可能是整型,一并收下。
func h3ToFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

// h3ShortEdgeFromSizeToken 从分辨率档位词取短边像素:"480P"/"768p" → 480/768;
// 取不出返回 0。
//
// 体验区把 H3 的 sizes 配成**档位词**而非像素串("480P" 而不是 "832x480"),这不是风格
// 选择而是必需:adaptor 转发顶层 size 时会用 AspectRatioFromSize 反推一个 aspect_ratio
// **覆盖掉**用户选的具名比例(见 BuildRequestBody 里那段),而档位词匹配不到 WxH 正则、
// 返回空串,于是不会覆盖。
func h3ShortEdgeFromSizeToken(size string) int {
	v := strings.ToLower(strings.TrimSpace(size))
	if v == "" || !strings.HasSuffix(v, "p") {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSuffix(v, "p"))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// h3AlignMultiple 复刻引擎的 _align_multiple(pipeline:273-274):
//
//	max(multiple, int(round(value/multiple)) * multiple)
//
// ⚠️ 是 **round 不是 floor**。这决定了结果:480 短边 16:9 得 864 而非 832
// (round(853.33/32)=27 → 864;floor 才是 832)。别"顺手"改成截断。
func h3AlignMultiple(value float64, multiple int) int {
	m := float64(multiple)
	aligned := int(math.Round(value/m)) * multiple
	if aligned < multiple {
		return multiple
	}
	return aligned
}

// h3Canvas 按 (短边, 宽高比) 推出画布,返回 (width, height)。
//
// 忠实复刻引擎的 _resolve_output_canvas(pipeline:533-553),包括**面积钳位**那一步 ——
// 漏掉它 768P/16:9 会算出 1376×768 而不是实测的 1344×768(768×16/9 的面积
// 1,048,576 已超过上限 1,032,192,先等比缩到上限再对齐才得 1344)。
//
// 之所以要在网关侧重算一遍而不是让引擎自己算:引擎只在 width/height **都缺省**时才走
// _resolve_output_canvas,而那条路里 short_edge 被硬校验成必须等于 768
// (`if short_edge != MINIMAX_H3_OUTPUT_SHORT_EDGE: raise`)。也就是说不下发尺寸就只能
// 出 768p,想要 480p 生产档必须自己算好传过去。
//
// 与实测档的对照(短边 768):
//
//	16:9 → 1344×768   4:3 → 1024×768   1:1 → 768×768   9:16 → 768×1344
//
// 短边 480 的 16:9 得 864×480,而生产部署档里跑基准用的是运维手传的 832×480 —— 两者差
// 3.8% 像素,且 864 反而更接近真 16:9(1.800 vs 1.733,真值 1.778)。这里按引擎公式走,
// 好处是万一将来不下发尺寸,行为与引擎自算完全一致。
func h3Canvas(shortEdge int, ratio float64) (width, height int) {
	if shortEdge <= 0 || !(ratio > 0) || math.IsInf(ratio, 0) {
		return 0, 0
	}
	var w, h float64
	if ratio >= 1.0 {
		w, h = float64(shortEdge)*ratio, float64(shortEdge)
	} else {
		w, h = float64(shortEdge), float64(shortEdge)/ratio
	}
	if area := w * h; area > h3MaxOutputPixels {
		scale := math.Sqrt(h3MaxOutputPixels / area)
		w *= scale
		h *= scale
	}
	return h3AlignMultiple(w, h3CanvasMultiple), h3AlignMultiple(h, h3CanvasMultiple)
}

// h3EnsureExtraParams 取出 body["extra_params"] 这个嵌套对象,不存在则建。
//
// H3 的任务选择与时长都走**嵌套的** extra_params,顶层同名字段会被引擎的
// VideoGenerationRequest(Pydantic,没有 extra="forbid")静默丢弃 —— 不报错、不生效,
// 是最难查的一类问题。同 foldEmotionParamsIntoExtra 的处境。
func h3EnsureExtraParams(body map[string]any) map[string]any {
	if extra, ok := body["extra_params"].(map[string]any); ok && extra != nil {
		return extra
	}
	extra := make(map[string]any)
	body["extra_params"] = extra
	return extra
}

// applyMiniMaxH3Request 把 H3 需要的形状整形到 body 上。仅在模型声明了
// engine=minimax-h3 时调用(见 common.VideoEngineFamilyForModel —— 判据是配置声明,
// 不是模型名)。
//
// durationSec 为 0 表示本次请求没给时长,此时不写 duration,由引擎按任务默认帧数决定
// (t2va/fl2va 是 209 帧 ≈ 8.708 s)。
//
// steps 为该模型配置的采样步数(common.VideoInferenceStepsForModel),0 表示没配、
// 回落到基座档 h3DefaultInferenceSteps。
//
// **调用方已有的显式取值一律优先**:metadata 是开放透传的(API 用户可直接下发
// extra_params / num_inference_steps 等引擎旋钮),这里只补默认,不覆盖用户意图。
func applyMiniMaxH3Request(body map[string]any, taskType string, durationSec int, durationLocked bool, steps int) {
	extra := h3EnsureExtraParams(body)

	// ── 时长白名单加固 ─────────────────────────────────────────────────────
	// 运营配了时长白名单时,必须连**嵌套**的时长键一起剥掉。
	//
	// 这不是可选的加固,是补一个真实的绕过口:上游那道 durationOverrideKeys 只剥
	// **顶层** metadata 键(target_video_length / video_length / num_frames / frames),
	// 而 H3 的时长走 extra_params 嵌套对象,完全不在它的射程内。于是调用方只要顶层
	// 老实发一个白名单内的 duration=5、同时塞
	// metadata.extra_params.duration=15,就能通过校验并让引擎按 15 秒出片 ——
	// 白名单形同虚设,GPU 时间翻三倍。
	//
	// 三个别名都要剥,顺序按引擎的优先级链(见上游契约 §4.2):
	//   extra_params.target.duration_seconds > extra_params.duration_seconds
	//     > extra_params.duration > num_frames
	// 只剥 duration 会被 duration_seconds 绕过,只剥这两个会被 target 嵌套绕过。
	if durationLocked {
		delete(extra, "duration")
		delete(extra, "duration_seconds")
		if target, ok := extra["target"].(map[string]any); ok {
			delete(target, "duration_seconds")
			// target 里可能还有 aspect_ratio / short_edge 等合法键,只剥时长那个,
			// 剥空了才顺手删掉这个壳。
			if len(target) == 0 {
				delete(extra, "target")
			}
		}
	}

	// ── 时长 ───────────────────────────────────────────────────────────────
	// H3 走 extra_params.duration(float 秒),**不是** target_video_length。
	// 后者是 wan 的 4n+1 @16fps 约定,H3 是 24fps 且帧数向上吸附到 17n+5 栅格,
	// 照搬会算出一个引擎根本不读的数(反而让人以为时长可控)。
	if durationSec > 0 {
		if _, ok := extra["duration"]; !ok {
			if _, ok := extra["duration_seconds"]; !ok {
				extra["duration"] = float64(durationSec)
			}
		}
	}
	// wan 专属字段:即便上面某处已经写了,对 H3 也要清掉 —— 留着不会报错,只会在
	// 排查时误导(引擎侧 H3 分支从不读它)。
	delete(body, "target_video_length")
	// InfiniteTalk 专属:输出时长 = min(驱动音频时长, video_duration, 参考视频时长)。
	// H3 的音频是**音色样本**,长度与输出时长无关,照搬会把输出错误地卡在音频配置上。
	delete(body, "video_duration")

	// ── 步数 ───────────────────────────────────────────────────────────────
	// 按模型取,没配才回落基座档。蒸馏版(Turbo8,标定 8 步)与基座共用引擎族,
	// 但步数必须各按各的,见 h3DefaultInferenceSteps 处的注释。
	if _, ok := body["num_inference_steps"]; !ok {
		if steps <= 0 {
			steps = h3DefaultInferenceSteps
		}
		body["num_inference_steps"] = steps
	}

	// ── 宽高比字段归一 ─────────────────────────────────────────────────────
	// 对所有玩法都做:体验区发的是 ratio / target_shape,引擎只认 aspect_ratio。
	// 关键帧虽然会被引擎忽略 aspect_ratio,但把 wan 的 target_shape 一路带到 H3
	// 同样没有意义,清掉更干净。
	h3NormalizeAspectRatio(body)

	// ── 缺省宽高比 ─────────────────────────────────────────────────────────
	// 只给 t2v 补。这不是"顺手加个默认值",是补一个真实的线上失败:引擎对 t2va 的
	// aspect_ratio 是硬校验(_resolve_minimax_h3_aspect_ratio:task=="t2va" 且值为空
	// 直接 `OmniClientError: t2va requires an explicit aspect_ratio`),而同一个引擎里
	// fl2va 永远跟随首图、r2va 缺省 16:9 —— 六种玩法里只有 t2va 会因为"没传"整条挂掉,
	// 且报回调用方的是一句引擎内部术语,自助不了。2026-08-13 现网 12 条请求挂了 5 条,
	// 全是直连侧没带比例的 t2va。
	//
	// **必须在 h3ApplyCanvas 之前**:那个函数取不到具名比例会走"清掉档位词、原样交给
	// 引擎"的降级路径,结果是用户选了 480P 却按 short_edge=768 自推出 768p,GPU 时间翻倍。
	//
	// 显式传值一律不动,包括不在具名表内的值 —— 那种情况调用方是"传错"而不是"没传",
	// 应该让引擎把 400 报回去,而不是被我们悄悄改成 16:9。
	//
	// ⚠️ 别"顺手"加上「已有 width/height 就不补」的判断:引擎的 _resolve_shape 是**先**
	// 无条件解析比例(必填校验在这一步)、**再**判断 `if height is None or width is None`
	// 才用它推画布。也就是说带了像素画布但不带比例的 t2va 同样是 400(实测 43 ms 返回
	// `t2va requires an explicit aspect_ratio`),而直连调用方按像素下发画布恰恰是常态 ——
	// 加了那个判断就是把要修的人群原样漏掉。
	// 反过来,画布显式时这个比例对出片没有任何影响:它唯二的用途是 [0.25,4] 区间校验
	// (16:9=1.778 恒过)和缺画布时的 _resolve_output_canvas。实测 832x480 配矛盾的 9:16
	// 仍出 832x480,比例被完全忽略。
	if taskType == "t2v" {
		if ar, _ := body["aspect_ratio"].(string); strings.TrimSpace(ar) == "" {
			body["aspect_ratio"] = h3DefaultAspectRatio
		}
	}

	// ── 画布 ───────────────────────────────────────────────────────────────
	// 只有 t2va 需要我们算:它的 aspect_ratio 是强制必填的具名值,且画幅完全由参数决定。
	//
	// 关键帧(i2v/l2va/flf2v)不在这里算 —— FL2VA 的画幅**永远跟随第一张图**
	// (引擎 _resolve_minimax_h3_aspect_ratio 对 fl2va 直接返回 image.width/image.height,
	// 传来的 aspect_ratio 被静默忽略),而网关这层拿到的图是 URL/base64,不解码就不知道
	// 宽高比。由前端按已加载的图算好经 metadata.width/height 透传(前端本来就要读图的
	// 像素尺寸做 256/5760/[0.4,2.5] 的前置校验);直连调用方不传则由引擎自算,出 768p。
	//
	// r2va(参考生视频)也算:Ref2VA **接受具名 aspect_ratio**(不传默认 16:9),
	// 与关键帧不同 —— 关键帧的画幅永远跟随第一张图,传了比例也被静默忽略,所以那边
	// 算不出、也不该算。不给 r2va 算画布的话,引擎按 short_edge=768 自推,每条多花
	// 一倍时间(实测 768p 约 190s)。
	if taskType == "t2v" || taskType == "r2va" {
		h3ApplyCanvas(body)
	} else {
		// 关键帧不推画布,但档位词仍不能漏给引擎(SizeStr 只认 "WxH")。
		h3DropResolutionToken(body)
	}
}

// h3NormalizeAspectRatio 把体验区实际发出的宽高比字段归一到引擎认的 aspect_ratio。
//
// 体验区**不发 aspect_ratio**,它按 pipeline 标记二选一(useVideoGeneration.js:1290-1306):
//
//	usePipeline=true  → metadata.target_shape = [h, w]   (wan 的 t2v runner 只认这个)
//	usePipeline=false → metadata.ratio        = "16:9"   (Ark/Seedance 等第三方的原生形态)
//
// 而 H3 两个都不认,要的是具名 aspect_ratio。归一放在网关侧而不是只改前端,理由有二:
// 引擎契约本来就归这一层管;且直连调用方发 ratio 的同样能被救,不必等前端一起上线。
//
// target_shape 是 wan 的 720p 级固定值表([720,1280] 等),对 H3 既非 32 的倍数也不是
// 我们要的档位,**当画布用**必出错档,所以这个键本身照旧丢弃 —— 但要先把比例从里面捞
// 出来。
//
// ⚠️ 别再按「H3 恒 pipeline=false,收不到 target_shape」写这里:那个前提是错的,且错得
// 完全静默。model/main.go 的 video_pipeline_flag_migrated 迁移会把**所有挂在自建
// gpustackplus 渠道上**的视频模型自动标成 pipeline:true,而 H3 正是跑在 gpustackplus
// 上的,于是它必然被标上;前端见 pipeline 就只发 target_shape、不发 ratio。原来这里
// 直接删掉它,aspect_ratio 就一路缺到 t2v 的缺省分支,补成 16:9 —— 用户在体验区选任何
// 比例都出 16:9,不报错、不告警,只有对着成片量宽高才看得出来。
func h3NormalizeAspectRatio(body map[string]any) {
	// wan 专属,引擎不读,留着只会在排查时误导。取值后即删。
	shape := body["target_shape"]
	delete(body, "target_shape")
	if _, ok := body["aspect_ratio"]; ok {
		delete(body, "ratio") // 已有权威值:别名清掉,免得两个键打架
		return
	}
	if r, ok := body["ratio"].(string); ok && strings.TrimSpace(r) != "" {
		body["aspect_ratio"] = common.NormalizeAspectRatio(r)
		delete(body, "ratio")
		return
	}
	delete(body, "ratio")
	// 两个别名都没有,才轮到 target_shape 兜底。优先级排最后是有意的:ratio 是调用方
	// 直接表达的比例,target_shape 是反推来的,前者更权威。
	if ar := h3AspectRatioFromTargetShape(shape); ar != "" {
		body["aspect_ratio"] = ar
	}
}

// h3ApplyCanvas 按 body 里的 size 档位词 + 具名 aspect_ratio 推出 width/height 并写回。
// 调用方已显式给了 width/height 时不动。
func h3ApplyCanvas(body map[string]any) {
	_, hasW := body["width"]
	_, hasH := body["height"]
	if hasW || hasH {
		// 调用方自己定了画布。档位词对引擎的 SizeStr 是非法值,仍要清掉。
		h3DropResolutionToken(body)
		return
	}
	size, _ := body["size"].(string)
	shortEdge := h3ShortEdgeFromSizeToken(size)
	if shortEdge <= 0 {
		return // 不是档位词(像素串或没配):原样交给引擎
	}
	ar, _ := body["aspect_ratio"].(string)
	ratio, ok := h3NamedAspectRatios[common.NormalizeAspectRatio(strings.ToLower(strings.TrimSpace(ar)))]
	if !ok {
		// 比例不是具名值,推不出画布。但档位词**必须**清掉:引擎的 SizeStr 只认 "WxH",
		// 把 "480P" 丢过去是解析错误。清掉后引擎按 short_edge=768 自算 —— 出 768p
		// 而不是想要的 480p,是可接受的降级;留着则是硬报错。
		h3DropResolutionToken(body)
		return
	}
	w, h := h3Canvas(shortEdge, ratio)
	if w <= 0 || h <= 0 {
		h3DropResolutionToken(body)
		return
	}
	body["width"] = w
	body["height"] = h
	h3DropResolutionToken(body)
}

// h3DropResolutionToken 清掉 "480P" 这类档位词形态的 size。
// 像素串("832x480")是引擎认的合法 SizeStr,保留。
func h3DropResolutionToken(body map[string]any) {
	if size, ok := body["size"].(string); ok && h3ShortEdgeFromSizeToken(size) > 0 {
		delete(body, "size")
	}
}
