package moderation

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service/mediastore"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// 视频抽帧。见 docs/content-moderation-design.md §12.1、§12.2。
//
// 判定本身仍然走图片模型：抽出首/中/尾三帧，各判一次，任一帧违规即拦。
//
// 三帧是知情的覆盖取舍，不是「够用」的技术结论——违规画面只出现在其它位置就会漏。
// 选它是因为这条路在同步路径上，抽帧成本必须有硬上界；首/中/尾覆盖了最常见的
// 构造方式（整段违规、片头片尾插入）。要提高覆盖率就得按时长动态抽帧，那会让
// 时延随视频长度增长，与同步路径冲突。

// ffmpegProtocolWhitelist 允许 ffmpeg 使用的协议。
//
// 这道闸只挡**协议**，挡不住目标地址——这一点必须说清楚，否则很容易误以为
// SSRF 已经防住了。它关掉的是 file:、concat:、subfile: 那半边（任意文件读取）；
// `http://169.254.169.254/...` 或任何内网地址在协议上完全合法，要靠
// validateFetchURL 那道独立的 SSRF 校验拦。两道闸缺一不可。
//
// 本地临时文件那条路（data-url 视频）单独放开 file，见 extractFramesFromDataURL。
const ffmpegProtocolWhitelist = "http,https,tcp,tls"

// validateFetchURL 对用户给的视频地址做 SSRF 校验。
//
// 抽帧让**网关自己**变成了取数方：ffmpeg 在我们的容器里发请求，它能看到的东西
// 远比外部调用者多（云厂商元数据服务、内网 Redis、其它内部端口）。仓库里每一条
// 拉取用户 URL 的路径都先过这道闸——service/http_client.go:27、
// controller/video_proxy.go:150、service/download.go:35——抽帧没有理由例外。
//
// 传 mediastore.OwnOBSHost() 放行我方 OBS：VPC 内 <bucket>.<endpoint> 解析到
// 100.125.x.x（CGNAT 私网段），不放行的话审核会把我们自己卸载上去的图和视频全部拒掉。
// 只放松私网这一条，scheme 与端口仍然强制——与 video_proxy.go:150 的处置一致。
func validateFetchURL(rawURL string) error {
	f := system_setting.GetFetchSetting()
	return common.ValidateURLWithFetchSetting(
		rawURL,
		f.EnableSSRFProtection, f.AllowPrivateIp,
		f.DomainFilterMode, f.IpFilterMode,
		f.DomainList, f.IpList, f.AllowedPorts,
		f.ApplyIPFilterForDomain,
		mediastore.OwnOBSHost(),
	)
}

// frameScaleFilter 抽帧后的缩放。
//
// 判定模型内部会把图缩到 896×896，先缩一道能省下大部分传输与 base64 膨胀的开销，
// 对判定结果没有影响。-2 保持宽高比且让宽度为偶数（png 不要求，但换编码器时不会炸）。
const frameScaleFilter = "scale=896:-2"

const (
	// ffprobeTimeout 探时长的超时。只读文件头，走 range 请求，不该超过几秒。
	ffprobeTimeout = 10 * time.Second
	// frameExtractTimeout 单帧抽取的超时。
	frameExtractTimeout = 20 * time.Second
	// maxFrameBytes 单帧 PNG 的字节上限。
	//
	// 缩放后 896 宽的 PNG 通常在 1 MB 以内，4 MB 是给高噪点画面留的余量。
	// 有上限是因为这段字节要 base64 之后进 HTTP 请求体，无上限等于把一个畸形
	// 视频变成打爆审核节点的手段。
	maxFrameBytes = 4 << 20
)

// ffmpegOnce / ffmpegAvailable ffmpeg 与 ffprobe 是否可用。
//
// 进程内探一次而不是每次调用都探：这是部署形态决定的静态事实，不会在运行中改变。
var (
	ffmpegOnce      sync.Once
	ffmpegAvailable bool
	ffmpegMissing   string
)

