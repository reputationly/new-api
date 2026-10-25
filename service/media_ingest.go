package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service/mediastore"
	"github.com/QuantumNous/new-api/service/watermark"
	"github.com/QuantumNous/new-api/setting/system_setting"
)

type watermarkTarget struct {
	Group    string
	Source   string
	UserId   int
	TaskId   string
	KindHint system_setting.MediaKind
}

var (
	persistObject          = mediastore.Persist
	applyWatermark         = watermark.Apply
	watermarkAvailable     = watermark.Available
	recordWatermarkFailure = model.RecordWatermarkFailure
)

// PersistTaskResultToOBS 把一个任务成功产物搬到 OBS，成功返回落库占位符 obs://<key> 与 true；
// 总开关关闭 / 非可搬来源 / 失败均返回 ("", false)，由调用方回退到老行为（§5.1/§5.2）。绝不阻塞任务返回。
//
// 两类来源，优先级从高到低：
//  1. nfsPath —— adaptor 显式给出的成品绝对路径（自建模型，GPUStack/LightX2V），须在 NFSOutputRoot 之下；
//  2. rawURL —— 上游临时 URL（第三方渠道，下载后落盘）；或历史上把 nfs 路径塞进 Url 的启发式兜底。
func PersistTaskResultToOBS(ctx context.Context, task *model.Task, nfsPath, rawURL string) (string, bool) {
	if !mediastore.Enabled() || task == nil {
		return "", false
	}
	s := system_setting.GetMediaStorageSettings()
	root := s.NFSRoot()

	// 1) 显式 nfs_path（最规范：无需猜）。
	if nfsPath != "" && s.IngestNFSPath && isUnderRoot(root, nfsPath) {
		key := mediastore.KeyFromNFSPath(root, nfsPath)
		if ok := persistToOBS(ctx, task, key, mediastore.PersistSource{NFSPath: nfsPath}); ok {
			return mediastore.WrapKey(key), true
		}
		return "", false
	}

	// 2) rawURL：nfs 路径塞进 Url 的启发式兜底 / 第三方上游 URL。
	if rawURL != "" {
		switch {
		case s.IngestNFSPath && isUnderRoot(root, rawURL):
			key := mediastore.KeyFromNFSPath(root, rawURL)
			if ok := persistToOBS(ctx, task, key, mediastore.PersistSource{NFSPath: rawURL}); ok {
				return mediastore.WrapKey(key), true
			}
		case s.IngestUpstreamURL && !channelPassThroughResultURL(task.ChannelId) &&
			(strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://")):
			key := buildTaskObjectKey(task, rawURL)
			if key != "" {
				if ok := persistToOBS(ctx, task, key, mediastore.PersistSource{UpstreamURL: rawURL}); ok {
					return mediastore.WrapKey(key), true
				}
			}
		}
	}
	return "", false
}

// channelPassThroughResultURL 该渠道是否配置了「成品 URL 直接透传，不搬 OBS」。
//
// 只对 rawURL 这一支生效，nfsPath 那一支不看它：自建模型的成品只在 SFS 上，
// 不落盘就没有任何对外可用的 URL，透传无从谈起（task_polling.go 会判失败退款）。
//
// 查不到渠道时返回 false —— 保持搬 OBS 的既有行为。透传是「少做一件事」，
// 出错时宁可多做（成品能长期访问），不能因为一次查询失败就把成品的生命周期缩到 24 小时。
func channelPassThroughResultURL(channelId int) bool {
	if channelId <= 0 {
		return false
	}
	channel, err := model.CacheGetChannel(channelId)
	if err != nil || channel == nil {
		return false
	}
	return channel.GetSetting().PassThroughResultURL
}

// isUnderRoot 判断 p 是否落在 root 目录之下（简单前缀，最终越权校验在 mediastore.ValidateNFSPath）。
func isUnderRoot(root, p string) bool {
	return strings.HasPrefix(p, strings.TrimRight(root, "/")+"/")
}

// persistToOBS 执行落盘并写审计 metadata，失败记日志。
func persistToOBS(ctx context.Context, task *model.Task, key string, src mediastore.PersistSource) bool {
	meta := map[string]string{
		"user-id":  strconv.Itoa(task.UserId),
		"task-id":  task.TaskID,
		"model":    task.Properties.OriginModelName,
		"platform": string(task.Platform),
	}
	var kindHint system_setting.MediaKind
	if constant.IsImageTaskAction(task.Action) {
		kindHint = system_setting.MediaKindImage
	} else if constant.IsVideoTaskAction(task.Action) {
		kindHint = system_setting.MediaKindVideo
	}
	if err := persistMedia(ctx, key, src, meta, watermarkTarget{
		Group: task.Group, Source: "task", UserId: task.UserId, TaskId: task.TaskID, KindHint: kindHint,
	}); err != nil {
		common.SysError("mediastore: persist task result failed, fallback to raw url: " + err.Error())
		return false
	}
	return true
}

func persistMedia(ctx context.Context, key string, src mediastore.PersistSource, meta map[string]string, target watermarkTarget) error {
	settings := system_setting.GetWatermarkSettings()
	ext := strings.TrimPrefix(strings.ToLower(path.Ext(key)), ".")
	kind, recognized := watermark.KindFromExt(ext)
	if !recognized {
		kind = target.KindHint
	}
	if !settings.Enabled || kind == "" {
		return persistObject(ctx, key, src, meta)
	}
	tpl := settings.ResolveTemplate(target.Group, kind)
	if tpl == nil {
		return persistObject(ctx, key, src, meta)
	}
	if ready, reason := watermarkAvailable(); !ready {
		err := fmt.Errorf("watermark unavailable: %s", reason)
		recordWatermarkFailure(target.Source, string(kind), target.Group, target.UserId, target.TaskId, key, err)
		return persistObject(ctx, key, src, meta)
	}

	tempDir, err := os.MkdirTemp("", "watermark-ingest-*")
	if err != nil {
		recordWatermarkFailure(target.Source, string(kind), target.Group, target.UserId, target.TaskId, key, err)
		return persistObject(ctx, key, src, meta)
	}
	defer os.RemoveAll(tempDir)
	inPath, cleanup, err := localizePersistSource(ctx, src, tempDir)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		if src.UpstreamURL != "" {
			// 上游下载失败不是水印故障，按普通落盘失败处理，不计入水印告警。
			return err
		}
		recordWatermarkFailure(target.Source, string(kind), target.Group, target.UserId, target.TaskId, key, err)
		return persistObject(ctx, key, src, meta)
	}
	format := ext
	if format == "" {
		if kind == system_setting.MediaKindImage {
			format = "png"
		} else {
			format = "mp4"
		}
	}
	outPath := filepath.Join(tempDir, "watermarked."+format)
	if err := applyWatermark(ctx, inPath, outPath, kind, format, *tpl); err != nil {
		recordWatermarkFailure(target.Source, string(kind), target.Group, target.UserId, target.TaskId, key, err)
		return persistObject(ctx, key, mediastore.PersistSource{LocalPath: inPath, ContentType: src.ContentType}, meta)
	}
	originalMeta := meta
	if err := persistObject(ctx, mediastore.OriginalKey(key), mediastore.PersistSource{LocalPath: inPath, ContentType: src.ContentType}, originalMeta); err != nil {
		return err
	}
	watermarkedMeta := make(map[string]string, len(meta)+1)
	for k, v := range meta {
		watermarkedMeta[k] = v
	}
	watermarkedMeta["watermark"] = "1"
	contentType := mediastore.InferContentType(key)
	if ext == "" {
		if kind == system_setting.MediaKindImage {
			contentType = "image/png"
		} else {
			contentType = "video/mp4"
		}
	}
	return persistObject(ctx, key, mediastore.PersistSource{LocalPath: outPath, ContentType: contentType}, watermarkedMeta)
}

