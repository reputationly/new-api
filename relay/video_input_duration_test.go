package relay

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// mp4Bytes 造一个带 mvhd 的最小 mp4(timescale/duration 决定时长)。
func mp4Bytes(timescale uint32, duration uint32) []byte {
	box := func(typ string, payload []byte) []byte {
		out := make([]byte, 8+len(payload))
		binary.BigEndian.PutUint32(out[0:4], uint32(8+len(payload)))
		copy(out[4:8], typ)
		copy(out[8:], payload)
		return out
	}
	mvhd := make([]byte, 4+16)
	binary.BigEndian.PutUint32(mvhd[12:16], timescale)
	binary.BigEndian.PutUint32(mvhd[16:20], duration)
	return append(box("ftyp", []byte("isom")), box("moov", box("mvhd", mvhd))...)
}

// probeCtx 造一个带用户身份的 gin 上下文 —— 归属校验要用它。
func probeCtx(t *testing.T, userID int) *gin.Context {
	t.Helper()
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	common.SetContextKey(c, constant.ContextKeyUserId, userID)
	c.Set("channel_type", constant.ChannelTypeGPUStackPlus)
	return c
}

// srReq 造一条超分请求(显式 task_type=sr,否则不会触发探测)。
func srReq(video string) *relaycommon.TaskSubmitReq {
	return &relaycommon.TaskSubmitReq{
		Metadata: map[string]any{"task_type": "sr", "video": video},
	}
}

func withNFSRoot(t *testing.T) string {
	t.Helper()
	s := system_setting.GetMediaStorageSettings()
	orig := s.NFSOutputRoot
	t.Cleanup(func() { s.NFSOutputRoot = orig })
	root := t.TempDir()
	s.NFSOutputRoot = root
	return root
}

// 聚合流水线的超分段传的就是 NFS 绝对路径 —— 这是最主要的形态。
// 路径里的 user_id 段必须与调用者一致(归属校验)。
func TestProbeSecondsFromNFSPath(t *testing.T) {
	root := withNFSRoot(t)
	path := filepath.Join(root, "sr-seedvr", "2026", "09", "06", "42", "x.mp4")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, mp4Bytes(600, 4200), 0o644)) // 7 秒

	require.Equal(t, 7, probeInputVideoSeconds(probeCtx(t, 42), srReq(path)))
}

// 不足一秒按一秒:避免"0 秒 = 0 元"这种明显错误的账单。
func TestProbeSecondsRoundsUp(t *testing.T) {
	root := withNFSRoot(t)
	path := filepath.Join(root, "sr-x", "2026", "09", "06", "42", "a.mp4")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, mp4Bytes(1000, 1200), 0o644)) // 1.2 秒

	require.Equal(t, 2, probeInputVideoSeconds(probeCtx(t, 42), srReq(path)))
}

// data-uri / 裸 base64 上传的源视频同样要能探测(不涉及归属校验)。
func TestProbeSecondsFromBase64(t *testing.T) {
	withNFSRoot(t)
	b64 := base64.StdEncoding.EncodeToString(mp4Bytes(30, 900)) // 30 秒

	require.Equal(t, 30, probeInputVideoSeconds(probeCtx(t, 42),
		srReq("data:video/mp4;base64,"+b64)))
}

// **只有输出长度等于输入长度的玩法才能按输入时长计费。**
//
// v2v / mv2v / ads2v 这些编辑类:客户不传 duration 时适配器根本不下发
// target_video_length,输出长度由引擎默认值决定。拿输入时长给它们计费,就会出现
// "60 秒素材、输出 5 秒、按 60 秒收钱"。
func TestProbeSecondsOnlyForLengthPreservingTaskTypes(t *testing.T) {
	root := withNFSRoot(t)
	path := filepath.Join(root, "x-model", "2026", "09", "06", "42", "a.mp4")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, mp4Bytes(10, 600), 0o644)) // 60 秒

	for _, tt := range []string{"sr", "v2a"} {
		t.Run("允许 "+tt, func(t *testing.T) {
			req := &relaycommon.TaskSubmitReq{
				Metadata: map[string]any{"task_type": tt, "video": path},
			}
			require.Equal(t, 60, probeInputVideoSeconds(probeCtx(t, 42), req))
		})
	}
	for _, tt := range []string{"v2v", "mv2v", "ads2v", "rv2v", "i2v", ""} {
		t.Run("拒绝 "+tt, func(t *testing.T) {
			req := &relaycommon.TaskSubmitReq{
				Metadata: map[string]any{"task_type": tt, "video": path},
			}
			require.Zero(t, probeInputVideoSeconds(probeCtx(t, 42), req),
				"输出长度不等于输入长度的玩法不能按输入时长计费")
		})
	}
}

