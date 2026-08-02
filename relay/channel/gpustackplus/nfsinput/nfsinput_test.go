package nfsinput

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// makeWAV 造一段指定时长的合法 PCM WAV(8kHz/单声道/16bit),只为让时长解析有东西可读,
// 内容全是静音。canonical 44 字节头 + data。
func makeWAV(seconds float64) []byte {
	const sampleRate = 8000
	const channels = 1
	const bitsPerSample = 16
	byteRate := sampleRate * channels * bitsPerSample / 8
	dataLen := int(float64(byteRate) * seconds)

	buf := new(bytes.Buffer)
	buf.WriteString("RIFF")
	binary.Write(buf, binary.LittleEndian, uint32(36+dataLen))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	binary.Write(buf, binary.LittleEndian, uint32(16))            // PCM fmt chunk 大小
	binary.Write(buf, binary.LittleEndian, uint16(1))             // PCM
	binary.Write(buf, binary.LittleEndian, uint16(channels))      //
	binary.Write(buf, binary.LittleEndian, uint32(sampleRate))    //
	binary.Write(buf, binary.LittleEndian, uint32(byteRate))      //
	binary.Write(buf, binary.LittleEndian, uint16(channels*2))    // blockAlign
	binary.Write(buf, binary.LittleEndian, uint16(bitsPerSample)) //
	buf.WriteString("data")
	binary.Write(buf, binary.LittleEndian, uint32(dataLen))
	buf.Write(make([]byte, dataLen))
	return buf.Bytes()
}

// makeMP3 造 n 帧 MPEG-1 Layer III / 128kbps / 44100Hz 的静音 mp3。
// 帧头 FF FB 90 00:FF FB = 帧同步 + MPEG1 + Layer III + 无 CRC;0x90 高四位 1001 = 128kbps,
// 采样率位 00 = 44100,无填充。每帧 1152 采样,帧长 144*128000/44100 = 417 字节。
func makeMP3(frames int) []byte {
	const frameLen = 417
	buf := new(bytes.Buffer)
	for i := 0; i < frames; i++ {
		buf.Write([]byte{0xFF, 0xFB, 0x90, 0x00})
		buf.Write(make([]byte, frameLen-4))
	}
	return buf.Bytes()
}

// 回归:传进来的 ext 不可信,时长闸必须按真实文件头判定容器。
//
// URL 下载 / multipart / 裸 base64 三条路都不带 ext,addBytesExt 会一律兜底成 .wav;
// data-uri 的 MIME 又是客户端自己写的。若拿这个 ext 去解析,非 wav 音频必然解析失败,
// 而解析失败是放行 —— 上限就被完全绕过了。
func TestCheckAudioDurationIgnoresWrongExt(t *testing.T) {
	// 约 60 秒的 mp3(每帧 1152/44100 秒),伪装成 URL 下载后兜底出来的 .wav
	mp3 := makeMP3(2300)
	m := (&Materializer{}).SetMaxAudioSeconds(30)
	if err := m.checkAudioDuration(FieldAudio, mp3, ".wav"); err == nil {
		t.Fatal("URL 路径兜底的 .wav 不应让 mp3 绕过时长上限")
	}
	// 空 ext(AddBytes 直接调用)同样要拦住
	if err := m.checkAudioDuration(FieldAudio, mp3, ""); err == nil {
		t.Fatal("空 ext 不应让 mp3 绕过时长上限")
	}
	// 反向:wav 被谎报成 .mp3,也要按真实内容算,且未超限时正常放行
	wav := makeWAV(10)
	if err := m.checkAudioDuration(FieldAudio, wav, ".mp3"); err != nil {
		t.Fatalf("wav 谎报成 mp3 时应按真实内容判定并放行,got %v", err)
	}
	// 同一段 mp3 在上限之下应放行,证明上面的拒绝来自时长而非解析失败
	m = (&Materializer{}).SetMaxAudioSeconds(300)
	if err := m.checkAudioDuration(FieldAudio, mp3, ".wav"); err != nil {
		t.Fatalf("60s mp3 在 300s 上限下应放行,got %v", err)
	}
}

