package gpustackplus

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// LTX-2.5 专属的请求整形与准入校验。
//
// 单独成文件的理由与 minimax_h3.go 同:LTX-2.5 与本渠道原有的 LightX2V 系
// (wan / seedvr2 / infinitetalk)在帧数约定上根本不同,混在 adaptor.go 里会让两套
// 约定互相污染。
//
// 以下全部核实自 vllm-omni 的 vllm_omni/diffusion/models/ltx2/ltx2_request.py
// 与 2026-08-28/29/30 的服务态实测(docs/六模型上线部署与网关适配方案 §5.4/§A.1、
// docs/ltx25-playground-and-api-design.md §三),非文档推测:
//
//  1. **帧数栅格**:num_frames 必须是 8k+1,24 fps。wan 是 4n+1 @16fps。
//     发错栅格引擎直接 500:`num_frames must be 8 * k + 1, got 120`。
//
//  2. **seconds 参数对 LTX-2.5 恒不可用,必须由网关换算成 num_frames**。
//     引擎的 seconds 是 `^[1-9]\d*$`(只收整数秒),而 frames = seconds×24,
//     24 本身能被 8 整除 ⇒ frames ≡ 0 (mod 8),永远取不到 8k+1 需要的余数 1。
//     实测:seconds=5 → 500(got 120);seconds=5.04 → 400(正则不匹配)。
//     换句话说这不是"挑个合适的秒数就能绕过",而是整数秒在 24fps 下无解。
//
//  3. **多卡还有第二重约束,而且它随尺寸变**:latent token
//     `seq_len = (W/32)×(H/32)×T` 必须能被 sequence_parallel_size 整除,
//     其中 `T = (num_frames-1)/8 + 1`。这条**不能只按某一个尺寸写死** ——
//     令 `P = (W/32)×(H/32)`、`m = SP/gcd(P,SP)`,合法帧数是
//     `F ≡ 8(m-1)+1 (mod 8m)`,m 完全由尺寸决定:
//
//     P ≡ 0 (mod 4) → m=1 → F ≡ 1  (mod 8)    704×704(P=484)
//     P ≡ 2 (mod 4) → m=2 → F ≡ 9  (mod 16)   960×544(510)、1248×704(858)、928×704(638)
//     P 为奇数      → m=4 → F ≡ 25 (mod 32)   544×544(289)、736×544/544×736(391)
//
//     本文件曾经把中间那一行(≡9 mod 16)当成全局常量,于是奇数 P 的尺寸一开放就 500。
//     2026-08-30 4 卡实测 544×544:121/249/345 帧 200,而 361 帧(15 s)与 393 帧(16 s)
//     报 `seq_len=13294 not divisible by sequence_parallel_size=4` —— 13294 = 289×46,
//     与公式完全吻合。
//
//  4. 向上而不是就近吸附:就近会让 10 秒落到 233 帧(9.71 s),**短于**对外承诺的时长。
//     向上吸附保证实际时长永不短于承诺。
//
//  5. **尺寸与显存包络是硬拒**:宽高都要被 32 整除、短边 ≤ 704、
//     `W×H×num_frames ≤ 4.3×10⁸`。三条都是"进了队列也只会 500 或 OOM"的输入,
//     挡在网关比让用户排队几分钟后失败好,所以在这里就地 400。
//
//  6. **画布要在这里合成**:引擎只认 OpenAI 风格的 size("WIDTHxHEIGHT")或 width/height。
//     它请求体里那个 aspect_ratio 字段是 **H3 专用**的,LTX 的 pipeline 从不读 ——
//     所以体验区那套「分辨率档位 + 具名比例」必须由网关合成像素,见 ltx25ApplyCanvas。
//     2026-08-31 现网就是在这里炸的:运营按字段提示填了档位词 "704P",原样透传到引擎,
//     用户拿到的是一段 `String should match pattern '^\d+x\d+$'` 的 pydantic 报错。

