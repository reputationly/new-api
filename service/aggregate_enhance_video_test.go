package service

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/setting/system_setting"
)

// allowLocalFetch 让 httptest 的 127.0.0.1:<随机端口> 通过 SSRF 校验。
//
// 生产默认是开防护 + 只允许 80/443/8080/8443,测试里必须临时放开 —— 否则
// 这里的用例测的是校验器,不是内联逻辑。
func allowLocalFetch(t *testing.T) {
	t.Helper()
	fs := system_setting.GetFetchSetting()
	origSSRF, origPrivate := fs.EnableSSRFProtection, fs.AllowPrivateIp
	fs.EnableSSRFProtection, fs.AllowPrivateIp = false, true
	client := GetHttpClient()
	InitHttpClient()
	t.Cleanup(func() {
		fs.EnableSSRFProtection, fs.AllowPrivateIp = origSSRF, origPrivate
		SetHttpClientForTest(client)
	})
}

// **远程参考视频要内联成 data URI。**
//
// 引擎自己出网拉视频会偶发挂死(30s→120s→480s 重试链),内联之后它只需要解码字节。
func TestInlineEnhanceVideosConvertsRemoteVideo(t *testing.T) {
	allowLocalFetch(t)
	payload := []byte("fake-mp4-bytes-0123456789")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	got := inlineEnhanceVideos(context.Background(), []string{srv.URL + "/clip.mp4"})
	require.Len(t, got, 1)
	require.True(t, strings.HasPrefix(got[0], "data:video/mp4;base64,"))
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(got[0], "data:video/mp4;base64,"))
	require.NoError(t, err)
	require.Equal(t, payload, raw)
}

// 已经是内联的别再取一遍(客户直接传 base64 的场景)。
func TestInlineEnhanceVideosLeavesDataURITouched(t *testing.T) {
	allowLocalFetch(t)
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
	}))
	defer srv.Close()

	in := "data:video/mp4;base64,AAAA"
	require.Equal(t, []string{in}, inlineEnhanceVideos(context.Background(), []string{in}))
	require.False(t, hit, "data URI 不该触发下载")
}

// **取不到就退回原 URL,不能让整次增强换个形态。**
func TestInlineEnhanceVideosFallsBackOnHTTPError(t *testing.T) {
	allowLocalFetch(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	u := srv.URL + "/boom.mp4"
	require.Equal(t, []string{u}, inlineEnhanceVideos(context.Background(), []string{u}))
}

// 非 http(s) 的形态(mm_file:// 之类)只能由上游解析,原样留着。
func TestInlineEnhanceVideosLeavesNonHTTPAlone(t *testing.T) {
	in := "mm_file://abc/clip.mp4"
	require.Equal(t, []string{in}, inlineEnhanceVideos(context.Background(), []string{in}))
}

// **超限不内联。** 判据是内联后的 data URI 长度(发送那一关按它卡),
// 所以原始字节到 3/4 就封顶 —— 否则下完一遍还是会被丢弃。
func TestInlineEnhanceVideosSkipsOversized(t *testing.T) {
	allowLocalFetch(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(make([]byte, maxInlineVideoBytes+1))
	}))
	defer srv.Close()

	u := srv.URL + "/big.mp4"
	require.Equal(t, []string{u}, inlineEnhanceVideos(context.Background(), []string{u}))
}

// 上限之内的视频内联之后必须仍然**通过发送关**。
func TestInlinedVideoPassesSendGuard(t *testing.T) {
	allowLocalFetch(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(make([]byte, maxInlineVideoBytes))
	}))
	defer srv.Close()

	got := inlineEnhanceVideos(context.Background(), []string{srv.URL + "/max.mp4"})
	require.True(t, strings.HasPrefix(got[0], "data:"), "上限之内必须内联")
	require.True(t, enhanceVideoAllowed(got[0]), "内联结果不能又被发送关丢掉")
}

// **端到端:请求体里真的带的是内联视频,不是原始 URL。**
//
// 上面几条测的是内联函数本身;这一条测它有没有接进 EnhancePrompt —— 少接一行,
// 内联就只是个没人调用的工具函数,而现象和没做一样。
func TestEnhanceRequestCarriesInlinedVideo(t *testing.T) {
	allowLocalFetch(t)
	payload := []byte("fake-mp4-payload")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write(payload)
	}))
	defer srv.Close()

	got := fakeSequence(t, chatResponse(scReply("integrated_multimodal_description: [Shot 1] x")))
	in := scInput("一只猫")
	in.VideoURLs = []string{srv.URL + "/clip.mp4"}
	res := EnhancePrompt(context.Background(), scCfg(), "Bearer sk-x", in)
	require.False(t, res.Degraded, res.DegradeReason)

	body := string((*got)[0])
	require.Contains(t, body, "data:video/mp4;base64,")
	require.NotContains(t, body, srv.URL+"/clip.mp4", "引擎不该再看到原 URL")
}

// 关掉 send_input_images 时**不下载**:白下一段字节没有意义。
func TestEnhanceSkipsInliningWhenMediaOff(t *testing.T) {
	allowLocalFetch(t)
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
	}))
	defer srv.Close()

	cfg := scCfg()
	off := false
	cfg.PromptEnhance.SendInputImages = &off

	got := fakeSequence(t, chatResponse(scReply("integrated_multimodal_description: [Shot 1] x")))
	in := scInput("一只猫")
	in.VideoURLs = []string{srv.URL + "/clip.mp4"}
	res := EnhancePrompt(context.Background(), cfg, "Bearer sk-x", in)
	require.False(t, res.Degraded, res.DegradeReason)
	require.False(t, hit, "素材不发送时不该下载")
	require.NotContains(t, string((*got)[0]), "data:video")
}

