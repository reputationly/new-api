package gpustackplus

// LTX-2.5 专属的请求整形。
//
// 单独成文件的理由与 minimax_h3.go 同:LTX-2.5 与本渠道原有的 LightX2V 系
// (wan / seedvr2 / infinitetalk)在帧数约定上根本不同,混在 adaptor.go 里会让两套
// 约定互相污染。
//
// 以下全部核实自 vllm-omni 的 vllm_omni/diffusion/models/ltx2/ltx2_request.py
// 与 2026-08-28/29 的服务态实测(docs/六模型上线部署与网关适配方案 §5.4/§A.1),
// 非文档推测:
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
//  3. **多卡还有第二重约束**:latent token = (W/32)×(H/32)×T 必须能被 SP 整除,
//     其中 T = (num_frames-1)/8 + 1。官方桶 960×544 的 (W/32)×(H/32) = 30×17 = 510
//     只含一个因子 2,所以 4 卡下 **T 必须是偶数**,否则去噪阶段直接报
//     "strict mode requires the sequence length to be evenly shardable"。
//
//     T 偶数 ⟺ num_frames ≡ 9 (mod 16),栅格间隔 16 帧 = 0.667 秒。
//     单纯的 `durationSec*24+1` 只有奇数秒落在这个栅格上(5→121 ✓,但 10→241 ✗,
//     T=31 是奇数)。10 秒档现在是单卡所以侥幸不炸,一旦改成多卡就是每请求 500 ——
//     这种"改部署才暴露"的雷不能留,所以统一**向上吸附到栅格**。
//
//  4. 向上而不是就近吸附:就近会让 10 秒落到 233 帧(9.71 s),**短于**对外承诺的时长。
//     向上吸附保证实际时长永不短于承诺(奇数秒 +0.04 s,偶数秒 +0.375 s)。
//
// 画布不在这里处理:引擎的 /v1/videos 直接认 OpenAI 风格的 size("WIDTHxHEIGHT"),
// adaptor 已经透传,不需要像 H3 那样反推 width/height。

const ltx25FPS = 24

// applyLTX25Request 把 LTX-2.5 需要的形状整形到 body 上。仅在模型声明了
// engine=ltx-2.5 时调用(见 common.VideoEngineFamilyForModel —— 判据是配置声明,
// 不是模型名)。
//
// durationSec 为 0 表示本次请求没给时长,此时不写 num_frames,由引擎按 pipeline
// 默认帧数决定。
//
// **调用方已有的显式取值一律优先**:metadata 是开放透传的(API 用户可直接下发
// num_frames 等引擎旋钮),这里只补默认,不覆盖用户意图 —— 与 H3 同一原则。
func applyLTX25Request(body map[string]any, durationSec int) {
	// wan 专属:引擎侧 LTX 分支从不读它。留着不会报错,只会在排查时误导。
	delete(body, "target_video_length")
	// InfiniteTalk 专属:输出时长 = min(驱动音频, video_duration, 参考视频)。
	// LTX-2.5 的音轨是与画面同步生成的,长度由帧数决定,与这个字段无关。
	delete(body, "video_duration")

	// seconds 对 LTX-2.5 恒非法(见文件头 §2)。即使调用方自己传了 num_frames,
	// 也要把它清掉:两者同时下发时引擎读哪个未验证,而 seconds 这条路只要被走到
	// 就是 500。清掉是唯一确定安全的做法。
	delete(body, "seconds")

	if durationSec <= 0 {
		return
	}
	if _, ok := body["num_frames"]; ok {
		// 调用方自己定了帧数。合法性(8k+1)交由引擎判定 —— 与本渠道对 size 的处理
		// 同一策略:门面不重复做引擎已经在做的校验,误伤成本高于收益。
		return
	}
	body["num_frames"] = ltx25FramesForDuration(durationSec)
	if _, ok := body["fps"]; !ok {
		body["fps"] = ltx25FPS
	}
}

// ltx25FramesForDuration 把对外时长(整数秒)换算成引擎帧数,向上吸附到
// `≡ 9 (mod 16)` 的栅格上 —— 该栅格同时满足 8k+1(引擎硬校验)与 T 为偶数
// (多卡 SP 整除),且实际时长永不短于承诺。理由见文件头 §3/§4。
//
//	4 秒 → 105(4.375 s)    9 秒 → 217(9.04 s)
//	5 秒 → 121(5.04 s)    10 秒 → 249(10.375 s)
//	6 秒 → 153(6.375 s)   11 秒 → 265(11.04 s)
//	7 秒 → 169(7.04 s)    12 秒 → 297(12.375 s)
//	8 秒 → 201(8.375 s)   13 秒 → 313(13.04 s)
//	                      15 秒 → 361(15.04 s)
func ltx25FramesForDuration(durationSec int) int {
	want := durationSec * ltx25FPS
	// ceil((want-9)/16),负数(极短时长)钳到栅格首项
	steps := (want - 9 + 15) / 16
	if steps < 0 {
		steps = 0
	}
	return 16*steps + 9
}