const (
	ltx25FPS = 24

	// 宽高两轴都必须被 32 整除,同时也是 latent patch 的边长(P 由 W/32 × H/32 得出)。
	ltx25AlignMultiple = 32

	// 短边上限。超过它引擎侧 VAE 解码后的 all-gather 会把 fp32 整段视频张量压在单卡上,
	// 加卡无用 —— 1080p 在 4×A100-40G 上时长上限只有约 11 秒,已定案交给超分链路。
	ltx25MaxShortEdge = 704

	// 显存包络:W×H×num_frames 的上限。最大实测点 1248×704×489(20.375 s)峰值
	// 37.0 GB / 40 GB,面积×帧数 = 4.296×10⁸,即这个数就是实测天花板本身。
	ltx25MaxPixelFrames = 430_000_000

	// 现网 LTX-2.5 部署的 sequence_parallel_size(dev-gpustack-a100-0017,4×A100-40G
	// 整机单实例)。它只参与**默认帧数的选取**,不参与对调用方自传帧数的校验 ——
	// 见 applyLTX25Request 里的注释。
	ltx25SequenceParallelSize = 4
)

// applyLTX25Request 把 LTX-2.5 需要的形状整形到 body 上,并做准入校验。
// 仅在模型声明了 engine=ltx-2.5 时调用(见 common.VideoEngineFamilyForModel ——
// 判据是配置声明,不是模型名)。
//
// durationSec 为 0 表示本次请求没给时长,此时不写 num_frames,由引擎按 pipeline
// 默认帧数决定。
//
// **调用方已有的显式取值一律优先**:metadata 是开放透传的(API 用户可直接下发
// num_frames 等引擎旋钮),这里只补默认,不覆盖用户意图 —— 与 H3 同一原则。
//
// 返回非 nil 时调用方应就地 400(本地错误,不触发跨渠道重试)。
func applyLTX25Request(body map[string]any, durationSec int) error {
	// wan 专属:引擎侧 LTX 分支从不读它。留着不会报错,只会在排查时误导。
	delete(body, "target_video_length")
	// InfiniteTalk 专属:输出时长 = min(驱动音频, video_duration, 参考视频)。
	// LTX-2.5 的音轨是与画面同步生成的,长度由帧数决定,与这个字段无关。
	delete(body, "video_duration")

	// seconds 对 LTX-2.5 恒非法(见文件头 §2)。即使调用方自己传了 num_frames,
	// 也要把它清掉:两者同时下发时引擎读哪个未验证,而 seconds 这条路只要被走到
	// 就是 500。清掉是唯一确定安全的做法。
	delete(body, "seconds")

	// 画布合成必须在尺寸准入之前:体验区下发的是档位词 + 具名比例(见文件头 §6),
	// 合成完才有 W/H 可校验、可算栅格。
	if err := ltx25ApplyCanvas(body); err != nil {
		return err
	}

	// 尺寸准入必须在帧数换算之前:换算要读 W/H 算栅格,尺寸本身不合法时算出来的
	// 帧数也没有意义,报"帧数超包络"更会把用户指向错误的方向。
	w, h, hasSize := ltx25Dims(body)
	if hasSize {
		if err := ltx25ValidateSize(w, h); err != nil {
			return err
		}
	}

	frames, framesKnown := ltx25IntField(body, "num_frames")
	switch {
	case framesKnown:
		// 调用方自己定了帧数,不覆盖。
		//
		// **也不校验它的栅格**:8k+1 是引擎的硬校验(报错自解释),而 SP 整除依赖
		// sequence_parallel_size —— 那是部署侧的数,网关只是抄了现网的 4。拿一个可能
		// 过期的部署常量去硬拒调用方显式表达的意图,误伤成本高于收益;真发错了引擎会拒。
		// 包络则不同:那是 OOM,引擎不会给出干净的 400,必须由这里挡(见下)。
	case durationSec > 0:
		frames = ltx25FramesForDuration(durationSec, w, h, hasSize)
		framesKnown = true
		body["num_frames"] = frames
		if _, ok := body["fps"]; !ok {
			body["fps"] = ltx25FPS
		}
	}

	// 包络校验放在最后:它需要最终生效的帧数,自传与换算两条路都要过这一关
	// (否则 API 用户能绕过体验区的档位限制发出必然 OOM 的组合)。
	if hasSize && framesKnown {
		if err := ltx25ValidateEnvelope(w, h, frames); err != nil {
			return err
		}
	}
	return nil
}

