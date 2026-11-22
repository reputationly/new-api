package controller

import (
	"strings"
	"testing"
)

// MiniMax 语音请求 → 我们自己的语音请求。app 的内置 minimax 适配器发的就是这组字段，
// 映射错了会表现为"语音合成成功但没声音/格式不对"，不报错。
func TestConvertT2ARequestMapsFields(t *testing.T) {
	raw := `{
	  "model":"indextts-2.5","text":"你好，世界","stream":false,
	  "voice_setting":{"voice_id":"female-shaonv","speed":1.2,"vol":1,"pitch":0,"emotion":"happy"},
	  "audio_setting":{"sample_rate":32000,"bitrate":128000,"format":"MP3","channel":1},
	  "language_boost":"Chinese","output_format":"url"
	}`
	req, msg := convertT2ARequest([]byte(raw))
	if msg != "" {
		t.Fatalf("unexpected error: %s", msg)
	}
	if req.Model != "indextts-2.5" || req.Input != "你好，世界" {
		t.Fatalf("model/input 映射错: %+v", req)
	}
	if req.Voice != "female-shaonv" {
		t.Fatalf("voice_id 应映射到 voice: %q", req.Voice)
	}
	if req.ResponseFormat != "mp3" {
		t.Fatalf("format 应小写映射: %q", req.ResponseFormat)
	}
	if req.Speed == nil || *req.Speed != 1.2 {
		t.Fatalf("speed 应透传: %+v", req.Speed)
	}
}

// 没给 format 时默认 mp3（官方缺省也是 mp3）。
func TestConvertT2ARequestDefaultsToMP3(t *testing.T) {
	req, msg := convertT2ARequest([]byte(`{"model":"m","text":"hi"}`))
	if msg != "" {
		t.Fatalf("unexpected error: %s", msg)
	}
	if req.ResponseFormat != "mp3" {
		t.Fatalf("默认格式应为 mp3，实际 %q", req.ResponseFormat)
	}
}

// 做不到的显式拒绝：流式、缺字段。报错文案要能让客户端直接改。
func TestConvertT2ARequestRejectsUnsupported(t *testing.T) {
	cases := []struct{ name, body, want string }{
		{"流式", `{"model":"m","text":"hi","stream":true}`, "stream=true"},
		{"缺 model", `{"text":"hi"}`, "model is required"},
		{"缺 text", `{"model":"m"}`, "text is required"},
		{"非 JSON", `not json`, "not valid JSON"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, msg := convertT2ARequest([]byte(tc.body))
			if !strings.Contains(msg, tc.want) {
				t.Fatalf("错误文案应包含 %q，实际 %q", tc.want, msg)
			}
		})
	}
}

// audio_format 报的是字节的真实容器,不是请求值:客户端拿它决定落盘/解码,
// 报错了就是"扩展名与字节不符",播放器报错而服务端看起来一切正常。
func TestMinimaxAudioFormatUsesBytes(t *testing.T) {
	cases := []struct {
		name      string
		audio     []byte
		requested string
		want      string
	}{
		{"mp3 字节、请求 mp3", []byte("ID3\x04\x00\x00"), "mp3", "mp3"},
		{"wav 字节、请求 mp3(上游回落默认容器)", []byte("RIFF\x24\x00\x00\x00WAVEfmt "), "mp3", "wav"},
		{"mp3 字节、请求 wav", []byte("ID3\x04\x00\x00"), "wav", "mp3"},
		{"认不出魔数 → 回落到请求值", []byte("opaque"), "flac", "flac"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := minimaxAudioFormat(tc.audio, tc.requested); got != tc.want {
				t.Fatalf("audio_format 应为 %q,实际 %q", tc.want, got)
			}
		})
	}
}