// FFmpegAvailable 报告视频抽帧是否可用，供管理端运行态展示。
//
// **这条必须在界面上可见。** 镜像里没装 ffmpeg 时视频审核会整体跳过，而「跳过」
// 如果只写在进程日志里，就是这套系统最不能出的那种错：以为在审，其实没审（§6.5 四）。
func FFmpegAvailable() (bool, string) {
	ffmpegOnce.Do(func() {
		for _, bin := range []string{"ffmpeg", "ffprobe"} {
			if _, err := exec.LookPath(bin); err != nil {
				ffmpegMissing = bin
				common.SysError(fmt.Sprintf(
					"moderation: 未找到 %s，视频审核将被跳过（图片审核不受影响）。"+
						"需要在镜像中安装 ffmpeg，否则用户上传的视频完全不经过内容审核", bin))
				return
			}
		}
		ffmpegAvailable = true
	})
	return ffmpegAvailable, ffmpegMissing
}

// ExtractVideoFrames 抽出首、中、尾三帧，返回它们的 data-url。
//
// 返回的帧数可能少于三：极短视频里三个时间点会落到同一帧，重复送审只是浪费调用。
// 一帧都抽不出来时返回错误，由调用方按 fail-close 处置——「解不开的视频」不等于
// 「没问题的视频」，尤其构造一个解不开的容器本身就是最省事的绕过方式。
func ExtractVideoFrames(ctx context.Context, videoURL string) ([]string, error) {
	if ok, missing := FFmpegAvailable(); !ok {
		return nil, fmt.Errorf("moderation: 缺少 %s，无法抽帧", missing)
	}

	if strings.HasPrefix(videoURL, "data:") {
		return extractFramesFromDataURL(ctx, videoURL)
	}
	if !strings.HasPrefix(videoURL, "http://") && !strings.HasPrefix(videoURL, "https://") {
		// 协议白名单在 ffmpeg 参数里也设了，这里提前挡一道是为了给出能看懂的错误，
		// 而不是让 ffmpeg 报一句 "Protocol not on whitelist"。
		return nil, errors.New("moderation: 视频地址协议不受支持")
	}
	// SSRF 校验必须在起 ffmpeg 之前：一旦进程起来，请求就已经从我们的容器发出去了。
	if err := validateFetchURL(videoURL); err != nil {
		return nil, fmt.Errorf("moderation: 视频地址未通过安全校验: %w", err)
	}
	return extractFrames(ctx, videoURL, ffmpegProtocolWhitelist)
}

// moderationScratchDir 抽帧临时文件在 NFS 上的前缀。
//
// 与 nfsinput 的 `inputs/` 平级而不是塞进去：`inputs/` 下的每个路径都是要交给推理引擎
// 的 input_ref，门面 routes/videos.py 对它有格式校验。审核的临时文件不是输入，
// 混进去会让「这个文件是给谁的」失去唯一答案，清理规则也没法分开配。
const moderationScratchDir = "moderation-scratch"