// ltx25NamedAspectRatios 是体验区「宽高比」下拉的具名值 → 数值。
//
// 与 h3NamedAspectRatios 数值相同但**刻意各存一份**:那张表是引擎契约
// (MINIMAX_H3_SUPPORTED_ASPECT_RATIOS,发表外的值 H3 直接 400),这张表是**网关自己**
// 推画布用的枚举 —— LTX 的引擎根本不读 aspect_ratio。两者会因各自的原因变动,
// 共用一张表会让改 H3 的人不知不觉改掉 LTX 的画布。
//
// 表里有的不等于菜单上有:实测跑过的是 16:9 / 4:3 / 1:1 及其竖版,21:9 只是能推出
// 合法画布(1632×704),它的长时长会被 ltx25ValidateEnvelope 挡下。开不开由运营配置决定。
var ltx25NamedAspectRatios = map[string]float64{
	"21:9": 21.0 / 9.0,
	"16:9": 16.0 / 9.0,
	"4:3":  4.0 / 3.0,
	"1:1":  1.0,
	"3:4":  3.0 / 4.0,
	"9:16": 9.0 / 16.0,
}

// ltx25ApplyCanvas 把体验区的「分辨率档位词 + 具名宽高比」合成引擎要的精确像素。
//
// 为什么 LTX 要有这一步而 wan 系不用:H3 的引擎自己吃 aspect_ratio + short_edge 并
// 自算画布,体验区配档位词就够了;LTX 的引擎只认 width/height / size,档位词会原样撞上
// SizeStr 的 `^\d+x\d+$`。把这层差异吃在网关里,两个模型在体验区的填法才能一致 ——
// 否则运营得记住"这个模型填像素、那个模型填档位",而记错的代价是线上 400。
//
// 三种入参形态:
//   - 已有 width/height:调用方自定画布,只把档位词从 size 上清掉(留着必被引擎拒);
//   - size 是像素串:原样放行(API 直连的常规形态,也是运营改填精确像素后的形态);
//   - size 是档位词:与具名比例合成像素串写回 size。
//
// 返回非 nil 时调用方就地 400。
func ltx25ApplyCanvas(body map[string]any) error {
	// 必须先归一:体验区**不发 aspect_ratio**(见 ltx25NormalizeAspectRatio)。
	// 放在所有早退分支之前,像素串那条路也要把 wan 专属的 target_shape 清掉。
	ltx25NormalizeAspectRatio(body)

	size, _ := body["size"].(string)
	size = strings.TrimSpace(size)

	_, hasW := body["width"]
	_, hasH := body["height"]
	if hasW || hasH {
		// 调用方自定画布,不覆盖。但档位词对引擎的 SizeStr 是非法值,仍要清掉。
		if ltx25ShortEdgeFromSizeToken(size) > 0 {
			delete(body, "size")
		}
		return nil
	}
	if size == "" {
		return nil // 没给尺寸:引擎按 pipeline 默认画布(960×544)出片
	}
	if _, _, ok := common.DimsFromSize(size); ok {
		return nil // 已经是像素串
	}

	shortEdge := ltx25ShortEdgeFromSizeToken(size)
	if shortEdge <= 0 {
		return fmt.Errorf(
			"LTX-2.5 的 size 只接受精确像素(如 %dx%d)或分辨率档位词(如 %dP),收到 %q",
			ltx25OfficialSizes[0][0], ltx25OfficialSizes[0][1], ltx25MaxShortEdge, size)
	}
	if shortEdge > ltx25MaxShortEdge || shortEdge%ltx25AlignMultiple != 0 {
		// 挡的是 540P / 720P 这类"看起来是行业档位、实际不是本模型短边"的写法。
		// 静默映射到最近的合法短边更糟:用户以为拿到 720,实际是 704,而这个差异
		// 只在出片后量像素才看得出来。
		return fmt.Errorf(
			"LTX-2.5 的分辨率档位词短边必须是 %d 的倍数且不超过 %d,%q 不合规(如 544P、704P)",
			ltx25AlignMultiple, ltx25MaxShortEdge, size)
	}

	ar, _ := body["aspect_ratio"].(string)
	ratio, ok := ltx25NamedAspectRatios[common.NormalizeAspectRatio(strings.ToLower(strings.TrimSpace(ar)))]
	if !ok {
		// 推不出画布。**不能像 H3 那样清掉档位词降级**:H3 清掉后引擎按 short_edge=768
		// 自算,出的还是同一档;LTX 清掉后引擎回落到 pipeline 默认的 960×544 —— 用户选的
		// 704P 被静默换成另一档,那种错比 400 难查得多。
		return fmt.Errorf(
			"LTX-2.5 使用分辨率档位词(%q)时必须同时指定具名宽高比(16:9 / 9:16 / 4:3 / 3:4 / 1:1 / 21:9),收到 %q",
			size, ar)
	}

	w, h := ltx25Canvas(shortEdge, ratio)
	if w <= 0 || h <= 0 {
		return fmt.Errorf("LTX-2.5 无法由档位词 %q 与宽高比 %q 推出合法画布", size, ar)
	}
	body["size"] = fmt.Sprintf("%dx%d", w, h)
	return nil
}