func localizePersistSource(ctx context.Context, src mediastore.PersistSource, tempDir string) (string, func(), error) {
	switch {
	case src.NFSPath != "":
		path, err := mediastore.ValidateNFSPath(mediastore.NFSRoot(), src.NFSPath)
		return path, nil, err
	case src.UpstreamURL != "":
		file, err := mediastore.DownloadToTemp(ctx, src.UpstreamURL)
		if err != nil {
			return "", nil, err
		}
		return file.Name(), func() {
			name := file.Name()
			_ = file.Close()
			_ = os.Remove(name)
		}, nil
	case len(src.Data) > 0:
		file, err := os.CreateTemp(tempDir, "source-*")
		if err != nil {
			return "", nil, err
		}
		_, writeErr := file.Write(src.Data)
		closeErr := file.Close()
		if writeErr != nil {
			return "", nil, writeErr
		}
		if closeErr != nil {
			return "", nil, closeErr
		}
		return file.Name(), nil, nil
	case src.LocalPath != "":
		return src.LocalPath, nil, nil
	default:
		return "", nil, mediastore.ErrInvalidSource
	}
}

// ResolveResultURL 序列化层统一 hook（§5.4）：obs:// 占位符 → 实时签名 URL；
// 其它原样返回。供 relay / controller 各读取入口复用。
func ResolveResultURL(ctx context.Context, raw string) string {
	return mediastore.ResolveResultURL(ctx, raw)
}

