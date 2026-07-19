// Package nfsinput 是 gpustackplus 渠道两条链路(图片同步 relay、视频异步 task)
// 共享的 NFS 输入物化工具。见:
//   - gpustack 仓 docs/lightx2v-nfs-input-design.md(§3 路径约定 / §4 门面契约 / §5 new-api 物化)
//   - new-api 仓 docs/gpustackplus-sync-image-backpressure.md
//
// 三种到达形态统一收敛成"写字节到 NFS + 生成相对 input_ref":
//   - base64 / data-uri(JSON)      → 解码 → 写盘
//   - multipart 上传文件(form-data) → 读字节 → 写盘
//   - URL(http/https,JSON)         → new-api 下载(带 SSRF 校验)→ 写盘;下不到 → 400 skip-retry
//
// 两个 adaptor(不同 package,同名 gpustackplus)都 import 本包,唯一物化点,不重复实现。
//
// 路径约定(必须与门面 routes/videos.py 校验完全一致):
//
//	inputs/<task_type>-<sanitizedModel>/YYYY/MM/DD/<user_id>/<gid>-<field>[-<i>].<ext>
//
// 其中 <root> = MediaStorageSettings.NFSRoot()(默认 /nfs-output),
// **必须等于** gpustack lightx2v_output_root(同一块 SFS、同一绝对挂载路径)——硬不变量,
// 见 ProbeNFSInputs 启动探测。new-api 先把全部输入字节写到 <root>/<相对ref>,再 POST 提交。
package nfsinput

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

// Field 输入字段名(与门面 _INPUT_FIELDS 键对齐)。
type Field string

const (
	FieldImage        Field = "image"         // 条件图 / 底图(i2i 支持多图,≤MaxImageRefs)
	FieldLastFrame    Field = "last_frame"    // 尾帧(flf2v),单值
	FieldImageMask    Field = "image_mask"    // 蒙版(带 mask 的 edit),单值
	FieldAudio        Field = "audio"         // 音频(s2v 数字人驱动音频),单值
	FieldVoice        Field = "voice"         // TTS 参考音色(zero-shot 克隆),单值
	FieldEmotionAudio Field = "emotion_audio" // TTS 情感参考音(可选),单值
	// 以下为视频输入(SeedVR2 超分 / VACE 编辑),门面映射见 routes/videos.py _INPUT_FIELDS。
	FieldVideo        Field = "video"          // SeedVR2 源视频(sr),单值
	FieldSrcVideo     Field = "src_video"      // VACE 源视频(vace),单值
	FieldSrcMask      Field = "src_mask"       // VACE 蒙版视频(vace),单值
	FieldSrcRefImages Field = "src_ref_images" // VACE 参考图(vace R2V),支持多图 ≤MaxImageRefs
	// 以下为音乐输入(ACE-Step),门面映射见 routes/videos.py _INPUT_FIELDS。
	FieldReferenceAudio Field = "reference_audio" // ACE-Step 参考音频(cover),单值
	FieldSrcAudio       Field = "src_audio"       // ACE-Step 源音频(repaint),单值
	// 以下为 vLLM-Omni TTS 参考音输入,门面映射 ref_audio→ref_audio_path、
	// ref_audio_2→ref_audio_2_path(引擎侧再转 file:// URI 交给 speech handler)。
	FieldRefAudio  Field = "ref_audio"   // vLLM-Omni 克隆参考音(VoxCPM2/CosyVoice3/MOSS-TTSD),单值
	FieldRefAudio2 Field = "ref_audio_2" // vLLM-Omni 双人对话第二说话人参考音(MOSS-TTSD),单值
	// 以下为 vLLM-Omni SoulX-Singer SVS(歌声合成)集成 preprocess 输入,门面原样映射
	// prompt_audio/target_audio(引擎 extra_args 同名键)。AudioX 视频复用上面的 FieldVideo。
	FieldPromptAudio Field = "prompt_audio" // SoulX 音色参考人声,单值
	FieldTargetAudio Field = "target_audio" // SoulX 目标曲/伴奏,单值
)