// ltx25NormalizeAspectRatio 把体验区实际发出的宽高比字段归一到本文件要的 aspect_ratio。
//
// **体验区不发 aspect_ratio**,它按引擎族二选一(useVideoGeneration.js 的 usesTargetShape):
//
//	usePipeline 且非 H3 → metadata.target_shape = [h, w]   (wan 的 t2v runner 只认这个)
//	其余                → metadata.ratio        = "16:9"
//
// LTX 跑在 gpustackplus 上,`video_pipeline_flag_migrated` 迁移会把它自动标成
// pipeline:true,而它的引擎族又不是 H3 —— 于是**每一发文生视频都落进 target_shape 分支,
// 一个比例字段都不带**。ltx25ApplyCanvas 只读 aspect_ratio 的话,运营一旦把 sizes 配成
// 档位词,体验区就是发一次 400 一次。H3 在 h3NormalizeAspectRatio 里踩过同一个坑
// (它漏了是静默出错档,我们漏了是硬 400),这里是同款补丁。
//
// target_shape 只用来反推**比例**,绝不当画布用:那是 wan 的 720p 级固定值表
// ([720,1280] 等),既不是 32 的倍数也不是用户选的档位。取值后即删 —— 与
// target_video_length / video_duration 同理,LTX 引擎不读,留着只会在排查时误导。
func ltx25NormalizeAspectRatio(body map[string]any) {
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
	// 两个别名都没有,才轮到 target_shape 兜底 —— ratio 是调用方直接表达的,更权威。
	//
	// 复用 h3AspectRatioFromTargetShape:它是纯算术(shape → 具名比例名),不是 H3 的
	// 引擎契约。⚠️ 它匹配用的是 h3NamedAspectRatios,与本文件的 ltx25NamedAspectRatios
	// 目前值相同;哪天两张表分叉,这里要一起改(前端 videoSteps.test.js 守着
	// aspectRatioToShape 与这个反推的容差契约)。
	if ar := h3AspectRatioFromTargetShape(shape); ar != "" {
		body["aspect_ratio"] = ar
	}
}