// **归属校验**:别人目录下的文件不能读,哪怕它就在挂载根内。
// 少了这道闸,任何登录用户猜一个路径就能让我们读它、并用它的时长决定账单。
func TestProbeSecondsEnforcesTenantOwnership(t *testing.T) {
	root := withNFSRoot(t)
	// 文件属于用户 99
	path := filepath.Join(root, "sr-x", "2026", "09", "06", "99", "victim.mp4")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, mp4Bytes(600, 6000), 0o644))

	// 调用者是用户 42
	require.Zero(t, probeInputVideoSeconds(probeCtx(t, 42), srReq(path)),
		"不得读取其他租户目录下的文件")
}

// 拿不到调用者身份时一律拒绝 —— 默认必须是"不读"。
func TestProbeSecondsRequiresCallerIdentity(t *testing.T) {
	root := withNFSRoot(t)
	path := filepath.Join(root, "sr-x", "2026", "09", "06", "42", "a.mp4")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, mp4Bytes(600, 6000), 0o644))

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec) // 没有 user id
	require.Zero(t, probeInputVideoSeconds(c, srReq(path)))
}

// 超过合理上限的时长当作探测失败。
//
// mvhd v1 的 duration 是 uint64,配个极小的 timescale 就能算出天文数字,而 Go 的
// float→int 越界行为是 implementation-defined。per_second 又没有结算侧兜底,
// 一个畸形 mp4 就能冻结出荒谬的价钱。钳到上限同样是错价,所以直接回退固定价。
func TestProbeSecondsRejectsAbsurdDuration(t *testing.T) {
	root := withNFSRoot(t)
	path := filepath.Join(root, "sr-x", "2026", "09", "06", "42", "huge.mp4")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	// timescale=1, duration=999999 → 约 11.5 天
	require.NoError(t, os.WriteFile(path, mp4Bytes(1, 999999), 0o644))

	require.Zero(t, probeInputVideoSeconds(probeCtx(t, 42), srReq(path)))
}

// **探测不到一律返回 0**,让整单回退固定价,而不是把请求判失败。
func TestProbeSecondsFallsBackToZero(t *testing.T) {
	withNFSRoot(t)
	cases := map[string]string{
		"文件不存在": "/nonexistent/a.mp4",
		"不是视频":  "just a string",
		"空值":    "",
	}
	for name, video := range cases {
		t.Run(name, func(t *testing.T) {
			require.Zero(t, probeInputVideoSeconds(probeCtx(t, 42), srReq(video)))
		})
	}
	require.Zero(t, probeInputVideoSeconds(probeCtx(t, 42), nil))
}

// 同一次提交里探测只做一次 —— videoBillingSeconds 会被预检与冻结各调一次,
// 不缓存就要重复走两遍 EvalSymlinks + 读盘(或对多 MB 载荷解码两遍)。
func TestProbeSecondsMemoizedPerRequest(t *testing.T) {
	root := withNFSRoot(t)
	path := filepath.Join(root, "sr-x", "2026", "09", "06", "42", "a.mp4")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, mp4Bytes(100, 500), 0o644)) // 5 秒

	c := probeCtx(t, 42)
	req := srReq(path)
	require.Equal(t, 5, probeInputVideoSeconds(c, req))

	// 把文件删掉:若第二次仍去读盘,结果会变成 0。
	require.NoError(t, os.Remove(path))
	require.Equal(t, 5, probeInputVideoSeconds(c, req), "同一请求内应复用首次探测结果")
}

