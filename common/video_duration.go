package common

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// 视频时长解析(ISO BMFF:mp4 / mov / m4v)。
//
// 为什么需要它:超分这类"输入什么就输出什么"的任务,时长是**输入文件的固有属性**,
// 客户不会传、也不该要求他传(他多半不知道自己那段视频几秒)。而按秒计费必须拿到秒数,
// 拿不到就只能回退固定价 —— 那正是超分现在按次计费的原因。
//
// 与 common/audio.go 同一条路子:纯 Go 解析容器头,不依赖外部 ffmpeg/ffprobe
// (那会给部署加一个二进制依赖,而我们只需要一个数字)。
//
// 只做 ISO BMFF。mp4 与 mov 共用这个容器,覆盖超分输入的绝大多数情形
// (nfsinput 给视频字段的默认后缀就是 .mp4)。webm/mkv 的 EBML 需要另一套解析器,
// 项目在音频侧就因为同样的理由放弃了它,这里保持一致:认不出就让调用方回退。

// mvhd 里时长的两种编码宽度(version 0 用 32 位,version 1 用 64 位)。
const (
	// maxBoxScan 最多扫描多少个顶层 box。moov 通常在文件头或尾,几个 box 之内必然命中;
	// 设上限是为了防着损坏文件里的畸形长度把解析拖成死循环。
	maxBoxScan = 64
	// maxBoxDepth 递归深度上限。moov→mvhd 只需要 2 层,给点余量即可;
	// 无上限的话,一个自引用的畸形 box 会把栈吃光。
	maxBoxDepth = 8
)

// ErrVideoDurationUnsupported 容器不是 ISO BMFF,或结构里没有可用的时长。
// 调用方应据此回退(例如退回按次计费),而不是把请求判失败。
var ErrVideoDurationUnsupported = errors.New("unsupported video container for duration probing")

// GetVideoDuration 从 ISO BMFF(mp4/mov)里读出时长(秒)。
//
// 认不出容器、或时长字段缺失/为 0 时返回 ErrVideoDurationUnsupported ——
// **不返回 0 秒**:0 与"没读到"在计费侧是完全不同的两件事,前者会让按秒计费算出 0 元。
func GetVideoDuration(r io.ReadSeeker) (float64, error) {
	if r == nil {
		return 0, ErrVideoDurationUnsupported
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return 0, fmt.Errorf("seek video: %w", err)
	}
	size, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return 0, fmt.Errorf("size video: %w", err)
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return 0, fmt.Errorf("seek video: %w", err)
	}

	dur, err := findMvhdDuration(r, 0, size, 0)
	if err != nil {
		return 0, err
	}
	if dur <= 0 {
		return 0, ErrVideoDurationUnsupported
	}
	return dur, nil
}

// findMvhdDuration 在 [start, end) 区间里逐个 box 扫,遇到 moov 就下钻,遇到 mvhd 就解析。
func findMvhdDuration(r io.ReadSeeker, start, end int64, depth int) (float64, error) {
	if depth > maxBoxDepth {
		return 0, ErrVideoDurationUnsupported
	}
	offset := start
	for i := 0; i < maxBoxScan && offset < end; i++ {
		boxType, payloadStart, boxEnd, err := readBoxHeader(r, offset, end)
		if err != nil {
			return 0, err
		}
		switch boxType {
		case "mvhd":
			return parseMvhd(r, payloadStart, boxEnd)
		case "moov":
			// 只有 moov 需要下钻:mvhd 就在它下面。其余顶层 box(ftyp/mdat/free…)
			// 直接跳过 —— mdat 往往是整个文件的主体,进去扫纯属浪费。
			if d, err := findMvhdDuration(r, payloadStart, boxEnd, depth+1); err == nil {
				return d, nil
			}
		}
		offset = boxEnd
	}
	return 0, ErrVideoDurationUnsupported
}

// readBoxHeader 读一个 box 头,返回类型与载荷区间。
//
// box 头是 [4 字节大小][4 字节类型];大小为 1 时后面跟 8 字节的扩展大小(大文件),
// 为 0 时表示"一直到文件尾"。这两种特例不处理就会在大 mp4 上算出错误的偏移,
// 进而把后续 box 全部错位。
func readBoxHeader(r io.ReadSeeker, offset, limit int64) (string, int64, int64, error) {
	if _, err := r.Seek(offset, io.SeekStart); err != nil {
		return "", 0, 0, ErrVideoDurationUnsupported
	}
	var head [8]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return "", 0, 0, ErrVideoDurationUnsupported
	}
	size := int64(binary.BigEndian.Uint32(head[0:4]))
	boxType := string(head[4:8])
	payloadStart := offset + 8

	switch size {
	case 0:
		// 到文件尾。
		size = limit - offset
	case 1:
		// 64 位扩展大小紧跟在头后面。
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return "", 0, 0, ErrVideoDurationUnsupported
		}
		size = int64(binary.BigEndian.Uint64(ext[:]))
		payloadStart += 8
	}
	// 畸形长度(比头还小、或越过文件尾)一律判定为不可解析,而不是硬着头皮往下走:
	// 那会让偏移乱掉并可能死循环。
	if size < 8 || offset+size > limit {
		return "", 0, 0, ErrVideoDurationUnsupported
	}
	return boxType, payloadStart, offset + size, nil
}