// ltx25ShortEdgeFromSizeToken 从档位词取短边像素:"704P"/"544p" → 704/544;不是档位词返回 0。
//
// 不像 h3ShortEdgeFromSizeToken 那样收 2K/4K:LTX 的短边上限是 704,那两个词无论怎么
// 解释都超界,与其解析出来再报"超界",不如让它落进"既不是像素串也不是档位词"那条
// 更贴切的错误 —— 用户真正需要知道的是"这个模型没有 2K"。
func ltx25ShortEdgeFromSizeToken(size string) int {
	s := strings.ToLower(strings.TrimSpace(size))
	if !strings.HasSuffix(s, "p") {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimSuffix(s, "p"))
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// ltx25Canvas 按 (短边, 宽高比) 推画布,两轴对齐到 32(四舍五入,与 h3Canvas 同法)。
//
// 不做 H3 那样的面积钳位:LTX 的闸门是 W×H×帧数(ltx25ValidateEnvelope),纯面积没有
// 独立上限,提前按面积缩一次只会让推出来的画布与运营在菜单上看到的档位对不上。
//
// 四舍五入而不是向下取整,是为了让结果正好落在实测过的官方桶上:
//
//	544 × 4/3  = 725.3 → 736(向下取整会得 704,画幅偏 3%)   704 × 4/3  = 938.7 → 928
//	544 × 16/9 = 967.1 → 960                                704 × 16/9 = 1251.6 → 1248
//
// 这六个值与 docs/ltx25-playground-and-api-design.md §三 里 4 卡实测过的尺寸一一对应。
func ltx25Canvas(shortEdge int, ratio float64) (int, int) {
	if shortEdge <= 0 || !(ratio > 0) || math.IsInf(ratio, 0) {
		return 0, 0
	}
	short := float64(shortEdge)
	if ratio >= 1.0 {
		return h3AlignMultiple(short*ratio, ltx25AlignMultiple), h3AlignMultiple(short, ltx25AlignMultiple)
	}
	return h3AlignMultiple(short, ltx25AlignMultiple), h3AlignMultiple(short/ratio, ltx25AlignMultiple)
}

// ltx25Dims 从 body["size"]("WIDTHxHEIGHT",adaptor 已透传顶层 size)解析宽高。
// 解析不出(档位词如 "720P"、缺失、乱填)返回 ok=false —— 此时既不校验也不按尺寸
// 算栅格,退回对现网菜单里所有偶数 P 尺寸都安全的 mod 16 行为。
func ltx25Dims(body map[string]any) (w, h int, ok bool) {
	s, _ := body["size"].(string)
	return common.DimsFromSize(s)
}

// ltx25IntField 取 body 里的整型字段。metadata 走 JSON 反序列化,数字落地成 float64;
// 网关自己写进去的是 int,两种都要收下。
func ltx25IntField(body map[string]any, key string) (int, bool) {
	v, ok := body[key]
	if !ok {
		return 0, false
	}
	f, ok := h3ToFloat(v)
	if !ok || f <= 0 {
		return 0, false
	}
	return int(f), true
}

// ltx25FrameGrid 按尺寸算出合法帧数的栅格:返回 (step, first),合法帧数即
// `first + k*step`(k ≥ 0)。见文件头 §3。
//
// P = (W/32)×(H/32)、m = SP/gcd(P,SP),则 step = 8m、first = 8(m-1)+1。
// m=1 时退化为引擎的裸约束 8k+1(单卡或 P 已被 SP 整除)。
func ltx25FrameGrid(w, h int) (step, first int) {
	p := (w / ltx25AlignMultiple) * (h / ltx25AlignMultiple)
	m := ltx25SequenceParallelSize / ltx25Gcd(p, ltx25SequenceParallelSize)
	return 8 * m, 8*(m-1) + 1
}

func ltx25Gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	if a < 0 {
		return -a
	}
	if a == 0 {
		return 1
	}
	return a
}