// 计费入口必须真的用上探测结果 —— videoBillingSeconds 才是两处计费共用的口子。
func TestVideoBillingSecondsUsesProbedDuration(t *testing.T) {
	root := withNFSRoot(t)
	path := filepath.Join(root, "sr-x", "2026", "09", "06", "42", "src.mp4")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, mp4Bytes(25, 200), 0o644)) // 8 秒

	require.Equal(t, 8, videoBillingSeconds(probeCtx(t, 42), srReq(path), 0))
}

// 请求显式给了 seconds 时以它为准 —— 探测只是最后一招。
func TestVideoBillingSecondsPrefersExplicit(t *testing.T) {
	root := withNFSRoot(t)
	path := filepath.Join(root, "sr-x", "2026", "09", "06", "42", "src.mp4")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, mp4Bytes(25, 200), 0o644))

	require.Equal(t, 3, videoBillingSeconds(probeCtx(t, 42), srReq(path), 3))
}

// 非自建渠道保持保守口径:不探测,回退旧路径。
func TestVideoBillingSecondsSkipsNonGPUStack(t *testing.T) {
	root := withNFSRoot(t)
	path := filepath.Join(root, "sr-x", "2026", "09", "06", "42", "src.mp4")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, mp4Bytes(25, 200), 0o644))

	c := probeCtx(t, 42)
	c.Set("channel_type", constant.ChannelTypeOpenAI)
	require.Zero(t, videoBillingSeconds(c, srReq(path), 0))
}

// allowTestServer 放开连到本地测试服务器所需的两项:私网 IP 与它的随机高位端口
// (默认端口白名单只有 80/443/8080/8443)。其余 SSRF 校验保持开启。
func allowTestServer(t *testing.T, srvURL string) {
	t.Helper()
	fs := system_setting.GetFetchSetting()
	orig := *fs
	t.Cleanup(func() { *fs = orig })
	fs.AllowPrivateIp = true
	if u, err := url.Parse(srvURL); err == nil && u.Port() != "" {
		fs.AllowedPorts = append(append([]string{}, fs.AllowedPorts...), u.Port())
	}
}

// allowTestServerPortOnly 只放开端口,**保留私网 IP 拦截** —— 用于验证 SSRF 生效。
func allowTestServerPortOnly(t *testing.T, srvURL string) {
	t.Helper()
	fs := system_setting.GetFetchSetting()
	orig := *fs
	t.Cleanup(func() { *fs = orig })
	fs.EnableSSRFProtection = true
	fs.AllowPrivateIp = false
	if u, err := url.Parse(srvURL); err == nil && u.Port() != "" {
		fs.AllowedPorts = append(append([]string{}, fs.AllowedPorts...), u.Port())
	}
}

// 第三方 URL:只拉文件头尾各 1 MB 找 mvhd,不整包下载。
//
// faststart 的 mp4(moov 在头部)是网络分发的常态,一次 Range 就够。
func TestProbeSecondsFromRemoteRangeHead(t *testing.T) {
	withNFSRoot(t)

	full := mp4Bytes(20, 400) // 20 秒,moov 在头部
	var gotRange string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Range")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(full)
	}))
	t.Cleanup(srv.Close)
	allowTestServer(t, srv.URL)

	got := probeInputVideoSeconds(probeCtx(t, 42), srReq(srv.URL+"/a.mp4"))

	require.Equal(t, 20, got)
	require.Contains(t, gotRange, "bytes=0-", "应先用 Range 拉文件头部,而不是整包下载")
}

// moov 在文件尾部(非 faststart)时,靠第二次 Range 拉尾部兜住。
func TestProbeSecondsFromRemoteRangeTail(t *testing.T) {
	withNFSRoot(t)

	// 头部是一大段无关数据,mvhd 只在尾部出现。
	head := bytes.Repeat([]byte{0x00}, 4096)
	tail := mp4Bytes(50, 2500) // 50 秒
	var ranges []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rng := r.Header.Get("Range")
		ranges = append(ranges, rng)
		w.WriteHeader(http.StatusPartialContent)
		if strings.HasPrefix(rng, "bytes=-") {
			_, _ = w.Write(tail)
			return
		}
		_, _ = w.Write(head)
	}))
	t.Cleanup(srv.Close)
	allowTestServer(t, srv.URL)

	got := probeInputVideoSeconds(probeCtx(t, 42), srReq(srv.URL+"/a.mp4"))

	require.Equal(t, 50, got)
	require.Len(t, ranges, 2, "头部找不到 mvhd 时应再试尾部")
	require.Contains(t, ranges[1], "bytes=-")
}

