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

// 回归:容器按真实文件头判定,与调用方声明的扩展名无关。
//
// URL 下载 / multipart / 裸 base64 三条路都不带 ext,addBytesExt 会兜底成 .wav;data-uri
// 的 MIME 又是客户端自己写的。早前版本拿这个 ext 去解析,非 wav 音频必然解析失败,而当时
// 解析失败是放行——上限就被完全绕过。现在容器只认文件头,这几条路都堵上了。
func TestCheckAudioDurationUsesMagicNotDeclaredExt(t *testing.T) {
	// 约 60 秒的 mp3(每帧 1152/44100 秒)
	mp3 := makeMP3(2300)
	m := (&Materializer{}).SetMaxAudioSeconds(30)
	if err := m.checkAudioDuration(FieldAudio, mp3); err == nil {
		t.Fatal("60s mp3 应被 30s 上限拦住(容器须按文件头判定)")
	}
	// wav 同样按真实内容算,未超限正常放行
	if err := m.checkAudioDuration(FieldAudio, makeWAV(10)); err != nil {
		t.Fatalf("10s wav 在 30s 上限下应放行,got %v", err)
	}
	// 同一段 mp3 在上限之下应放行,证明上面的拒绝来自时长而非解析失败
	m = (&Materializer{}).SetMaxAudioSeconds(300)
	if err := m.checkAudioDuration(FieldAudio, mp3); err != nil {
		t.Fatalf("60s mp3 在 300s 上限下应放行,got %v", err)
	}
}

// 配了上限时,文件头声称是某容器、内容却解不出来的一律拒。这层顺带收掉 magicOK 挡不住的
// polyglot:只看头几个字节的嗅探放行了它,真解码这一步才拆穿。
func TestCheckAudioDurationRejectsUndecodable(t *testing.T) {
	m := (&Materializer{}).SetMaxAudioSeconds(30)

	// polyglot:前 12 字节伪装成 RIFF/WAVE,载荷是 PE 可执行文件。magicOK 会放行。
	polyglot := append([]byte("RIFF\x00\x00\x00\x00WAVE"),
		[]byte{0x4D, 0x5A, 0x90, 0x00, 0x03, 0x00, 0x00, 0x00}...)
	if !magicOK(FieldAudio, polyglot) {
		t.Fatal("前提失效:该 polyglot 本应能过 magicOK,否则这个用例失去意义")
	}
	if err := m.checkAudioDuration(FieldAudio, polyglot); err == nil {
		t.Fatal("伪装 WAVE 头的 polyglot 应被真解码这一步拒掉")
	}

	// 截断的 wav:头完整、data 块缺失
	truncated := makeWAV(10)[:30]
	if err := m.checkAudioDuration(FieldAudio, truncated); err == nil {
		t.Fatal("残缺的 wav 应被拒")
	}

	// 未配上限时本闸整体不生效,同样的字节要放行(加固只在管理员显式开启后生效)
	if err := (&Materializer{}).checkAudioDuration(FieldAudio, polyglot); err != nil {
		t.Fatalf("未配 maxAudioSec 时不应拦截,got %v", err)
	}
}