// ltx25FramesForDuration 把对外时长(整数秒)换算成引擎帧数,**按尺寸**向上吸附到
// 该尺寸的合法栅格上 —— 栅格同时满足 8k+1(引擎硬校验)与 SP 整除(多卡),
// 且实际时长永不短于承诺。理由见文件头 §3/§4。
//
// hasSize=false(API 直连可能不给 size)时退回 ≡9 (mod 16):它对现网菜单里所有
// 偶数 P 的尺寸都是安全的(m=2 的栅格是 m=1 的子集),奇数 P 尺寸必须配 size 才能开放。
//
//	960×544(m=2):  5 s → 121   10 s → 249   15 s → 361   18 s → 441
//	1248×704(m=2): 5 s → 121   10 s → 249   14 s → 345
//	544×544(m=4):  5 s → 121   10 s → 249   15 s → 377   16 s → 409
//	704×704(m=1):  栅格最细,24d 向上取到最近的 8k+1
func ltx25FramesForDuration(durationSec, w, h int, hasSize bool) int {
	step, first := 16, 9
	if hasSize {
		step, first = ltx25FrameGrid(w, h)
	} else {
		common.SysLog("[gpustackplus] LTX-2.5: 请求未带可解析的 size,帧数按 ≡9 (mod 16) 栅格换算;奇数 P 的尺寸(如 544x544)必须显式提供 WIDTHxHEIGHT 才能取到正确栅格")
	}
	want := durationSec * ltx25FPS
	steps := (want - first + step - 1) / step // ceil((want-first)/step)
	if steps < 0 {
		steps = 0
	}
	return step*steps + first
}

// ltx25OfficialSizes 是上线菜单里的横向尺寸桶(竖版由本文件自动转置得到,
// 方版转置后与自身相同)。现网只开 704 这一档短边,按宽高比给五个:
//
//	16:9 → 1248x704   9:16 → 704x1248
//	4:3  → 928x704    3:4  → 704x928
//	1:1  → 704x704
//
// **它不是准入白名单**,只用来生成错误文案里的建议值 —— 准入仍然只看三条硬约束
// (32 对齐 / 短边 ≤704 / 面积×帧数 ≤ 4.3e8),那三条才是引擎的真实契约。直连调用方
// 发一个桶外但合法的尺寸(如 800×480、544×544)照样放行,R1 的按尺寸栅格换算对任意
// 合法尺寸都成立,不依赖这张表。
//
// 为什么建议值要落在桶上而不是「就近对齐到 32」:后者会算出 704×416 这种引擎收得下、
// 但菜单里没有、也没标过定价的野值 —— 技术上合法,业务上不是我们想让人照着发的。
// 运营配的、计费覆盖的、实测跑过的,就是这几个。
//
// 短边 544 那一档(960×544 / 736×544 / 544×544)引擎支持、我们也放行,但**不列进来**:
// 它不在菜单上,把人往那儿引等于制造一批没定过价的请求。将来菜单加档时再往这里补,
// 加漏了的后果只是建议值不够近,不会让合法请求被拒。
var ltx25OfficialSizes = [][2]int{
	{1248, 704}, // 16:9, P=858, m=2
	{928, 704},  // 4:3,  P=638, m=2
	{704, 704},  // 1:1,  P=484, m=1
}