// **SSRF 防护必须生效**:这是唯一一处会按客户给的 URL 发起外连的计费路径。
// 不校验的话,客户填一个内网地址就能借我们的服务器去打自己的内网
// (数据库、云元数据服务、本机管理接口)。
func TestProbeSecondsRemoteBlockedBySSRF(t *testing.T) {
	withNFSRoot(t)

	var reached bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		_, _ = w.Write(mp4Bytes(20, 400))
	}))
	t.Cleanup(srv.Close)
	// 只放开端口,私网 IP 仍然拦 —— 这样测到的就是私网防护本身。
	allowTestServerPortOnly(t, srv.URL)

	got := probeInputVideoSeconds(probeCtx(t, 42), srReq(srv.URL+"/a.mp4"))

	require.Zero(t, got, "被 SSRF 拦下时应回退固定价")
	require.False(t, reached, "请求根本不该发出去")
}

// 归属校验里那道"没有身份就拒绝"必须是**显式**的。
//
// 单看别的用例不够:路径段与调用者不匹配时本来就会拒,遮住了这道检查。
// 这里用一个 user_id 段为 "0" 的路径 —— 缺身份时 GetContextKeyInt 返回的正是 0,
// 少了显式检查就会"匹配成功"并放行。
func TestProbeSecondsRejectsZeroUserIDPath(t *testing.T) {
	root := withNFSRoot(t)
	path := filepath.Join(root, "sr-x", "2026", "09", "06", "0", "a.mp4")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, mp4Bytes(600, 6000), 0o644))

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec) // 无用户身份 → userID 解析为 0
	require.Zero(t, probeInputVideoSeconds(c, srReq(path)),
		"缺身份时不能因为路径段恰好是 0 就放行")
}

// 上游无视 Range、整包返回一个大文件时,读取仍要封顶。
//
// 这条钉住的是"计费路径不把大文件拉进内存"这个性质:mvhd 被放在 1.5 MB 处,
// 超过单次读取上限,于是探测失败、回退固定价 —— 这正是我们想要的结果。
// 去掉 LimitReader 的话,几十 MB 的远程文件会在同步提交路径上被整个读进来。
func TestProbeSecondsRemoteCapsReadSize(t *testing.T) {
	withNFSRoot(t)

	// 1.5 MB 填充 + 末尾才是带 mvhd 的数据。
	big := append(bytes.Repeat([]byte{0x00}, 1_500_000), mp4Bytes(20, 400)...)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 无视 Range,永远整包返回(不少上游就是这么干的)。
		_, _ = w.Write(big)
	}))
	t.Cleanup(srv.Close)
	allowTestServer(t, srv.URL)

	require.Zero(t, probeInputVideoSeconds(probeCtx(t, 42), srReq(srv.URL+"/a.mp4")),
		"单次读取必须封顶;读不到 mvhd 就回退固定价,而不是把整个文件拉进内存")
}

// padMP4 在 mp4 尾部补一个大 free box,把文件撑到指定大小。
// moov 在前,解析照常在 free 之前就命中 mvhd —— 撑大的只是"文件有多大"。
func padMP4(base []byte, total int) []byte {
	pad := total - len(base)
	if pad < 8 {
		return base
	}
	free := make([]byte, pad)
	binary.BigEndian.PutUint32(free[0:4], uint32(pad))
	copy(free[4:8], "free")
	return append(append([]byte{}, base...), free...)
}

func withMaxObjectSizeMB(t *testing.T, mb int) {
	t.Helper()
	s := system_setting.GetMediaStorageSettings()
	orig := s.MaxObjectSizeMB
	t.Cleanup(func() { s.MaxObjectSizeMB = orig })
	s.MaxObjectSizeMB = mb
}