const (
	// MaxImageRefs image 维度最多张数(与门面 _MAX_INPUT_IMAGES 对齐)。
	MaxImageRefs = 5
	// downloadTimeout URL 下载超时(new-api 侧,独立于 relay 长超时)。
	downloadTimeout = 30 * time.Second
)

// sanitizeModelRe 除 [A-Za-z0-9._-] 外的字符替换为 '_'(与门面 sanitize 规则一致)。
var sanitizeModelRe = regexp.MustCompile(`[^A-Za-z0-9._-]`)

// SanitizeModel 把上游模型名里非 [A-Za-z0-9._-] 的字符替换为 '_'。
func SanitizeModel(model string) string {
	s := sanitizeModelRe.ReplaceAllString(model, "_")
	if s == "" {
		return "_"
	}
	return s
}

// Materializer 一次请求内的物化上下文;所有输入落到同一 <task_type>/日期/<user_id>/<gid> 前缀下。
type Materializer struct {
	root      string // NFSRoot(去尾斜杠)
	taskType  string // t2i|i2i|t2v|i2v|flf2v|s2v
	model     string // sanitized model
	userID    string // new-api 终端用户 id(= 提交体 user_id;门面校验 parent_dir_name == user_id)
	gid       string // 唯一 input-group id(PublicTaskID 或新 uuid)
	dateParts string // YYYY/MM/DD(UTC)

	refs     map[Field][]string // 已生成的相对 ref,按 field 归组
	written  []string           // 已写盘的绝对路径,供失败时回滚(§N2 复审)
	maxBytes int64              // 单文件字节上限(0=不限;用于参考音大小兜底,防直连绕过前端限制)
}

// SetMaxBytes 设置单文件字节上限(0=不限)。返回自身便于链式。
func (m *Materializer) SetMaxBytes(n int64) *Materializer {
	m.maxBytes = n
	return m
}

// NewMaterializer 构造物化上下文。userID / gid 不得为空;gid 传 info.PublicTaskID,空则由调用方传新 uuid。
// taskType 也过一次 sanitize:它可能来自 metadata,含 '/'、'..' 时会让写盘路径逃出
// <root>/inputs(门面校验在 new-api 写盘之后,救不了写盘侧),故与 model 同规则收敛。
func NewMaterializer(taskType, model, userID, gid string) *Materializer {
	now := time.Now().UTC()
	return &Materializer{
		root:      system_setting.GetMediaStorageSettings().NFSRoot(),
		taskType:  SanitizeModel(taskType),
		model:     SanitizeModel(model),
		userID:    userID,
		gid:       gid,
		dateParts: fmt.Sprintf("%04d/%02d/%02d", now.Year(), int(now.Month()), now.Day()),
		refs:      make(map[Field][]string),
	}
}

// relRef 按 §3 约定拼相对路径;多值 image 带 -<i> 后缀,单值字段无后缀。
func (m *Materializer) relRef(field Field, index int, multi bool, ext string) string {
	name := fmt.Sprintf("%s-%s", m.gid, field)
	if multi {
		name = fmt.Sprintf("%s-%s-%d", m.gid, field, index)
	}
	dir := fmt.Sprintf("inputs/%s-%s/%s/%s", m.taskType, m.model, m.dateParts, m.userID)
	return dir + "/" + name + ext
}

// writeBytes 把字节写到 <root>/<ref>,自建父目录;记录已写盘绝对路径供回滚。
func (m *Materializer) writeBytes(ref string, data []byte) error {
	abs := filepath.Join(m.root, filepath.FromSlash(ref))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return fmt.Errorf("创建 NFS 输入目录失败: %w", err)
	}
	if err := os.WriteFile(abs, data, 0o644); err != nil {
		return fmt.Errorf("写 NFS 输入文件失败: %w", err)
	}
	m.written = append(m.written, abs)
	return nil
}

