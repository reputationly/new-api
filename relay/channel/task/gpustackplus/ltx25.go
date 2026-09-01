package gpustackplus

import (
	"fmt"
	"math"
	"sort"
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
//  5. **尺寸、时长与显存包络是硬拒**:宽高都要被 32 整除、短边 ≤ 1408(2K)、
//     对外时长 ≤ 15 秒、`W×H×num_frames ≤ 1.3×10⁹`。都是"进了队列也只会 500 或 OOM"
//     (或超出对外承诺)的输入,
//     挡在网关比让用户排队几分钟后失败好,所以在这里就地 400。
//     ⚠️ 2026-08-31 之前这两个上限是 704 / 4.3×10⁸,那是"1080p 交超分"那版的口径;
//     引擎侧加了解码前卸载 DiT 之后 1080p 与 2K 都能原生出片,上限随之抬高。
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

	// 一阶段档的最大短边。它同时是「这个画布归哪一档实例服务」的判据:
	// ltx25SizeTiers 里一阶段最大 704、两阶段最小 1088,中间没有重叠。
	ltx25MaxOneStageShortEdge = 704

	// 短边上限 = 2K 档的短边。2026-08-31 之前这里是 704(「1080p 交超分」的那版口径),
	// 引擎侧加了「VAE 解码前把 DiT 搬回主机内存」之后天花板整个抬掉了:1080p 与 2K
	// 都能原生出片,4K 只能出 5 秒、成本又高(283 s/条),对外不给,所以上限停在 1408。
	ltx25MaxShortEdge = 1408

	// 对外统一的时长上限(秒)。四个分辨率档共用一个数 —— 产品口径,好解释、好对外说,
	// 也免得用户去记"哪一档能出多久"。
	//
	// 15 秒是实测能站住的最大统一值。最吃紧的是 2K:2496×1408×361 = 1.269e9,
	// 2026-09-01 用八条不同题材压过一轮,**8/8 通过**(峰值 39.2~39.4 GB)。其余三档在
	// 15 秒下都远低于它:1080p 7.54e8、720p 3.17e8、540p 1.89e8。
	ltx25MaxOutputSec = 15

	// 显存包络:W×H×num_frames 的上限。2026-09-01(引擎侧分块 postprocess + gather
	// 收敛到 rank0 之后)的实测分界:
	//
	//	2496×1408×361 = 1.269e9 ✅  1920×1088×601 = 1.256e9 ✅  3840×2176×169 = 1.412e9 ✅
	//	1920×1088×697 = 1.456e9 ❌  2496×1408×425 = 1.494e9 ❌  3840×2176×201 = 1.680e9 ❌
	//
	// 取 1.30e9:**它的职责已经不是定对外时长了** —— 那件事由 ltx25MaxOutputSec 一个数
	// 说清楚。这里只需要"不挡住 15 秒的最大组合(2K 的 1.269e9)",同时仍然挡住菜单外的
	// 野组合(比如 21:9 那种超宽画幅、或直连 API 传个 30 秒)。
	//
	// ⚠️ 别再拿它当"每档能出多久"的表来读:1.30e9 除出来 1080p 是 622 帧、720p 是 1479 帧,
	// 那些都够不着,真正生效的是 15 秒那条。
	ltx25MaxPixelFrames = 1_300_000_000

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

	// 时长与包络放在最后:都需要最终生效的帧数,自传与换算两条路都要过这一关
	// (否则 API 用户能绕过体验区的档位限制)。
	//
	// 时长先于包络:15 秒是对外承诺,超了要告诉用户"最长 15 秒",而不是甩一句
	// "超出显存包络" —— 后者既看不懂也不是真正的原因(1.30e9 在 15 秒下根本够不着)。
	if framesKnown {
		if err := ltx25ValidateOutputLength(w, h, hasSize, frames); err != nil {
			return err
		}
	}
	if hasSize && framesKnown {
		if err := ltx25ValidateEnvelope(w, h, frames); err != nil {
			return err
		}
	}
	return nil
}