// scratchRoot 临时文件的落点。
//
// 用运营在媒体存储里配的那个 NFS 路径（MediaStorageSettings.NFSOutputRoot），
// 不新增配置项——它已经是 new-api 与 gpustack 共享的那块盘，且是硬不变量
// （见 nfsinput 包注释：new-api NFSRoot == gpustack lightx2v_output_root）。
//
// NFS 不可用时回退系统临时目录：审核不该因为一个只用来放中间文件的路径没配好就整体
// 失效。回退会记一条日志——容器重启时本地临时文件会随容器一起消失，那是可接受的，
// 而 NFS 上的孤儿文件需要靠定期清理兜底。
func scratchRoot() (string, bool) {
	root := system_setting.GetMediaStorageSettings().NFSRoot()

	// 必须先确认 root **已经存在**，不能直接 MkdirAll 整条路径。
	//
	// NFSRoot() 在没配时默认 /nfs-output，而 MkdirAll 会把缺失的父目录一并创建——
	// 于是 NFS 根本没挂载的机器上这一步会「成功」，把几百 MB 的视频写进容器可写层，
	// 路径看起来还像 NFS。既撑爆容器磁盘，又躲开了 NFS 那边的清理规则，
	// 而下面那条回退日志一次都不会打印。
	//
	// 注意这只挡得住「目录不存在」，挡不住「有人手工 mkdir 了但没挂载」——
	// 后者 nfsinput.ProbeNFSInputs 同样挡不住（它也用 MkdirAll），属于运维前提。
	if fi, err := os.Stat(root); err != nil || !fi.IsDir() {
		common.SysLog("moderation: NFS 根目录不存在（" + root + "），抽帧临时文件回退本地临时目录")
		return "", false
	}

	dir := filepath.Join(root, moderationScratchDir, time.Now().Format("20060102"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		common.SysLog("moderation: NFS 抽帧临时目录不可写，回退本地临时目录: " + err.Error())
		return "", false
	}
	return dir, true
}

// extractFramesFromDataURL 把 data-url 视频落成临时文件再抽帧。
//
// ffmpeg 读不了 data-url。落盘是唯一的路，代价是这条分支必然要把整个视频写一遍——
// §12.2 那条「200 MB 视频不必整包下载」的 range seek 优化对它不成立。
// 好在这是非白名单渠道才会走到的形态：卸载到 OBS 的渠道拿到的已经是 http URL。
func extractFramesFromDataURL(ctx context.Context, dataURL string) ([]string, error) {
	// 走 mediastore.ParseDataURL 而不是自己剥头解码，为的是那个 limit——
	// 它在**解码之前**按 base64 长度预判，超限直接拒绝，不会为了扔掉一个 200 MB
	// 的串先把它解成 150 MB 的字节切片。
	//
	// 这条路是全仓唯一没有上游大小闸的：白名单渠道的 data-url 在
	// relay/task_media_offload.go:112 已经被同一个 limit 卡过，而这里恰恰是
	// **非**白名单渠道才会走到的分支，客户端能一直塞到 MAX_REQUEST_BODY_MB。
	// 不设闸的话，base64 串（整个请求期间都挂在 MediaItem.URL 上）加解码后的字节
	// 会同时驻留，再乘以 mediaItemConcurrency 和并发请求数。
	//
	// 复用 MaxObjectSizeMB 而不是新增配置项：它就是运营已经配好的「单个媒体对象上限」，
	// 审核没有理由比存储更宽松。
	limit := int64(system_setting.GetMediaStorageSettings().MaxObjectSizeMB) * 1024 * 1024
	parsed, err := mediastore.ParseDataURL(dataURL, limit)
	if err != nil {
		return nil, fmt.Errorf("moderation: 视频 data-url 无法解析: %w", err)
	}
	raw := parsed.Data

	dir, _ := scratchRoot() // 空字符串时 CreateTemp 落系统临时目录，正是回退语义
	// 带上扩展名：ffmpeg 主要靠内容嗅探选 demuxer，但少数容器（如 .ts）会参考扩展名，
	// 而 ParseDataURL 已经把它算好了，白给的信息没理由丢掉。
	f, err := os.CreateTemp(dir, "video-*."+parsed.Ext)
	if err != nil {
		return nil, err
	}
	// 主动删而不是只靠定期清理：清理是给进程崩溃留的兜底，不是常规回收路径。
	// 一次提交带一个 200 MB 视频，指望定时任务回收会让盘的水位随流量起伏。
	defer os.Remove(f.Name())
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return nil, err
	}
	f.Close()

	// 这条路才放开 file 协议，且喂给 ffmpeg 的是我们自己刚写下的临时文件路径，
	// 不是用户给的字符串——用户可控的部分只有内容，不是路径。
	return extractFrames(ctx, f.Name(), "file")
}

// httpHardeningArgs 网络输入才需要的额外 ffmpeg 参数。
//
// -max_redirects 0 是 SSRF 校验的另一半：validateFetchURL 只验了**我们看到的**
// 那个地址，而 ffmpeg 默认跟随最多 8 次跳转。不关掉的话，一个指向公网的合法 URL
// 只要 302 到 169.254.169.254 就绕过了全部校验。
//
// 选项名是实测出来的：ffmpeg 8.1 的 http protocol 只有 -max_redirects，
// 没有 -follow_redirects（后者会以 "Option not found" 直接退出，
// 那会让每一次 http 抽帧都失败）。改这里之前先跑一次 ffmpeg -h protocol=http。
//
// 本地文件（data-url 落盘那条路）不能带这个参数——它是 http 协议的选项。
func httpHardeningArgs(protocols string) []string {
	if protocols == "file" {
		return nil
	}
	return []string{"-max_redirects", "0"}
}

