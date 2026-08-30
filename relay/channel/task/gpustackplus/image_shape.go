package gpustackplus

import "github.com/QuantumNous/new-api/common"

// 生图(t2i/i2i)的画幅整形。
//
// **这份逻辑与同步链路 relay/channel/gpustackplus/adaptor.go 的 setImageShape 是
// 刻意同语义的一对**,两边漂移的后果是静默的:同一个模型、同一个档位,走同步出对画幅、
// 走异步出 16:9,而请求两边都是 200。
//
// 为什么必须两边都有:图片异步化(docs/image-async-task-design.md)之后,体验区生图走的是
// middleware.ImageAsyncConvert → RelayTask → 本适配器这条**新链路**,它不经过同步链路的
// ConvertImageRequest。新链路当初只搬了 `AspectRatioFromSize` 那一半,于是:
//
//   - 比例词("1:1"/"16:9"):AspectRatioFromSize 只认 `WxH`,对冒号写法返回空串 ⇒
//     aspect_ratio **一个字段都不下发**;
//   - 精确像素("1664x928"):target_shape 压根没搬过来 ⇒ 引擎只能拿约分出来的
//     "52:29" 去查离散分辨率表,查不到就回落。
//
// 两条都通向同一个结果:引擎 ImageTaskRequest.aspect_ratio 的默认值**写死 "16:9"**,
// 不传就是强制横屏。2026-08-30 现网复现:qwen-image 选 1:1 出三张横图。
// (同一个坑在 i2i 上先被踩过一次:4:3 底图出 16:9 成品、构图被重排。)
//
// 优先级与同步链路一致:比例词直接透传 → 否则从精确像素约分兜底;有精确像素时再补
// target_shape,引擎会优先用它(2 元素精确像素出图,优于 aspect_ratio 的离散表)。
//
// ⚠️ 只对生图调用。target_shape 在视频侧是 **wan 专属**字段,H3 与 LTX-2.5 都在各自的
// apply*Request 里显式 delete 掉它(留着会让人误以为时长/画幅可控),无差别写等于把刚
// 删掉的键又塞回去。
func applyImageShape(body map[string]any, size string) {
	if size == "" {
		return
	}
	// 比例词优先。IsAspectRatio 认 "a:b",而 AspectRatioFromSize 只认 "WxH" ——
	// 少了前者,冒号写法就一路静默到引擎的 16:9 默认值。
	if common.IsAspectRatio(size) {
		body["aspect_ratio"] = common.NormalizeAspectRatio(size)
	} else if ar := common.AspectRatioFromSize(size); ar != "" {
		body["aspect_ratio"] = ar
	}
	if w, h, ok := common.DimsFromSize(size); ok {
		// 引擎的 target_shape 是 [height, width],不是 [width, height]。
		// 顺序写反不会报错,只会把横竖对调 —— 与本文件要修的是同一类静默故障。
		body["target_shape"] = []int{h, w}
	}
}

// imageShapeTaskTypes 是需要画幅整形的生图玩法。
//
// 只有这两个:其余 task_type 要么是视频/音频(画幅另有各自的约定),要么根本没有画幅概念。
var imageShapeTaskTypes = map[string]bool{"t2i": true, "i2i": true}