// PersistImageNFSToOBS 把一张成品图（nfs_path）落 OBS 并返回实时签名 URL 与对象 key。
// 用于同步生图链路（/v1/images/generations，当场返回 URL）。
// 总开关关闭 / 路径非法 / 落盘失败均返回错误——同步链路必须拿到可访问 URL，不做静默降级。
//
// key 一并回吐是给同步任务留档用的：签名 URL 有效期有限、不能存库，任务表存的是
// obs://<key> 占位符，查询时实时签发（与异步链路同一套，见 relay/image_task_response.go）。
func PersistImageNFSToOBS(ctx context.Context, userID int, group, nfsPath string) (signedURL string, objectKey string, err error) {
	if !mediastore.Enabled() {
		return "", "", fmt.Errorf("media storage 未启用，无法对外提供生图结果 URL")
	}
	s := system_setting.GetMediaStorageSettings()
	if !s.IngestNFSPath {
		return "", "", fmt.Errorf("nfs_path 落盘已关闭")
	}
	root := s.NFSRoot()
	if !isUnderRoot(root, nfsPath) {
		return "", "", fmt.Errorf("nfs_path %q 不在挂载根 %q 之下", nfsPath, root)
	}
	key := mediastore.KeyFromNFSPath(root, nfsPath)
	meta := map[string]string{
		"user-id": strconv.Itoa(userID),
	}
	if err := persistMedia(ctx, key, mediastore.PersistSource{NFSPath: nfsPath}, meta, watermarkTarget{
		Group: group, Source: "sync_image", UserId: userID, KindHint: system_setting.MediaKindImage,
	}); err != nil {
		return "", "", fmt.Errorf("落盘 OBS 失败: %w", err)
	}
	signed, err := mediastore.Sign(ctx, key)
	if err != nil {
		return "", "", err
	}
	return signed, key, nil
}