// Cleanup best-effort 删除本次已写盘的输入文件(§N2 复审):多输入请求中某一路
// 物化失败时,调用方在返回 400 前调用它,避免把已写的前几张留成孤儿(janitor 的
// TTL 仍是最终兜底,这里只是即时回滚,更干净)。
func (m *Materializer) Cleanup() {
	for _, p := range m.written {
		_ = os.Remove(p)
	}
	m.written = nil
}

// extForField 默认扩展名:视频类(sr 源视频 / VACE src_video·src_mask).mp4,
// 音频类(s2v audio / TTS voice / 情感音).wav,其余(image / VACE 参考图).png。
func extForField(field Field) string {
	switch field {
	case FieldVideo, FieldSrcVideo, FieldSrcMask:
		return ".mp4"
	case FieldAudio, FieldVoice, FieldEmotionAudio, FieldReferenceAudio, FieldSrcAudio,
		FieldRefAudio, FieldRefAudio2, FieldPromptAudio, FieldTargetAudio:
		return ".wav"
	default:
		return ".png"
	}
}

// extForData 从 data-uri 的 MIME 推导真实扩展名(保留上传的实际格式,如 .mp3/.mov/.jpg),
// 避免把 mp3 存成 .wav、mov 存成 .mp4 误导下游按扩展名/容器识别的引擎。无法识别或非
// data-uri 时返回 "",由调用方回退到字段默认扩展名(extForField)。白名单输出,不引入
// 任意后缀。
func extForData(raw string) string {
	if !strings.HasPrefix(raw, "data:") {
		return ""
	}
	end := strings.IndexAny(raw, ";,")
	if end <= len("data:") {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(raw[len("data:"):end])) {
	case "audio/wav", "audio/x-wav", "audio/wave":
		return ".wav"
	case "audio/mpeg", "audio/mp3":
		return ".mp3"
	case "audio/mp4", "audio/x-m4a", "audio/aac":
		return ".m4a"
	case "audio/ogg", "audio/opus":
		return ".ogg"
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "video/webm":
		return ".webm"
	case "image/png":
		return ".png"
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	}
	return ""
}

// —— 文件头(magic-bytes)嗅探:按 field 的媒体类别校验上传内容,挡住"改后缀"(如把
// .txt/.jpg 改成 .mp4 上传)。只看头几个字节,识别常见容器即可;通不过即拒。内容按容器
// 解码是引擎的事,这里只做廉价前置过滤,省掉一次白派发。

func magicHasPrefix(b []byte, sig ...byte) bool {
	if len(b) < len(sig) {
		return false
	}
	for i, c := range sig {
		if b[i] != c {
			return false
		}
	}
	return true
}

func magicHasAt(b []byte, off int, s string) bool {
	return len(b) >= off+len(s) && string(b[off:off+len(s)]) == s
}

func isImageBytes(b []byte) bool {
	return magicHasPrefix(b, 0x89, 0x50, 0x4E, 0x47) || // png
		magicHasPrefix(b, 0xFF, 0xD8, 0xFF) || // jpg
		(magicHasAt(b, 0, "RIFF") && magicHasAt(b, 8, "WEBP")) || // webp
		magicHasAt(b, 0, "GIF8") // gif
}

func isVideoBytes(b []byte) bool {
	return magicHasAt(b, 4, "ftyp") || // mp4/mov/m4v(ISO BMFF)
		magicHasPrefix(b, 0x1A, 0x45, 0xDF, 0xA3) || // webm/mkv(EBML)
		(magicHasAt(b, 0, "RIFF") && magicHasAt(b, 8, "AVI ")) || // avi
		magicHasAt(b, 0, "FLV") || // flv
		magicHasPrefix(b, 0x00, 0x00, 0x01, 0xBA) || // mpeg-ps
		magicHasPrefix(b, 0x00, 0x00, 0x01, 0xB3) // mpeg-es
}

