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
// 网关自己也得有一份,因为**提示词里的时长要用交付值**:H3 官方写作规范写着
// "S.SS is the effective video duration",而客户填的是名义秒数 —— 只给名义值,
// 判定的尾帧时刻就落在成片之外(5 秒的片子写成 5.00,实际最后一帧在 5.17)。
// 目前唯一的生产消费者是 service/aggregate_enhance_singlecall.go 的证据;回显、
// 计费那条路将来要用也必须从这里取,不要在别处复制一份对齐公式。
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