// parseMvhd 解析 mvhd 里的 timescale 与 duration。
//
// 布局(version 0):version(1) flags(3) creation(4) modification(4) timescale(4) duration(4)
//
//	(version 1):version(1) flags(3) creation(8) modification(8) timescale(4) duration(8)
func parseMvhd(r io.ReadSeeker, start, end int64) (float64, error) {
	if _, err := r.Seek(start, io.SeekStart); err != nil {
		return 0, ErrVideoDurationUnsupported
	}
	var version [4]byte // version(1) + flags(3)
	if _, err := io.ReadFull(r, version[:]); err != nil {
		return 0, ErrVideoDurationUnsupported
	}

	var timescale uint32
	var duration uint64
	switch version[0] {
	case 0:
		var buf [16]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return 0, ErrVideoDurationUnsupported
		}
		timescale = binary.BigEndian.Uint32(buf[8:12])
		duration = uint64(binary.BigEndian.Uint32(buf[12:16]))
	case 1:
		var buf [28]byte
		if _, err := io.ReadFull(r, buf[:]); err != nil {
			return 0, ErrVideoDurationUnsupported
		}
		timescale = binary.BigEndian.Uint32(buf[16:20])
		duration = binary.BigEndian.Uint64(buf[20:28])
	default:
		return 0, ErrVideoDurationUnsupported
	}

	if timescale == 0 {
		// 除零保护。timescale=0 在规范里非法,但损坏文件里见得到。
		return 0, ErrVideoDurationUnsupported
	}
	// duration 全 1 是"未知时长"的惯用哨兵(直播/分片封装),不能当成一个巨大的秒数。
	if version[0] == 0 && duration == 0xFFFFFFFF {
		return 0, ErrVideoDurationUnsupported
	}
	if version[0] == 1 && duration == 0xFFFFFFFFFFFFFFFF {
		return 0, ErrVideoDurationUnsupported
	}
	_ = end
	return float64(duration) / float64(timescale), nil
}

// GetVideoDurationFromPartial 从**文件片段**里尽力解析时长。
//
// 给 HTTP Range 探测用:那时手上只有文件的一段(头部或尾部),box 树是残缺的,
// 从 offset 0 逐个 box 走必然走偏。这里改为直接搜 `mvhd` 标记再就地解析 ——
// 它是我们唯一要的东西,不必先把 moov 树走通。
//
// 代价是可能误匹配(视频码流里恰好出现 "mvhd" 这四个字节)。用两道合理性校验兜住:
// timescale 必须非零、算出的秒数必须落在一个正常视频的区间内。误匹配落到区间外就
// 当作探测失败,回退固定价 —— 与其它失败路径同一个归宿。
func GetVideoDurationFromPartial(data []byte) (float64, error) {
	const marker = "mvhd"
	for off := 0; off < len(data); {
		idx := bytes.Index(data[off:], []byte(marker))
		if idx < 0 {
			break
		}
		start := off + idx + len(marker)
		if d, err := parseMvhdPayload(data[start:]); err == nil && d > 0 {
			return d, nil
		}
		off = off + idx + 1
	}
	return 0, ErrVideoDurationUnsupported
}

// parseMvhdPayload 解析紧跟在 "mvhd" 之后的载荷(version+flags 起)。
func parseMvhdPayload(p []byte) (float64, error) {
	if len(p) < 4 {
		return 0, ErrVideoDurationUnsupported
	}
	version := p[0]
	var timescale uint32
	var duration uint64
	switch version {
	case 0:
		if len(p) < 4+16 {
			return 0, ErrVideoDurationUnsupported
		}
		timescale = binary.BigEndian.Uint32(p[4+8 : 4+12])
		duration = uint64(binary.BigEndian.Uint32(p[4+12 : 4+16]))
		if duration == 0xFFFFFFFF {
			return 0, ErrVideoDurationUnsupported
		}
	case 1:
		if len(p) < 4+28 {
			return 0, ErrVideoDurationUnsupported
		}
		timescale = binary.BigEndian.Uint32(p[4+16 : 4+20])
		duration = binary.BigEndian.Uint64(p[4+20 : 4+28])
		if duration == 0xFFFFFFFFFFFFFFFF {
			return 0, ErrVideoDurationUnsupported
		}
	default:
		return 0, ErrVideoDurationUnsupported
	}
	if timescale == 0 {
		return 0, ErrVideoDurationUnsupported
	}
	return float64(duration) / float64(timescale), nil
}