func isAudioBytes(b []byte) bool {
	return (magicHasAt(b, 0, "RIFF") && magicHasAt(b, 8, "WAVE")) || // wav
		magicHasAt(b, 0, "ID3") || // mp3(带 ID3 标签)
		(len(b) >= 2 && b[0] == 0xFF && (b[1]&0xE0) == 0xE0) || // mp3/aac 帧同步
		magicHasAt(b, 0, "OggS") || // ogg/opus
		magicHasAt(b, 0, "fLaC") || // flac
		magicHasAt(b, 4, "ftyp") // m4a/aac(ISO 容器)
}

// magicOK 校验 data 的文件头是否匹配 field 的媒体类别。未知 field 放行。
func magicOK(field Field, data []byte) bool {
	switch field {
	case FieldImage, FieldLastFrame, FieldImageMask, FieldSrcRefImages:
		return isImageBytes(data)
	case FieldAudio, FieldVoice, FieldEmotionAudio, FieldReferenceAudio, FieldSrcAudio,
		FieldRefAudio, FieldRefAudio2, FieldPromptAudio, FieldTargetAudio:
		return isAudioBytes(data)
	case FieldVideo, FieldSrcVideo, FieldSrcMask:
		return isVideoBytes(data)
	}
	return true
}

// AddBytes 直接写一段字节(multipart 上传文件字节走这里),用字段默认扩展名。
func (m *Materializer) AddBytes(field Field, index int, multi bool, data []byte) error {
	return m.addBytesExt(field, index, multi, data, "")
}

// addBytesExt 写字节,ext 为空时回退字段默认扩展名。
func (m *Materializer) addBytesExt(field Field, index int, multi bool, data []byte, ext string) error {
	if len(data) == 0 {
		return fmt.Errorf("输入 %s 字节为空", field)
	}
	if m.maxBytes > 0 && int64(len(data)) > m.maxBytes {
		return fmt.Errorf("输入 %s 超过大小上限 %d MB", field, m.maxBytes/1024/1024)
	}
	// 文件头校验:挡住改后缀的非媒体文件(如 .txt 改 .mp4)。内容能否被容器解码交给引擎,
	// 这里只做廉价前置过滤,省一次白派发。
	if !magicOK(field, data) {
		return fmt.Errorf("输入 %s 不是有效的媒体文件(文件头校验未通过,请勿改后缀上传)", field)
	}
	if ext == "" {
		ext = extForField(field)
	}
	ref := m.relRef(field, index, multi, ext)
	if err := m.writeBytes(ref, data); err != nil {
		return err
	}
	m.refs[field] = append(m.refs[field], ref)
	return nil
}

// AddString 处理一个字符串输入:data-uri / 裸 base64 → 解码写;http(s) URL → 下载写;
// task:<task_id> → 引用同用户已成功任务的产物(NFS 直读优先,OBS 退化,见 taskref.go)。
func (m *Materializer) AddString(ctx context.Context, field Field, index int, multi bool, raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fmt.Errorf("输入 %s 为空字符串", field)
	}
	if strings.HasPrefix(raw, TaskRefScheme) {
		data, ext, err := m.resolveTaskRef(ctx, raw)
		if err != nil {
			return fmt.Errorf("输入 %s 解析任务引用失败: %w", field, err)
		}
		// 保留产物真实扩展名(如 ACE-Step .mp3):下游引擎按扩展名识别容器,ext 为空回退字段默认
		return m.addBytesExt(field, index, multi, data, ext)
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		data, err := downloadURL(ctx, raw, m.maxBytes)
		if err != nil {
			return err
		}
		return m.AddBytes(field, index, multi, data)
	}
	// data-uri 或裸 base64。data-uri 时保留 MIME 推导的真实扩展名。
	ext := extForData(raw)
	b64 := raw
	if strings.HasPrefix(raw, "data:") {
		if i := strings.Index(raw, ","); i >= 0 {
			b64 = raw[i+1:]
		}
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return fmt.Errorf("输入 %s 既非 http(s) URL 也非合法 base64/data-uri: %w", field, err)
	}
	return m.addBytesExt(field, index, multi, data, ext)
}

