package gpustackplus

import (
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

	// 生产档步数。引擎自身兜底是 50(pipeline 的 `num_inference_steps or 50`),
	// 即 2.5 倍耗时(480p:70s → 117-165s)。
	//
	// 注:vllm-omni #57 之后 deploy-configs/minimax_h3_a100_40g.yaml 的
	// default_sampling_params 也设了 20,但**我们仍要显式下发** —— 部署档 §3 那条裸 CLI
	// 启动命令不带 --deploy-config(引擎会回落 50 步),不该把"跑多快"押在对方的启动参数上。
	h3DefaultInferenceSteps = 20

	// 时长硬区间(pipeline MINIMAX_H3_MIN/MAX_OUTPUT_SECONDS)。超界引擎 400。
	h3MinDurationSec = 4.0
	h3MaxDurationSec = 15.0
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
// **调用方已有的显式取值一律优先**:metadata 是开放透传的(API 用户可直接下发
// extra_params / num_inference_steps 等引擎旋钮),这里只补默认,不覆盖用户意图。
func applyMiniMaxH3Request(body map[string]any, taskType string, durationSec int, durationLocked bool) {
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
	if _, ok := body["num_inference_steps"]; !ok {
		body["num_inference_steps"] = h3DefaultInferenceSteps
	}

	// ── 宽高比字段归一 ─────────────────────────────────────────────────────
	// 对所有玩法都做:体验区发的是 ratio / target_shape,引擎只认 aspect_ratio。
	// 关键帧虽然会被引擎忽略 aspect_ratio,但把 wan 的 target_shape 一路带到 H3
	// 同样没有意义,清掉更干净。
	h3NormalizeAspectRatio(body)

	// ── 画布 ───────────────────────────────────────────────────────────────
	// 只有 t2va 需要我们算:它的 aspect_ratio 是强制必填的具名值,且画幅完全由参数决定。
	//
	// 关键帧(i2v/l2va/flf2v)不在这里算 —— FL2VA 的画幅**永远跟随第一张图**
	// (引擎 _resolve_minimax_h3_aspect_ratio 对 fl2va 直接返回 image.width/image.height,
	// 传来的 aspect_ratio 被静默忽略),而网关这层拿到的图是 URL/base64,不解码就不知道
	// 宽高比。由前端按已加载的图算好经 metadata.width/height 透传(前端本来就要读图的
	// 像素尺寸做 256/5760/[0.4,2.5] 的前置校验);直连调用方不传则由引擎自算,出 768p。
	if taskType == "t2v" {
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
// 我们要的档位,拿它反推画布只会得到错的尺寸,故直接丢弃(H3 本期 pipeline 恒为 false,
// 正常不会出现)。
func h3NormalizeAspectRatio(body map[string]any) {
	// wan 专属,引擎不读,留着只会在排查时误导。
	delete(body, "target_shape")
	if _, ok := body["aspect_ratio"]; ok {
		delete(body, "ratio") // 已有权威值:别名清掉,免得两个键打架
		return
	}
	if r, ok := body["ratio"].(string); ok && strings.TrimSpace(r) != "" {
		body["aspect_ratio"] = common.NormalizeAspectRatio(r)
	}
	delete(body, "ratio")
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