// RewriteImageResponseToOBS 把一份 OpenAI 图片响应里的第三方结果统一搬到 OBS（§一、目标）。
// 逐项 best-effort：url 下载落盘 / b64_json 解码落盘 → 换成我方 OBS 签名 URL。
// 「只有 OBS 不可用时才降级透传」——单项落盘失败则保留该项原始 url（或原 b64），不整体失败。
// 已经是我方 OBS 的 url（如自建 gpustackplus 渠道产出）跳过，不重复搬运。
//
// responseFormat 为客户端请求的 response_format：显式要 b64_json 时只在内存中尝试加水印，
// 不上传 OBS，保持客户端期望的响应形态。
//
// 改写走 map[string]json.RawMessage 外科手术式替换，只动 data[i].url / data[i].b64_json
// 两个字段——顶层 usage、逐项 revised_prompt 等未建模字段原样保留，不因重编码丢失。
// 返回改写后的响应体；若总开关关闭 / 无可搬项 / 解析失败，原样返回入参。
//
// 第二个返回值是本次落盘的对象 key（按 data[] 顺序），供同步链路把结果留档进任务表；
// 未落盘的项（透传 / 已是我方 OBS / b64 直通 / 落盘失败）不在其中，所以它可能比 data[] 短，
// 甚至为空 —— 调用方必须容忍「有响应但没有 key」。
func RewriteImageResponseToOBS(ctx context.Context, userID, channelID int, group, modelName, responseFormat string, body []byte) ([]byte, []string) {
	if !mediastore.Enabled() || len(body) == 0 {
		return body, nil
	}
	wantB64 := strings.EqualFold(responseFormat, "b64_json")
	// 渠道配了透传：上游给的是公网可直达的 URL，原样交给客户端，不中转。
	// **只挡 url 分支**——b64 项没有 URL 可透传，不落盘就只能把 base64 原样吐回去。
	passThroughURL := channelPassThroughResultURL(channelID)

	var envelope map[string]json.RawMessage
	if err := common.Unmarshal(body, &envelope); err != nil || len(envelope["data"]) == 0 {
		return body, nil
	}
	var items []map[string]json.RawMessage
	if err := common.Unmarshal(envelope["data"], &items); err != nil || len(items) == 0 {
		return body, nil
	}

	changed := false
	var keys []string
	for _, item := range items {
		urlStr := jsonRawString(item["url"])
		b64Str := jsonRawString(item["b64_json"])
		switch {
		case urlStr != "":
			if passThroughURL {
				continue // 渠道配了透传：保留上游原始 url
			}
			if mediastore.IsOwnOBSURL(urlStr) {
				continue // 已是我方 OBS，跳过
			}
			signed, key, err := persistImageToOBS(ctx, userID, group, modelName, mediastore.PersistSource{UpstreamURL: urlStr}, extFromURL(urlStr))
			if err != nil {
				common.SysError("mediastore: rewrite image url failed, keep upstream url: " + err.Error())
				continue // OBS 不可用 → 降级保留上游 url
			}
			setJSONRawString(item, "url", signed)
			delete(item, "b64_json")
			keys = append(keys, key)
			changed = true
		case b64Str != "":
			if wantB64 {
				if updated, changedB64 := watermarkB64Image(ctx, b64Str, userID, group); changedB64 {
					setJSONRawString(item, "b64_json", updated)
					changed = true
				}
				continue
			}
			raw, decErr := base64.StdEncoding.DecodeString(b64Str)
			if decErr != nil {
				continue
			}
			signed, key, err := persistImageToOBS(ctx, userID, group, modelName, mediastore.PersistSource{Data: raw}, "png")
			if err != nil {
				common.SysError("mediastore: rewrite image b64 failed, keep b64: " + err.Error())
				continue
			}
			setJSONRawString(item, "url", signed)
			delete(item, "b64_json")
			keys = append(keys, key)
			changed = true
		}
	}
	if !changed {
		return body, nil
	}
	newData, err := common.Marshal(items)
	if err != nil {
		return body, nil
	}
	envelope["data"] = newData
	out, err := common.Marshal(envelope)
	if err != nil {
		return body, nil
	}
	return out, keys
}

// jsonRawString 从 RawMessage 中取 JSON 字符串值；非字符串/缺失返回空。
func jsonRawString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := common.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// setJSONRawString 把字符串值编码回 map 字段。
func setJSONRawString(m map[string]json.RawMessage, key, val string) {
	if enc, err := common.Marshal(val); err == nil {
		m[key] = enc
	}
}

