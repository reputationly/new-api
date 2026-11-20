package common

// MiniMax H3 的输出时长与引擎的帧栅格。
//
// 引擎(vllm-omni)的 H3 出片帧数**不是**请求秒数×fps,而是先按 round(秒×24) 算出
// 请求帧数,再向上吸附到 17n+5 栅格(`time_request._align_frame_count`)。于是:
//
//	请求 1 s  → 39 帧  → 交付 1.625 s
//	请求 5 s  → 124 帧 → 交付 5.167 s
//	请求 15 s → 362 帧 → 交付 15.083 s
//	请求 20 s → 481 帧 → 交付 20.042 s
//
// 这层换算必须由网关自己也有一份,理由有三:
//
//  1. **回显**:客户看到的 `seconds` 与账单必须对应真实产物,而不是他填的数字;
//  2. **计费**:按秒计价的模型如果按请求秒收费,1 s 的单会白送 0.625 s 算力;
//  3. **校验**:续写要判断"生成段至少 1.625 s",而这是栅格的下限,不是 1 s。
//
// 只有**一个**地方可以改这套换算,就是这里 —— 引擎改栅格时两边一起改,否则回显、
// 计费、校验三处会各错各的。
const (
	// H3FPS H3 的固定出片帧率(引擎 preprocessing / time_request 一致)。
	H3FPS = 24
	// h3FrameGridStep 帧栅格的步长:n 每加 1,帧数 +17。
	h3FrameGridStep = 17
	// h3FrameGridBase 栅格的基准偏移帧数(第 1 档 = 22 帧)。
	h3FrameGridBase = 5
)

// H3AlignFrameCount 把帧数向上吸附到 17n+5 栅格。
//
// 与引擎逐位一致(含 <=0 的退化输入返回 1),因为我们拿它算的是**同一个数**:
// 客户账单上的交付时长。
func H3AlignFrameCount(frames int) int {
	if frames <= 0 {
		return 1
	}
	aligned := frames
	for aligned%h3FrameGridStep != h3FrameGridBase {
		aligned++
	}
	return aligned
}

// H3DeliveredDuration 请求秒数 → (对齐后的帧数, 交付秒数)。
//
// requestedSeconds <= 0(未指定,由引擎取默认帧数)时返回 (0, 0) —— 调用方据此
// **不要**编一个时长出来:默认档的帧数由引擎决定,网关猜不准。
func H3DeliveredDuration(requestedSeconds int) (frames int, seconds float64) {
	if requestedSeconds <= 0 {
		return 0, 0
	}
	aligned := H3AlignFrameCount(requestedSeconds * H3FPS)
	return aligned, float64(aligned) / float64(H3FPS)
}

// IsH3VideoModel 判断模型是否属 H3 引擎族。
//
// 判据是**运营配置里声明的 engine**(VideoModelConfig.models[name].engine),不是
// 模型名 substring —— 名字是随上游改的,配置声明才是"这个模型跑在什么引擎上"的
// 唯一记录。传多个候选名(公开名 / 上游名)是因为重定向后两者可能不同。
func IsH3VideoModel(names ...string) bool {
	return VideoEngineFamilyForModel(names...) == VideoEngineMinimaxH3
}