// 重定向不跟随。
//
// 上面那次 SSRF 校验只管住了初始 URL:若 client 无条件跟随 3xx,一个 302 就能把探测
// 引到内网地址。两台服务器都在放行端口上,所以初始校验必过 —— 这里单独验的就是
// "跟不跟随重定向",而不是 SSRF 校验本身。
func TestProbeRemoteDoesNotFollowRedirect(t *testing.T) {
	withNFSRoot(t)

	full := mp4Bytes(20, 400) // 跟过去就能拿到 20 秒
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(full)
	}))
	defer target.Close()

	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer redirector.Close()

	allowTestServer(t, target.URL)
	allowTestServer(t, redirector.URL)

	// 不跟随 → 拿到 302 → 非 2xx → 探测失败 → 0 秒(回退固定价)。
	require.Zero(t, videoBillingSeconds(probeCtx(t, 7), srReq(redirector.URL), 0))
}

// 超过 MaxObjectSizeMB 的内联 base64 不解码。
//
// 载荷本身是**能解析出时长的合法 mp4**,所以这条测试只可能因为大小闸而返回 0 ——
// 拆掉闸门它就会返回 20 秒。
func TestProbeBase64RejectsOversized(t *testing.T) {
	withNFSRoot(t)
	withMaxObjectSizeMB(t, 1)

	big := padMP4(mp4Bytes(20, 400), 2<<20) // 2 MB > 1 MB 上限
	raw := base64.StdEncoding.EncodeToString(big)
	require.Zero(t, videoBillingSeconds(probeCtx(t, 7), srReq(raw), 0))

	// 同一段内容落在限额内时必须照常探到 —— 否则上面的 0 可能来自别的原因。
	withMaxObjectSizeMB(t, 8)
	require.Equal(t, 20, videoBillingSeconds(probeCtx(t, 7), srReq(raw), 0))
}

// 不带 padding 的 base64 要能读。
//
// mediastore.ParseDataURL 会回退 RawStdEncoding,物化侧收得下;计费侧只认 StdEncoding
// 就会静默回退固定价 —— 不报错、少收钱,是最难发现的那类账务偏差。
func TestProbeBase64AcceptsUnpadded(t *testing.T) {
	withNFSRoot(t)

	// 补到 64 字节以上,裸串才过得了 looksLikeBase64Video 的长度预筛。
	payload := padMP4(mp4Bytes(20, 400), 128)
	unpadded := base64.RawStdEncoding.EncodeToString(payload)
	require.NotContains(t, unpadded, "=")

	require.Equal(t, 20, videoBillingSeconds(probeCtx(t, 7), srReq(unpadded), 0),
		"裸 base64 无 padding")
	require.Equal(t, 20, videoBillingSeconds(probeCtx(t, 7),
		srReq("data:video/mp4;base64,"+unpadded), 0), "data-uri 无 padding")
}

// 超分/配乐读不出画幅时,计费行名落到 "*"。
//
// 这两种玩法的输出画幅跟随源视频,客户没有画幅入参,resolution 恒为空;不给兜底行名
// 就会撞上矩阵的空行名守卫,按秒计费配了也永不生效 —— 超分一直只能按次收正因于此。
func TestVideoBillingResolutionWildcardForFollowInputTasks(t *testing.T) {
	cases := []struct {
		name   string
		md     map[string]any
		rawRes string
		want   string
	}{
		{"sr 无画幅 → 兜底行", map[string]any{"task_type": "sr"}, "", "*"},
		{"v2a 无画幅 → 兜底行", map[string]any{"task_type": "v2a"}, "", "*"},
		// 已解析出画幅时不得改写:精确行必须优先于兜底行。
		{"sr 有画幅 → 原样", map[string]any{"task_type": "sr"}, "1080p", "1080p"},
		// 编辑类玩法不在此列:它们的输出长度/画幅由引擎默认值决定,拿兜底价收是猜。
		{"v2v 不兜底", map[string]any{"task_type": "v2v"}, "", ""},
		{"无 task_type 不兜底", map[string]any{}, "", ""},
		{"nil metadata 不兜底", nil, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := videoBillingResolution(&relaycommon.TaskSubmitReq{Metadata: tc.md}, tc.rawRes)
			require.Equal(t, tc.want, got)
		})
	}
	require.Equal(t, "", videoBillingResolution(nil, ""))
}