// 不变量:isAudioBytes 与 audioExtFromMagic 必须逐条对齐。
//
// 「解析失败即拒」建立在这条不变量上——magicOK 放行的音频,audioExtFromMagic 一定认得
// 容器、且返回 GetAudioDuration 支持的扩展名。若有人给 isAudioBytes 加了新签名却忘了
// audioExtFromMagic,那个格式就会走成「认不出 → 解析失败 → 被拒」,合法输入被误伤。
func TestAudioMagicListsAligned(t *testing.T) {
	samples := [][]byte{
		makeWAV(1), makeMP3(1), adtsAAC,
		append([]byte("ID3\x04\x00\x00"), make([]byte, 32)...),
		append([]byte("OggS"), make([]byte, 32)...),
		append([]byte("fLaC"), make([]byte, 32)...),
		append([]byte{0, 0, 0, 0x20}, append([]byte("ftypM4A "), make([]byte, 16)...)...),
		[]byte("plain text, not audio"), gif, avi, nil,
	}
	supported := map[string]bool{
		".wav": true, ".mp3": true, ".aac": true, ".ogg": true, ".flac": true, ".m4a": true,
	}
	for i, b := range samples {
		okMagic := isAudioBytes(b)
		ext := audioExtFromMagic(b)
		if okMagic != (ext != "") {
			t.Errorf("样本 %d: isAudioBytes=%v 但 audioExtFromMagic=%q — 两份签名名单漂移了", i, okMagic, ext)
		}
		// 返回值必须落在 GetAudioDuration 支持的集合里,否则合法音频会被判成解析失败
		if ext != "" && !supported[ext] {
			t.Errorf("样本 %d: audioExtFromMagic 返回 %q,GetAudioDuration 不支持", i, ext)
		}
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
		// .aac 不在 allowedRefExts 里,但这个函数刻意不收口:少了它 ADTS AAC 解不出
		// 时长,而解析失败是放行策略,上限会被绕过。收口只在 extFromMagic 那侧。
		{"adts aac", adtsAAC, ".aac"},
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
	if err := m.checkAudioDuration(FieldAudio, tenSec); err != nil {
		t.Fatalf("no limit should pass, got %v", err)
	}

	// 非音频字段 → 不参与时长校验。视频/图片字段共用同一个 Materializer,不该被误伤。
	m = (&Materializer{}).SetMaxAudioSeconds(1)
	if err := m.checkAudioDuration(FieldVideo, tenSec); err != nil {
		t.Fatalf("non-audio field should be exempt, got %v", err)
	}

	// 未超限 → 放行
	m = (&Materializer{}).SetMaxAudioSeconds(30)
	if err := m.checkAudioDuration(FieldAudio, tenSec); err != nil {
		t.Fatalf("10s under 30s limit should pass, got %v", err)
	}

	// 超限 → 拒
	m = (&Materializer{}).SetMaxAudioSeconds(5)
	if err := m.checkAudioDuration(FieldAudio, tenSec); err == nil {
		t.Fatal("10s over 5s limit should reject")
	}

	// 边界:恰好等于上限不算超(用 > 而非 >=)
	m = (&Materializer{}).SetMaxAudioSeconds(10)
	if err := m.checkAudioDuration(FieldAudio, tenSec); err != nil {
		t.Fatalf("exactly at limit should pass, got %v", err)
	}

	// 认不出容器 → 放行。这类字节实际到不了这里(addBytesExt 里 magicOK 先要求
	// isAudioBytes),留着是兜住「两份签名名单漂移」这个本方 bug:那种情况下不该让用户的
	// 合法音频替我们买单。真正的漂移由 TestAudioMagicListsAligned 挡在 CI。
	m = (&Materializer{}).SetMaxAudioSeconds(1)
	if err := m.checkAudioDuration(FieldAudio, []byte("not an audio file")); err != nil {
		t.Fatalf("认不出容器时应放行,got %v", err)
	}
}

// magic 认得、但不在 allowedRefExts 里的容器:今天靠类别默认值工作,不能改名。
var (
	adtsAAC = []byte{0xFF, 0xF1, 0x50, 0x80, 0x00, 0x1F, 0xFC}
	gif     = append([]byte("GIF89a"), make([]byte, 16)...)
	avi     = append([]byte("RIFF\x00\x00\x00\x00AVI "), make([]byte, 16)...)
	flv     = append([]byte("FLV\x01"), make([]byte, 16)...)
	mpegPS  = append([]byte{0x00, 0x00, 0x01, 0xBA}, make([]byte, 16)...)
)

// 落盘扩展名:不带 ext 的三条路(URL 下载 / multipart / 裸 base64)此前一律落到 extForField
// 的类别默认值,mp3 被存成 .wav、webm 被存成 .mp4。这是可读性问题,不影响下游解码
// (引擎按内容嗅探,核对依据见 extForData 注释)。
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

		// 白名单之外的容器一律回退默认值。这些格式今天靠类别默认值(.wav/.mp4/.png)工作,
		// 下游按内容嗅探解得开;给它们换上真实后缀反而可能被扩展名白名单拒掉。
		{"adts aac 回退", FieldAudio, adtsAAC, ""},
		{"avi 回退", FieldVideo, avi, ""},
		{"flv 回退", FieldVideo, flv, ""},
		{"mpeg-ps 回退", FieldVideo, mpegPS, ""},
		{"gif 回退", FieldImage, gif, ""},
	}
	for _, c := range cases {
		if got := extFromMagic(c.field, c.data); got != c.want {
			t.Errorf("%s: extFromMagic = %q, want %q", c.name, got, c.want)
		}
	}
}

// 不变量:extFromMagic 只能产出 allowedRefExts 里的后缀,不给系统引入新扩展名。
// magic 识别范围比白名单宽,这条断言挡住「加了新签名却忘了它不在白名单里」。
func TestExtFromMagicNeverEscapesWhitelist(t *testing.T) {
	fields := []Field{FieldImage, FieldAudio, FieldVideo, FieldVoice, FieldSrcVideo, Field("unknown")}
	payloads := [][]byte{
		makeWAV(1), makeMP3(1), adtsAAC, gif, avi, flv, mpegPS,
		{0x89, 0x50, 0x4E, 0x47}, {0xFF, 0xD8, 0xFF, 0xE0},
		append([]byte("OggS"), make([]byte, 16)...),
		append([]byte("fLaC"), make([]byte, 16)...),
		append([]byte{0, 0, 0, 0x20}, append([]byte("ftypisom"), make([]byte, 8)...)...),
		[]byte("not media at all"), nil,
	}
	for _, f := range fields {
		for _, p := range payloads {
			if ext := extFromMagic(f, p); ext != "" && !allowedRefExts[ext] {
				t.Errorf("field %s: extFromMagic 返回了白名单外的 %q", f, ext)
			}
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
