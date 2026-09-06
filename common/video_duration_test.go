package common

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

// box 拼一个 ISO BMFF box。
func box(typ string, payload []byte) []byte {
	out := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint32(out[0:4], uint32(8+len(payload)))
	copy(out[4:8], typ)
	copy(out[8:], payload)
	return out
}

// mvhdV0 造一个 version 0 的 mvhd 载荷。
func mvhdV0(timescale uint32, duration uint32) []byte {
	p := make([]byte, 4+16)
	p[0] = 0 // version 0
	binary.BigEndian.PutUint32(p[4+8:4+12], timescale)
	binary.BigEndian.PutUint32(p[4+12:4+16], duration)
	return p
}

// mvhdV1 造一个 version 1 的 mvhd 载荷(64 位时长)。
func mvhdV1(timescale uint32, duration uint64) []byte {
	p := make([]byte, 4+28)
	p[0] = 1
	binary.BigEndian.PutUint32(p[4+16:4+20], timescale)
	binary.BigEndian.PutUint64(p[4+20:4+28], duration)
	return p
}

func mp4With(mvhd []byte, extraLeading ...[]byte) *bytes.Reader {
	var buf []byte
	buf = append(buf, box("ftyp", []byte("isomiso2avc1"))...)
	for _, e := range extraLeading {
		buf = append(buf, e...)
	}
	buf = append(buf, box("moov", box("mvhd", mvhd))...)
	return bytes.NewReader(buf)
}

// 常规 mp4:600 单位 / 每秒 60 → 10 秒。
func TestGetVideoDurationV0(t *testing.T) {
	d, err := GetVideoDuration(mp4With(mvhdV0(60, 600)))
	require.NoError(t, err)
	require.InDelta(t, 10.0, d, 0.001)
}

// version 1 的 64 位时长同样要认 —— 长视频与部分设备产出的就是这种。
func TestGetVideoDurationV1(t *testing.T) {
	d, err := GetVideoDuration(mp4With(mvhdV1(1000, 90000)))
	require.NoError(t, err)
	require.InDelta(t, 90.0, d, 0.001)
}

// moov 在 mdat 之后(未 faststart 的文件很常见)也要能找到。
// mdat 往往是整个文件的主体,解析器必须跳过它而不是钻进去扫。
func TestGetVideoDurationWithMdatBeforeMoov(t *testing.T) {
	mdat := box("mdat", bytes.Repeat([]byte{0xAB}, 4096))
	d, err := GetVideoDuration(mp4With(mvhdV0(600, 3000), mdat))
	require.NoError(t, err)
	require.InDelta(t, 5.0, d, 0.001)
}

// 不是 ISO BMFF(如 webm/随机字节)要明确报"不支持",让调用方回退按次计费。
func TestGetVideoDurationRejectsNonBMFF(t *testing.T) {
	for _, raw := range [][]byte{
		[]byte("not a video at all"),
		{0x1A, 0x45, 0xDF, 0xA3, 0x01, 0x02, 0x03, 0x04}, // webm/EBML
		{},
	} {
		_, err := GetVideoDuration(bytes.NewReader(raw))
		require.ErrorIs(t, err, ErrVideoDurationUnsupported)
	}
}

// timescale 为 0 是损坏文件里见得到的形态 —— 必须挡住除零,而不是 panic。
func TestGetVideoDurationRejectsZeroTimescale(t *testing.T) {
	_, err := GetVideoDuration(mp4With(mvhdV0(0, 600)))
	require.ErrorIs(t, err, ErrVideoDurationUnsupported)
}

// 时长为 0 要报"不支持"而不是返回 0 秒:0 与"没读到"在计费侧是两回事,
// 前者会让按秒计费算出 0 元。
func TestGetVideoDurationRejectsZeroDuration(t *testing.T) {
	_, err := GetVideoDuration(mp4With(mvhdV0(600, 0)))
	require.ErrorIs(t, err, ErrVideoDurationUnsupported)
}

// 全 1 是"未知时长"的哨兵(直播/分片封装),不能当成一个天文数字的秒数去计费。
func TestGetVideoDurationRejectsUnknownSentinel(t *testing.T) {
	_, err := GetVideoDuration(mp4With(mvhdV0(600, 0xFFFFFFFF)))
	require.ErrorIs(t, err, ErrVideoDurationUnsupported)

	_, err = GetVideoDuration(mp4With(mvhdV1(600, 0xFFFFFFFFFFFFFFFF)))
	require.ErrorIs(t, err, ErrVideoDurationUnsupported)
}

// 畸形 box 长度不能把解析拖死或读越界。
func TestGetVideoDurationHandlesMalformedBox(t *testing.T) {
	bad := make([]byte, 16)
	binary.BigEndian.PutUint32(bad[0:4], 0xFFFFFFF0) // 声称的长度远超文件
	copy(bad[4:8], "moov")
	_, err := GetVideoDuration(bytes.NewReader(bad))
	require.ErrorIs(t, err, ErrVideoDurationUnsupported)
}