// ltx25ValidateOutputLength 校验对外统一的时长上限。
//
// 按**帧数**比而不是直接比秒数:不同尺寸的栅格粗细不同(见 ltx25FrameGrid),15 秒在
// m=2 的尺寸上落 361 帧(15.04 s)、在奇数 P 的尺寸上落 377 帧(15.71 s)。拿"15.0 秒"
// 这个数去卡后者,会把它自己的 15 秒档误伤掉 —— 上限必须用同一套栅格算出来。
func ltx25ValidateOutputLength(w, h int, hasSize bool, frames int) error {
	maxFrames := ltx25FramesForDuration(ltx25MaxOutputSec, w, h, hasSize)
	if frames <= maxFrames {
		return nil
	}
	return fmt.Errorf(
		"LTX-2.5 对外最长 %d 秒(该尺寸下 %d 帧),本次请求 %d 帧(约 %.1f 秒)",
		ltx25MaxOutputSec, maxFrames, frames, float64(frames)/ltx25FPS)
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
		if _, _, ok := ltx25SizeTier(size); ok {
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

	shortEdge, align, isTier := ltx25SizeTier(size)
	if !isTier {
		// 挡的是 540P / 720P / 4K 这类"看起来是行业档位、实际不是本模型档位"的写法。
		// 静默映射到最近的合法档更糟:用户以为拿到 720p,实际是 704,这个差异只在
		// 出片后量像素才看得出来。
		return fmt.Errorf(
			"LTX-2.5 的 size 只接受精确像素(如 %dx%d)或档位词 %s,收到 %q",
			ltx25OfficialSizes[0][0], ltx25OfficialSizes[0][1], ltx25SizeTierNames(), size)
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

	w, h := ltx25Canvas(shortEdge, ratio, align)
	if w <= 0 || h <= 0 {
		return fmt.Errorf("LTX-2.5 无法由档位词 %q 与宽高比 %q 推出合法画布", size, ar)
	}
	body["size"] = fmt.Sprintf("%dx%d", w, h)
	return nil
}

// ltx25SizeTierNames 把档位词表渲染成错误文案里的可选值,按短边从小到大。
// 不硬编码成字符串:菜单改了文案要跟着改,而"文案与实际取值分叉"这个仓里已经栽过。
func ltx25SizeTierNames() string {
	names := make([]string, 0, len(ltx25SizeTiers))
	for name := range ltx25SizeTiers {
		names = append(names, strings.ToUpper(name))
	}
	sort.Slice(names, func(i, j int) bool {
		a, _, _ := ltx25SizeTier(names[i])
		b, _, _ := ltx25SizeTier(names[j])
		return a < b
	})
	return strings.Join(names, " / ")
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

// ltx25SizeTiers 是对外档位词 → (短边像素, 对齐粒度)。
//
// **必须查表,不能按字面算**,两处都反直觉:
//
//  1. 短边不是档位词的字面值。`1080P` 的短边是 **1088** 不是 1080、`2K` 是 **1408**
//     不是 1440 —— 两阶段的引擎硬校验(ltx2_request.py 的
//     `alignment = vae_spatial_compression_ratio × max_spatial_downscale` = 32×2 = 64)
//     要求宽高都被 64 整除,而 1080/64=16.875、1440/64=22.5 都不整除,发过去直接被拒。
//     所以别复用 h3ShortEdgeFromSizeToken 的 `2k→1440`,照抄必炸。
//  2. 对齐粒度随档位变。544P/704P 由一阶段实例服务(对齐 32),1080P/2K 由**两阶段
//     实例**服务(对齐 64)。粒度写死 32 的话,`2K + 4:3` 会算出 1888×1408,
//     1888/64=29.5,引擎拒。
//
// 档位与实例是绑定的(--model-class-name 是启动参数、不是请求参数),所以一个部署只
// 服务这张表里的一部分档位;网关这里只管把词翻成像素,发给哪个实例由模型名决定。
var ltx25SizeTiers = map[string]struct {
	shortEdge int
	align     int
}{
	"544p":  {544, 32},  // 一阶段原生桶
	"704p":  {704, 32},  // 一阶段
	"1080p": {1088, 64}, // 两阶段,实测 1920×1088 最长 17.7 s(见 ltx25MaxPixelFrames)
	"2k":    {1408, 64}, // 两阶段,实测 2496/2560×1408 最长 10.4 s
}

// ltx25SizeTier 解析档位词;不是档位词返回 ok=false。
//
// 不收 4K:实测 3840×2176 只能出 5 秒(283 s/条、整机 0.21 条/分),对外不给。
// 与其解析出来再报"超界",不如让它落进"既不是像素串也不是档位词"那条更贴切的错误。
func ltx25SizeTier(size string) (shortEdge int, align int, ok bool) {
	t, hit := ltx25SizeTiers[strings.ToLower(strings.TrimSpace(size))]
	if !hit {
		return 0, 0, false
	}
	return t.shortEdge, t.align, true
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
// align 由档位决定(见 ltx25SizeTiers):一阶段 32、两阶段 64。两阶段那两档的结果:
//
//	1088 × 16/9 = 1934.2 → 1920   1408 × 16/9 = 2503.1 → 2496
//
// 2496×1408 比实测用的 2560×1408 更接近 16:9(偏 0.3% vs 2.3%)且面积小 2.5%,
// 与「704 短边取 1248 而不是 1280」是同一条理由;既然更小,实测过的包络照样成立。
func ltx25Canvas(shortEdge int, ratio float64, align int) (int, int) {
	if shortEdge <= 0 || align <= 0 || !(ratio > 0) || math.IsInf(ratio, 0) {
		return 0, 0
	}
	short := float64(shortEdge)
	if ratio >= 1.0 {
		return h3AlignMultiple(short*ratio, align), h3AlignMultiple(short, align)
	}
	return h3AlignMultiple(short, align), h3AlignMultiple(short/ratio, align)
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
	gw, gh := ltx25GridCanvas(w, h)
	p := (gw / ltx25AlignMultiple) * (gh / ltx25AlignMultiple)
	m := ltx25SequenceParallelSize / ltx25Gcd(p, ltx25SequenceParallelSize)
	return 8 * m, 8*(m-1) + 1
}

// ltx25GridCanvas 返回**算栅格时该用的那个画布**。
//
// 一阶段:就是请求的画布。
// 两阶段:是它的**一半** —— stage 1 在半分辨率上去噪(recipe 的 spatial_downscale=2),
// SP 切分发生在那里,栅格由半分辨率的 P 决定,不是最终尺寸。
//
// 2026-09-01 实测坐实:1920×1088 / 721 帧(T=91)报
// `seq_len=46410 not divisible by sequence_parallel_size=4`,而 46410 = 510 × 91,
// 510 = (960/32)×(544/32) —— 正是 stage 1 的 960×544,不是最终画布的 P=2040。
// 按最终尺寸算会得出 m=1(任意 8k+1 都合法),于是"1080p 10 秒"被换算成 241 帧
// (T=31 奇数)直接 500。这条洞之前没暴露,是因为验收时都直接发 num_frames 绕开了换算。
//
// 判据用**短边**而不是"宽高是否 64 对齐":704×704 也满足 64 对齐,但它是一阶段的桶
// (ltx25SizeTiers 里一阶段最大短边 704、两阶段最小短边 1088,中间没有重叠)。
func ltx25GridCanvas(w, h int) (int, int) {
	if min(w, h) > ltx25MaxOneStageShortEdge {
		return w / 2, h / 2
	}
	return w, h
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
	{2496, 1408}, // 2K   16:9, 两阶段(对齐 64)
	{1408, 1408}, // 2K   1:1
	{1920, 1088}, // 1080p 16:9, 两阶段
	{1088, 1088}, // 1080p 1:1
	{1248, 704},  // 720p 16:9, P=858, m=2
	{928, 704},   // 720p 4:3,  P=638, m=2
	{704, 704},   // 720p 1:1,  P=484, m=1
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

	// 两轮:先按画幅选出**同一族**的候选,再在族内按面积就近。
	//
	// 不能一轮比完。菜单跨了 540p~2K 五个分辨率之后,同一个画幅族里有三个桶
	// (16:9 有 1248×704 / 1920×1088 / 2496×1408),它们的比例误差差在小数点后三位;
	// 单轮「比例优先、并列才比面积」会把 700×400 这种小请求判给 1920×1088 ——
	// 画幅对了,分辨率却跳了两档。族内按面积就近才是"最接近"。
	minRatioErr := math.Inf(1)
	for _, s := range ltx25OfficialSizes {
		long, short := max(s[0], s[1]), min(s[0], s[1])
		if e := math.Abs(float64(long)/float64(short) - want); e < minRatioErr {
			minRatioErr = e
		}
	}
	// 0.01 的窗口:16:9 族内部三个桶的比例互差最多 0.008(1.7647 ~ 1.7727),
	// 而相邻画幅族(16:9 与 4:3)相差 0.44,不会被误并进来。
	bestW, bestH := 0, 0
	bestAreaErr := math.Inf(1)
	for _, s := range ltx25OfficialSizes {
		long, short := max(s[0], s[1]), min(s[0], s[1])
		if math.Abs(float64(long)/float64(short)-want) > minRatioErr+0.01 {
			continue
		}
		if areaErr := math.Abs(float64(s[0])*float64(s[1]) - area); areaErr < bestAreaErr {
			bestAreaErr = areaErr
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