// AddMultipartFile 读 multipart 上传文件字节并写盘。
func (m *Materializer) AddMultipartFile(field Field, index int, multi bool, fh *multipart.FileHeader) error {
	f, err := fh.Open()
	if err != nil {
		return fmt.Errorf("打开上传文件失败: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("读取上传文件失败: %w", err)
	}
	return m.AddBytes(field, index, multi, data)
}

// Refs 返回 input_refs(field → 相对路径数组)。用于放进提交体的 "input_refs" 字段。
// 空则返回 nil(t2i/t2v 无输入,整机制不触发)。
func (m *Materializer) Refs() map[string][]string {
	if len(m.refs) == 0 {
		return nil
	}
	out := make(map[string][]string, len(m.refs))
	for k, v := range m.refs {
		out[string(k)] = v
	}
	return out
}

// downloadURL 带 SSRF 校验地下载一个远程 URL 到字节(复用项目统一 fetch 设置 + OBS host 白名单)。
// new-api 够不到(SSRF 拒绝 / 网络失败 / 非 2xx)→ 返回错误,调用方转 400 skip-retry,不提交任务。
// capBytes 为调用方的 per-file 上限(0=不限);实际下载上限取它与全局 MaxObjectSizeMB 的较小正值,
// 用 io.LimitReader 边下边限,避免超限 URL 被整块拉进内存后才拒(§per-model 护栏对 URL 生效)。
// trustedHosts 为额外授信的私网 host(如自家 OBS,task: 引用退化下载时传入,与
// controller/video_proxy.go 的 OwnOBSHost 放行同精神);仅放松私网解析这一条,scheme/端口仍强制。
func downloadURL(ctx context.Context, rawURL string, capBytes int64, trustedHosts ...string) ([]byte, error) {
	return downloadURLWithHeader(ctx, rawURL, capBytes, nil, "", trustedHosts...)
}

// downloadURLWithHeader 同 downloadURL,附加请求头(如取上游渠道产物需要的
// Authorization: Bearer <channel-key>,见 ResolveTaskRefBytes 的代理 URL 分支)与可选
// 渠道代理(proxy 为空则直连;非空复用渠道配置的代理 transport,与 VideoProxy 一致)。
func downloadURLWithHeader(ctx context.Context, rawURL string, capBytes int64, header http.Header, proxy string, trustedHosts ...string) ([]byte, error) {
	s := system_setting.GetMediaStorageSettings()
	fs := system_setting.GetFetchSetting()
	if err := common.ValidateURLWithFetchSetting(rawURL,
		fs.EnableSSRFProtection, fs.AllowPrivateIp, fs.DomainFilterMode, fs.IpFilterMode,
		fs.DomainList, fs.IpList, fs.AllowedPorts, fs.ApplyIPFilterForDomain, trustedHosts...); err != nil {
		return nil, fmt.Errorf("输入 URL 被安全策略拒绝: %w", err)
	}
	// 可选 host 白名单(与 OBS 上游下载共用配置);留空即不额外限制。授信 host 一并放行。
	if hosts := s.AllowedUpstreamHosts(); len(hosts) > 0 {
		if err := hostAllowed(rawURL, append(append([]string{}, hosts...), trustedHosts...)); err != nil {
			return nil, err
		}
	}

	// 禁止自动跟随重定向:SSRF/白名单只校验了原始 URL,若跟随 3xx 跳到内网地址
	// (如 169.254.169.254 云元数据)就绕过了校验。返回 ErrUseLastResponse 让 client
	// 不跟随,3xx 会被下面的状态码检查当作下载失败 → 400 skip-retry。
	client := &http.Client{
		Timeout: downloadTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	// 复用渠道配置的代理 transport(如 OpenAI/Sora 需科学上网),但保留自己的
	// CheckRedirect(防 SSRF 重定向)与 Timeout,不改动 service 缓存的共享 client。
	if proxy != "" {
		pc, perr := service.GetHttpClientWithProxy(proxy)
		if perr != nil {
			return nil, fmt.Errorf("构造代理下载客户端失败: %w", perr)
		}
		client.Transport = pc.Transport
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构造下载请求失败: %w", err)
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载输入 URL 失败(new-api 够不到): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("下载输入 URL 失败: 上游返回 %d", resp.StatusCode)
	}
	// 实际上限 = 全局 MaxObjectSizeMB 与 per-file capBytes 的较小正值(0=不限)。
	limit := int64(s.MaxObjectSizeMB) * 1024 * 1024
	if capBytes > 0 && (limit <= 0 || capBytes < limit) {
		limit = capBytes
	}
	var reader io.Reader = resp.Body
	if limit > 0 {
		reader = io.LimitReader(resp.Body, limit+1)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("读取下载内容失败: %w", err)
	}
	if limit > 0 && int64(len(data)) > limit {
		return nil, fmt.Errorf("输入 URL 内容超过大小上限 %d MB", limit/1024/1024)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("下载输入 URL 得到空内容")
	}
	return data, nil
}

// hostAllowed 简单 host 白名单(精确或 .suffix 子域匹配)。
func hostAllowed(rawURL string, hosts []string) error {
	i := strings.Index(rawURL, "://")
	rest := rawURL
	if i >= 0 {
		rest = rawURL[i+3:]
	}
	if j := strings.IndexAny(rest, "/?#"); j >= 0 {
		rest = rest[:j]
	}
	host := rest
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	if c := strings.LastIndex(host, ":"); c >= 0 {
		host = host[:c]
	}
	host = strings.ToLower(strings.TrimSpace(host))
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			continue
		}
		if host == h || strings.HasSuffix(host, "."+h) {
			return nil
		}
	}
	return fmt.Errorf("输入 URL host %q 不在允许列表内", host)
}