// persistThirdPartyImage 为第三方图片构造 Key 并落盘，返回签名 URL。
// persistImageToOBS 落盘接缝。mediastore 的包级函数在没有真 OBS 时无法打桩，
// 单测通过替换它注入假实现（手法与 relay/task_media_offload.go 的 defaultUploader 一致）。
var persistImageToOBS = persistThirdPartyImage

// 返回签名 URL 与对象 key；key 供同步任务留档（理由见 PersistImageNFSToOBS）。
func persistThirdPartyImage(ctx context.Context, userID int, group, modelName string, src mediastore.PersistSource, ext string) (string, string, error) {
	s := system_setting.GetMediaStorageSettings()
	if src.UpstreamURL != "" && !s.IngestUpstreamURL {
		return "", "", fmt.Errorf("上游 URL 落盘已关闭")
	}
	if ext == "" {
		ext = "png"
	}
	taskID := "img_" + common.GetRandomString(16)
	key := mediastore.BuildKey("t2i", modelName, userID, taskID, ext, time.Now())
	meta := map[string]string{"user-id": strconv.Itoa(userID)}
	if err := persistMedia(ctx, key, src, meta, watermarkTarget{
		Group: group, Source: "third_party_image", UserId: userID, KindHint: system_setting.MediaKindImage,
	}); err != nil {
		return "", "", err
	}
	signed, err := mediastore.Sign(ctx, key)
	if err != nil {
		return "", "", err
	}
	return signed, key, nil
}

func watermarkB64Image(ctx context.Context, value string, userID int, group string) (string, bool) {
	tpl := system_setting.GetWatermarkSettings().ResolveTemplate(group, system_setting.MediaKindImage)
	if tpl == nil {
		return value, false
	}
	fail := func(err error) (string, bool) {
		recordWatermarkFailure("b64_image", string(system_setting.MediaKindImage), group, userID, "", "", err)
		return value, false
	}
	if ready, reason := watermarkAvailable(); !ready {
		return fail(fmt.Errorf("watermark unavailable: %s", reason))
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return fail(fmt.Errorf("decode b64 image: %w", err))
	}
	dir, err := os.MkdirTemp("", "watermark-b64-*")
	if err != nil {
		return fail(err)
	}
	defer os.RemoveAll(dir)
	input := filepath.Join(dir, "source.png")
	output := filepath.Join(dir, "watermarked.png")
	if err := os.WriteFile(input, raw, 0o600); err != nil {
		return fail(err)
	}
	if err := applyWatermark(ctx, input, output, system_setting.MediaKindImage, "png", *tpl); err != nil {
		return fail(err)
	}
	watermarked, err := os.ReadFile(output)
	if err != nil {
		return fail(err)
	}
	return base64.StdEncoding.EncodeToString(watermarked), true
}

func extFromURL(rawURL string) string {
	ext := strings.TrimPrefix(path.Ext(pathOnly(rawURL)), ".")
	if ext == "" {
		return "png"
	}
	return strings.ToLower(ext)
}

// buildTaskObjectKey 为无 nfs_path 的第三方任务构造 OBS Key（§4.2）。
// feature 取自 task.Action，model 取自 OriginModelName，ext 由 URL 路径推断。
func buildTaskObjectKey(task *model.Task, rawURL string) string {
	feature := task.Action
	if feature == "" {
		feature = "task"
	}
	ext := strings.TrimPrefix(path.Ext(pathOnly(rawURL)), ".")
	return mediastore.BuildKey(feature, task.Properties.OriginModelName,
		task.UserId, task.TaskID, ext, time.Now())
}

// pathOnly 去掉 URL 的 query，便于取扩展名。
func pathOnly(rawURL string) string {
	if i := strings.IndexAny(rawURL, "?#"); i >= 0 {
		return rawURL[:i]
	}
	return rawURL
}