func TestAudioExtFromMagic(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		want string
	}{
		{"wav", makeWAV(1), ".wav"},
		{"mp3 帧同步", makeMP3(1), ".mp3"},
		{"mp3 ID3", append([]byte("ID3\x04\x00\x00"), make([]byte, 32)...), ".mp3"},
		{"adts aac", []byte{0xFF, 0xF1, 0x50, 0x80, 0x00, 0x1F, 0xFC}, ".aac"},
		{"ogg", append([]byte("OggS"), make([]byte, 32)...), ".ogg"},
		{"flac", append([]byte("fLaC"), make([]byte, 32)...), ".flac"},
		{"m4a", append([]byte{0, 0, 0, 0x20}, append([]byte("ftypM4A "), make([]byte, 16)...)...), ".m4a"},
		{"非音频", []byte("hello world, not audio"), ""},
		{"过短", []byte{0xFF}, ""},
	}
	for _, c := range cases {
		if got := audioExtFromMagic(c.data); got != c.want {
			t.Errorf("%s: audioExtFromMagic = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCheckAudioDuration(t *testing.T) {
	tenSec := makeWAV(10)

	// 未配上限 → 放行(哪怕音频很长)
	m := &Materializer{}
	if err := m.checkAudioDuration(FieldAudio, tenSec, ".wav"); err != nil {
		t.Fatalf("no limit should pass, got %v", err)
	}

	// 非音频字段 → 不参与时长校验。视频/图片字段共用同一个 Materializer,不该被误伤。
	m = (&Materializer{}).SetMaxAudioSeconds(1)
	if err := m.checkAudioDuration(FieldVideo, tenSec, ".wav"); err != nil {
		t.Fatalf("non-audio field should be exempt, got %v", err)
	}

	// 未超限 → 放行
	m = (&Materializer{}).SetMaxAudioSeconds(30)
	if err := m.checkAudioDuration(FieldAudio, tenSec, ".wav"); err != nil {
		t.Fatalf("10s under 30s limit should pass, got %v", err)
	}

	// 超限 → 拒
	m = (&Materializer{}).SetMaxAudioSeconds(5)
	if err := m.checkAudioDuration(FieldAudio, tenSec, ".wav"); err == nil {
		t.Fatal("10s over 5s limit should reject")
	}

	// 边界:恰好等于上限不算超(用 > 而非 >=)
	m = (&Materializer{}).SetMaxAudioSeconds(10)
	if err := m.checkAudioDuration(FieldAudio, tenSec, ".wav"); err != nil {
		t.Fatalf("exactly at limit should pass, got %v", err)
	}

	// 认不出容器且解析不了 → 放行,不当违规。这类字节实际到不了这里(addBytesExt 里
	// magicOK 先要求 isAudioBytes),留着是兜住 magicOK 放行、解析器却覆盖不到的边角格式。
	m = (&Materializer{}).SetMaxAudioSeconds(1)
	if err := m.checkAudioDuration(FieldAudio, []byte("not an audio file"), ".wav"); err != nil {
		t.Fatalf("unparseable audio should pass through, got %v", err)
	}
	// 但扩展名不认识不等于放行:容器按文件头判定,.xyz 照样会被识别成 wav 并卡住时长。
	if err := m.checkAudioDuration(FieldAudio, tenSec, ".xyz"); err == nil {
		t.Fatal("unknown ext must not bypass the limit: container comes from magic bytes")
	}
}

// 落盘扩展名:不带 ext 的三条路(URL 下载 / multipart / 裸 base64)此前一律落到 extForField
// 的类别默认值,mp3 被存成 .wav、webm 被存成 .mp4,误导按扩展名识别容器的下游引擎。
func TestExtFromMagic(t *testing.T) {
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	jpg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}
	webm := append([]byte{0x1A, 0x45, 0xDF, 0xA3}, make([]byte, 16)...)
	mp4 := append([]byte{0, 0, 0, 0x20}, append([]byte("ftypisom"), make([]byte, 16)...)...)
	mov := append([]byte{0, 0, 0, 0x20}, append([]byte("ftypqt  "), make([]byte, 16)...)...)

	cases := []struct {
		name  string
		field Field
		data  []byte
		want  string
	}{
		{"音频 mp3 不再落 .wav", FieldAudio, makeMP3(1), ".mp3"},
		{"音频 wav", FieldAudio, makeWAV(1), ".wav"},
		{"图片 jpg 不再落 .png", FieldImage, jpg, ".jpg"},
		{"图片 png", FieldImage, png, ".png"},
		{"视频 webm 不再落 .mp4", FieldVideo, webm, ".webm"},
		{"视频 mp4", FieldVideo, mp4, ".mp4"},
		// ISO BMFF 只有 brand 分得开 mov 与 mp4,认得 "qt  " 才报 .mov
		{"视频 mov 靠 brand 区分", FieldVideo, mov, ".mov"},
		// 认不出就返回空,由 extForField 落默认值,不猜
		{"认不出", FieldImage, []byte("plain text"), ""},
		// 未知字段不参与推导
		{"未知字段", Field("whatever"), png, ""},
	}
	for _, c := range cases {
		if got := extFromMagic(c.field, c.data); got != c.want {
			t.Errorf("%s: extFromMagic = %q, want %q", c.name, got, c.want)
		}
	}
}

// 优先级:调用方给的已知 ext(data-uri MIME / 任务引用)不被文件头覆盖。
// mov 与 mp4 同为 ISO BMFF 且多数 mov 不带 "qt  " brand,靠 magic 会误判成 .mp4,
// 所以 data-uri 声明的 video/quicktime 必须保住 —— 覆盖了就是退步。
func TestDeclaredExtWinsOverMagic(t *testing.T) {
	// extForData 认 video/quicktime → .mov;该 ext 非空,addBytesExt 就不会再走 extFromMagic
	if got := extForData("data:video/quicktime;base64,AAAA"); got != ".mov" {
		t.Fatalf("extForData(video/quicktime) = %q, want .mov", got)
	}
	// 而同一段字节若没有声明,magic 只能给到 .mp4
	isoNoBrand := append([]byte{0, 0, 0, 0x20}, append([]byte("ftypisom"), make([]byte, 16)...)...)
	if got := extFromMagic(FieldVideo, isoNoBrand); got != ".mp4" {
		t.Fatalf("extFromMagic(ISO BMFF) = %q, want .mp4", got)
	}
}

// isAudioField 与 extForField 必须共用同一份名单:extForField 给音频字段兜底 .wav,
// 时长闸也按同一组字段生效,两处漂移会让某个音频字段悄悄绕过校验。
func TestIsAudioFieldMatchesExtForField(t *testing.T) {
	all := []Field{
		FieldImage, FieldLastFrame, FieldImageMask, FieldSrcRefImages,
		FieldVideo, FieldSrcVideo, FieldSrcMask,
		FieldAudio, FieldVoice, FieldEmotionAudio, FieldReferenceAudio, FieldSrcAudio,
		FieldRefAudio, FieldRefAudio2, FieldPromptAudio, FieldTargetAudio,
	}
	for _, f := range all {
		if got, want := isAudioField(f), extForField(f) == ".wav"; got != want {
			t.Fatalf("field %s: isAudioField=%v but extForField=%s", f, got, extForField(f))
		}
	}
}