// ltx25ValidateSize 校验尺寸的两条硬约束:短边 ≤ 704、32 对齐。
//
// 两条都给同一个建议值(ltx25SuggestSize),而不是各算各的 —— 用户发的尺寸常常两条
// 全违(1920×1080 既超短边又不对齐,1280×720 同样),各算各的会给出一个「照着改还是
// 被拒」的建议:只按 32 就近对齐 720 得 736,仍然超短边上限。
//
// 报哪一条按**更根本的那条**排:短边超界要改的是分辨率档位,对齐只是抹个零头。
func ltx25ValidateSize(w, h int) error {
	sw, sh := ltx25SuggestSize(w, h)
	if short := min(w, h); short > ltx25MaxShortEdge {
		return fmt.Errorf(
			"LTX-2.5 的短边上限是 %d 像素,%dx%d 的短边为 %d;画幅最接近的官方尺寸是 %dx%d(更高分辨率请走超分链路)",
			ltx25MaxShortEdge, w, h, short, sw, sh)
	}
	if w%ltx25AlignMultiple != 0 || h%ltx25AlignMultiple != 0 {
		return fmt.Errorf(
			"LTX-2.5 的宽高必须都是 %d 的倍数,%dx%d 不合规;画幅最接近的官方尺寸是 %dx%d",
			ltx25AlignMultiple, w, h, sw, sh)
	}
	return nil
}

// ltx25SuggestSize 在官方桶里挑一个**画幅最接近**的,并跟随请求的朝向(横/竖/方)。
//
// 按比例挑而不是按面积:用户选的是画幅。1280×720 想要的是 16:9,给它 1248×704
// (1.773,对 16:9 的 1.778 偏 0.3%)才是同一个画幅;按面积挑会给出一个画幅不对的桶,
// 而画幅不对意味着构图整个变了。比例相同时(如 704×704 与 544×544 对方图)才按面积
// 就近,取更接近用户原始分辨率的那个。
func ltx25SuggestSize(w, h int) (int, int) {
	if w <= 0 || h <= 0 {
		return ltx25OfficialSizes[0][0], ltx25OfficialSizes[0][1]
	}
	portrait := h > w
	want := float64(max(w, h)) / float64(min(w, h)) // 恒 ≥1,与朝向无关
	area := float64(w) * float64(h)

	bestW, bestH := 0, 0
	bestRatioErr, bestAreaErr := math.Inf(1), math.Inf(1)
	for _, s := range ltx25OfficialSizes {
		long, short := max(s[0], s[1]), min(s[0], s[1])
		ratioErr := math.Abs(float64(long)/float64(short) - want)
		areaErr := math.Abs(float64(s[0])*float64(s[1]) - area)
		// 比例优先;比例基本相同(浮点噪声级)时才比面积
		if ratioErr < bestRatioErr-1e-9 ||
			(math.Abs(ratioErr-bestRatioErr) <= 1e-9 && areaErr < bestAreaErr) {
			bestRatioErr, bestAreaErr = ratioErr, areaErr
			bestW, bestH = long, short
		}
	}
	if portrait {
		return bestH, bestW
	}
	return bestW, bestH
}

// ltx25ValidateEnvelope 校验显存包络 W×H×num_frames ≤ 4.3×10⁸,
// 并在超界时给出该尺寸下的最长可用秒数。
func ltx25ValidateEnvelope(w, h, frames int) error {
	area := w * h
	if area <= 0 || area*frames <= ltx25MaxPixelFrames {
		return nil
	}
	return fmt.Errorf(
		"LTX-2.5 在 %dx%d 下最多能出 %d 帧(约 %.1f 秒),本次请求的 %d 帧(约 %.1f 秒)会超出显存包络;请缩短时长或降低分辨率",
		w, h, ltx25MaxFramesForArea(w, h), ltx25MaxDurationSec(w, h),
		frames, float64(frames)/ltx25FPS)
}

// ltx25MaxFramesForArea 返回该尺寸下落在包络内的最大**合法**帧数(仍在栅格上)。
func ltx25MaxFramesForArea(w, h int) int {
	step, first := ltx25FrameGrid(w, h)
	limit := ltx25MaxPixelFrames / (w * h)
	if limit < first {
		return 0
	}
	return first + (limit-first)/step*step
}

// ltx25MaxDurationSec 把上面那个帧数换算回秒,供错误文案使用。
func ltx25MaxDurationSec(w, h int) float64 {
	return float64(ltx25MaxFramesForArea(w, h)) / ltx25FPS
}