// ProbeNFSInputs 启动探测:<root>/inputs/ 可创建 + 可写 + 可读(写临时探针 → 读回 → 删)。
// 失败返回错误,调用方应 fatal + 打清日志:NFS 未挂载 / 不可写 / root 与 gpustack output_root 不一致。
// 成功时把解析出的 root 打进日志,便于人工核对两侧一致(硬不变量:new-api NFSRoot == gpustack lightx2v_output_root)。
func ProbeNFSInputs() error {
	root := system_setting.GetMediaStorageSettings().NFSRoot()
	inputsDir := filepath.Join(root, "inputs")
	if err := os.MkdirAll(inputsDir, 0o755); err != nil {
		return fmt.Errorf("NFS 输入目录不可创建(NFS 未挂载/不可写?root=%s): %w", root, err)
	}
	probe := filepath.Join(inputsDir, fmt.Sprintf(".nfs_probe_%d_%s", time.Now().UnixNano(), common.GetUUID()))
	want := []byte("nfs-probe")
	if err := os.WriteFile(probe, want, 0o644); err != nil {
		return fmt.Errorf("NFS 输入目录不可写(root=%s): %w", root, err)
	}
	defer os.Remove(probe)
	got, err := os.ReadFile(probe)
	if err != nil {
		return fmt.Errorf("NFS 输入探针不可读(root=%s): %w", root, err)
	}
	if string(got) != string(want) {
		return fmt.Errorf("NFS 输入探针读回内容不一致(root=%s):可能挂载异常", root)
	}
	return nil
}