func extractFrames(ctx context.Context, input string, protocols string) ([]string, error) {
	duration, err := probeDuration(ctx, input, protocols)
	if err != nil {
		return nil, err
	}

	frames := make([]string, 0, 3)
	seen := make(map[string]bool, 3)
	for _, at := range framePositions(duration) {
		png, err := extractFrame(ctx, input, protocols, at)
		if err != nil {
			// 单帧失败不放弃整段：尾帧在时长探测不准的容器里经常抽空，
			// 那不该让整个视频变成审核失败。只要拿到至少一帧就继续判。
			common.SysLog(fmt.Sprintf("moderation: 抽帧失败（位置 %.2fs）: %v", at, err))
			continue
		}
		// 极短视频里三个位置会落到同一帧，去重省掉重复调用。
		key := common.HashModerationContent(png)
		if seen[key] {
			continue
		}
		seen[key] = true
		frames = append(frames, "data:image/png;base64,"+png)
	}
	if len(frames) == 0 {
		return nil, errors.New("moderation: 未能从视频中抽出任何一帧")
	}
	return frames, nil
}

// framePositions 三个抽帧位置：首、中、尾。
//
// 尾帧取 duration-0.5 而不是 duration：正好落在末尾时 seek 会落到最后一帧之后，
// 抽出来的是空。0.5 秒的回退对「片尾插入违规画面」这个要防的场景没有影响。
func framePositions(duration float64) []float64 {
	if duration <= 0 {
		// 时长探不出来（直播流、损坏的容器头）。只抽首帧——比整体判失败强，
		// 也比假装抽了三帧强。
		return []float64{0}
	}
	last := duration - 0.5
	if last < 0 {
		last = 0
	}
	return []float64{0, duration / 2, last}
}

// probeDuration 探视频时长（秒）。
func probeDuration(ctx context.Context, input string, protocols string) (float64, error) {
	ctx, cancel := context.WithTimeout(ctx, ffprobeTimeout)
	defer cancel()

	args := []string{"-v", "error", "-protocol_whitelist", protocols}
	args = append(args, httpHardeningArgs(protocols)...)
	args = append(args,
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		input,
	)
	cmd := exec.CommandContext(ctx, "ffprobe", args...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("moderation: ffprobe 失败: %w (%s)", err, truncateForError(stderr.String()))
	}
	d, err := strconv.ParseFloat(strings.TrimSpace(out.String()), 64)
	if err != nil {
		// 时长解析不出来不算失败：framePositions 会退化成只抽首帧。
		return 0, nil
	}
	return d, nil
}

// extractFrame 抽取指定位置的一帧，返回 PNG 的 base64。
func extractFrame(ctx context.Context, input string, protocols string, at float64) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, frameExtractTimeout)
	defer cancel()

	// -ss 放在 -i **之前**是 input seeking：ffmpeg 直接跳到目标位置附近开始解码，
	// 对 http 输入会转成 range 请求，只读目标位置附近的几 MB。放在 -i 之后是
	// output seeking——那会从头解码丢弃到目标位置，200 MB 的视频要整包下载，
	// §12.2 把这一点列为视频审核能否留在同步路径上的决定性因素。
	args := []string{"-hide_banner", "-loglevel", "error", "-protocol_whitelist", protocols}
	args = append(args, httpHardeningArgs(protocols)...)
	args = append(args,
		"-ss", strconv.FormatFloat(at, 'f', 3, 64),
		"-i", input,
		"-frames:v", "1",
		"-vf", frameScaleFilter,
		"-f", "image2",
		"-c:v", "png",
		"-",
	)
	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("ffmpeg 失败: %w (%s)", err, truncateForError(stderr.String()))
	}
	if out.Len() == 0 {
		return "", errors.New("ffmpeg 未输出任何数据")
	}
	if out.Len() > maxFrameBytes {
		return "", fmt.Errorf("单帧超过 %d 字节上限", maxFrameBytes)
	}
	return base64.StdEncoding.EncodeToString(out.Bytes()), nil
}
