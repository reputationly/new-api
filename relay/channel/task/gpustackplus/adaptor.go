// Package gpustackplus 实现「GPUStackPlus」任务渠道:对接二次开发 GPUStack 的
// LightX2V 内置后端异步门面(/v1/videos,2026-07-06 上线,见 gpustack 仓
// docs/lightx2v-backend-design.md §6.0 与 docs/lightx2v-m4-m5-handover.md)。
//
// 门面契约(GPUStack server,非直连引擎):
//
//	POST {base}/v1/videos        body: {model(必填), task_type, prompt, user_id,
//	                                    image(URL 或 base64/data-uri), ...引擎可选参数}
//	                             → {task_id, status, model, task_type, nfs_path, error, error_type}
//	GET  {base}/v1/videos/{id}   → 同上;status ∈ queued/assigned/running/done/failed/canceled;
//	                               done 时 nfs_path 为成品在共享 SFS 上的绝对路径
//
// 关键约定:
//   - save_result_path / image_path 等引擎原生路径字段是门面的 engine-owned 字段,
//     外部传入会被剥掉——路径由门面统一 dictates 并自建父目录,new-api 不再拼路径
//     也不再 mkdir,完成后从状态响应读 nfs_path 交给落盘钩子搬 OBS;
//   - 图片输入走 "image" 字段(URL 直透 / base64 由门面持久化到 SFS inputs/ 再喂引擎);
//   - 除保留字段外的请求参数(negative_prompt/seed/target_video_length 等)原样透传,
//     门面转交引擎,校验归上游(new-api 侧 metadata 即此通道)。
package gpustackplus

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	"github.com/QuantumNous/new-api/relay/channel/gpustackplus/nfsinput"
	taskcommon "github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/service/mediastore"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/pkg/errors"
)

// new-api 侧当前支持的 task_type,与门面 routes/videos.py 的 _VALID_TASK_TYPES 对齐。
//
// tts(语音合成,IndexTTS-2):文本走 prompt,参考音色 metadata.voice + 可选情感参考音
// metadata.emotion_audio 物化到 input_refs,情感标量(emo_vector/emo_alpha/emo_text)收进
// body.extra_params(引擎只从 extra_params 读)。见 materializeTTSInputs / foldEmotionParamsIntoExtra。
//
// s2v(数字人,InfiniteTalk):人物图走 image/input_reference,驱动音频 metadata.audio,
// 一并物化到 input_refs(image + audio)。见 materializeS2VInputs。
// sr(超分,SeedVR2):源视频 metadata.video 物化到 input_refs.video,倍率 metadata.sr_ratio
// 随 metadata 透传(门面按 config 目标尺寸封顶)。见 materializeSRInputs。
// v2v/rv2v/r2v/mv2v/ads2v(视频编辑/图生视频,Bernini,顶替下线的 wan2.2-VACE):按输入
// 组合区分——v2v 单源视频 metadata.src_video、rv2v 源视频+参考图 metadata.src_ref_images、
// r2v 仅参考图(体验区「图生视频」)、mv2v 双源视频多源编辑、ads2v 双源视频广告植入
// (与 mv2v 同输入,引擎侧 system prompt/guidance 不同,只能显式指定)。
// 物化到 input_refs,见 materializeBerniniInputs。
var validTaskTypes = map[string]bool{
	"t2i": true, "i2i": true, "t2v": true, "i2v": true, "flf2v": true,
	"tts": true, "s2v": true, "sr": true, "v2v": true, "rv2v": true, "r2v": true,
	"mv2v": true, "ads2v": true,
	// 关键帧「只给尾帧」(MiniMax H3 的 L2VA):H3 的一个 FL2VA checkpoint 同时吃
	// 首帧/尾帧/首尾帧,由 extra_params.frame_indices([0] / [-1] / [0,-1])区分,
	// 门面负责把 l2va 翻成 fl2va + [-1]。与 i2v 输入形态相同(都是 1 张图),
	// 只有语义不同,所以必须独立成值——靠张数推不出"这张是尾帧"。
	// 须与门面 _VALID_TASK_TYPES、gpustack-ui task-inputs.ts 同步。
	"l2va": true,
	// 参考生视频(MiniMax H3 Ref2VA):参考图(+可选音色参考)→ 带语音的视频。
	// 与 s2v(InfiniteTalk 音频驱动口型)、r2v(Bernini 纯参考图、无音频)都不同,
	// 故独立成值 —— 三者的 maxAudioSec 语义与计费口径都不一样。
	"r2va": true,
	// 视频配乐(LTX-2.3 v2a):输入视频 + 可选 prompt → 原画面逐帧不动 + AI 音轨的
	// mp4。2026-07 契约改判:v2a 原属 AudioX(出 .wav 纯音频),该产品线下线,
	// v2a 现为「视频→配好音的视频」任务形态(可挂多模型,LTX-2.3 首发);tv2a
	// 随之删除(新契约 prompt 本就可选,无需"带文本"单列)。
	"v2a": true,
	// 音乐生成(ACE-Step):t2m 纯文本、cover 参考音频、repaint 源音频。
	"t2m": true, "cover": true, "repaint": true,
	// 扩散音频(vLLM-Omni audiogen):AudioX t2a/v2m/tv2m + SoulX-Singer svs。
	"t2a": true, "v2m": true, "tv2m": true, "svs": true,
}

// legacyInputKeys 旧的原始输入 / 引擎原生路径字段:输入统一走 input_refs,这些键
// 若从 metadata 混进 body,门面会因"检测到原始输入字段"整单 400,故播种后剥掉。
// 与门面 _INPUT_FIELDS + _ENGINE_OWNED_FIELDS 对齐(含 TTS 的 voice/emotion_audio)。
var legacyInputKeys = map[string]bool{
	"image": true, "last_frame": true, "image_mask": true, "audio": true,
	"voice": true, "emotion_audio": true,
	"video": true, "src_video": true, "src_mask": true, "src_ref_images": true,
	// 音乐(ACE-Step)原始输入 + 引擎原生路径字段。
	"reference_audio": true, "src_audio": true,
	// vLLM-Omni TTS 参考音原始输入 + 门面注入的引擎路径字段(ref_audio→ref_audio_path)。
	// 输入统一走 input_refs 物化,残留裸键会被门面当作"原始输入"整单拒。
	"ref_audio": true, "ref_audio_2": true,
	"ref_audio_path": true, "ref_audio_2_path": true,
	// 扩散音频(vLLM-Omni audiogen):AudioX 视频复用 video(已在上);SoulX SVS 的
	// prompt_audio/target_audio 引擎字段名与裸键同名,一并剥离。
	"prompt_audio": true, "target_audio": true,
	"image_path": true, "last_frame_path": true, "image_mask_path": true,
	"audio_path": true, "spk_audio_path": true, "emo_audio_path": true,
	"video_path": true, "save_result_path": true,
	"reference_audio_path": true, "src_audio_path": true,
}

// localBadRequest 构造本地 400 skip-retry 错误:BuildRequestBody 里的输入校验 /
// 物化失败(URL 下不到、非法 task_type 等)属客户端问题,不应触发跨渠道重试。
// relay_task.go 识别 *types.NewAPIError 并转成 LocalError 的 TaskError。
func localBadRequest(err error) *types.NewAPIError {
	return types.NewErrorWithStatusCode(
		err, types.ErrorCodeInvalidRequest, http.StatusBadRequest,
		types.ErrOptionWithSkipRetry(),
	)
}

// submitResponse 门面提交接口返回(_public 形态,提交时 nfs_path 恒为 null)。
type submitResponse struct {
	TaskID string `json:"task_id"`
	Status string `json:"status"`
}

// statusResponse 门面状态接口返回(_public 形态)。
type statusResponse struct {
	TaskID    string `json:"task_id"`
	Status    string `json:"status"`
	NFSPath   string `json:"nfs_path"`
	Error     string `json:"error"`
	ErrorType string `json:"error_type"`
	// Progress 门面已按阶段权重表折算好的全局进度 0-100(引擎只报"阶段+阶段内
	// 百分比",合成在门面侧完成);Phase 是产生该进度的阶段名,仅用于日志/排查。
	// 老版本门面不返回这两个字段,零值走 taskcommon 的固定档位兜底。
	Progress float64 `json:"progress"`
	Phase    string  `json:"phase"`
}

// progressInProgressFloor/Ceil 把门面的 0-100 映射到 new-api 的进度语义。
// new-api 里 100% 是终态(成功/失败都写 100%,见 service/task_polling.go),
// 运行中报 100% 会被前端读成"已完成但没有结果";10%/20% 又已被 submitted/
// queued 占用。所以真实进度压缩进 [30,95],既接上 ProgressInProgress 的起点,
// 又给落 OBS 那段尾巴留出空间。
const (
	progressInProgressFloor = 30.0
	progressInProgressCeil  = 95.0
)

// scaleProgress 把门面的全局进度折进 [30,95] 区间并格式化成 new-api 的 "N%"。
func scaleProgress(global float64) string {
	if global < 0 {
		global = 0
	}
	if global > 100 {
		global = 100
	}
	scaled := progressInProgressFloor + (progressInProgressCeil-progressInProgressFloor)*global/100
	return strconv.Itoa(int(scaled)) + "%"
}

type TaskAdaptor struct {
	taskcommon.BaseBilling
	ChannelType int
	apiKey      string
	baseURL     string
}

func (a *TaskAdaptor) Init(info *relaycommon.RelayInfo) {
	a.ChannelType = info.ChannelType
	a.baseURL = strings.TrimRight(info.ChannelBaseUrl, "/")
	a.apiKey = info.ApiKey
}

func (a *TaskAdaptor) ValidateRequestAndSetAction(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	// 成品只落 SFS(nfs_path),必须经 OBS 才能对外提供 URL——存储关闭时提前拒绝,
	// 不占用 GPU 渲染一个交付不出去的成品。
	if !mediastore.Enabled() {
		return service.TaskErrorWrapper(
			fmt.Errorf("媒体存储(OBS)未启用,gpustackplus 渠道无法对外提供成品 URL,请先在系统设置启用"),
			"media_storage_disabled", http.StatusServiceUnavailable)
	}
	if taskErr := relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate); taskErr != nil {
		return taskErr
	}
	// 若超管为该模型配置了时长白名单(系统设置→视频模型配置),按配置校验;未配置则
	// 不加限制。此处早于模型映射,用请求里的公开 model 名做 key。参数错误归为本地 400
	// (不重试、不误标渠道故障)。
	// 配置按公开模型名键控(体验区用选中的公开名读它),映射不改 OriginModelName;
	// 故只用公开名做 key,与映射时机无关。
	// tab 的选取依赖 task_type,而它主要由「体验区配置声明的候选集 ∩ 输入形态」定
	// (见 taskTypeOfRequest),两者都只认公开名与请求内容,与映射时机无关。
	// 只有模型没配进体验区时才退回 inferTaskType(名字推断):那条兜底路径这里用公开名、
	// BuildRequestBody 用映射后的上游名,重定向时仍可能分叉。不误拒的依据是
	// 「模型级配置 ⊇ 任一 tab」这条不变量(见前端 recomputeModelLevel:列表并集、上限取
	// max、任一 tab 不限则整体不限;迁移也只从模型级往 tab 扇出)—— 推错到没配的 tab 会
	// 退回模型级(最宽松)。若哪天把模型级改成交集或精确值,须把校验挪到模型映射之后。
	// 尺寸不校验:sizes 只供体验区做候选值,档位词/宽高比与精确像素对不上(见
	// common/media_model_config.go 文件头),交由引擎判定。
	if req, err := relaycommon.GetTaskRequest(c); err == nil {
		taskType, terr := taskTypeOfRequest(&req,
			firstNonEmpty(req.Model, info.OriginModelName), req.Model, info.OriginModelName)
		if terr != nil {
			return service.TaskErrorWrapperLocal(terr, "invalid_request", http.StatusBadRequest)
		}
		if verr := common.ValidateVideoDurationForModel(taskType, req.Duration, req.Seconds,
			req.Model, info.OriginModelName); verr != nil {
			return service.TaskErrorWrapperLocal(verr, "invalid_request", http.StatusBadRequest)
		}
	}
	return nil
}

func (a *TaskAdaptor) BuildRequestURL(info *relaycommon.RelayInfo) (string, error) {
	// 视频经任务子系统走异步门面;图片走同步 relay,另行接入。
	return fmt.Sprintf("%s/v1/videos", a.baseURL), nil
}

func (a *TaskAdaptor) BuildRequestHeader(c *gin.Context, req *http.Request, info *relaycommon.RelayInfo) error {
	req.Header.Set("Content-Type", "application/json")
	if a.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+a.apiKey)
	}
	return nil
}

func (a *TaskAdaptor) BuildRequestBody(c *gin.Context, info *relaycommon.RelayInfo) (io.Reader, error) {
	req, err := relaycommon.GetTaskRequest(c)
	if err != nil {
		return nil, errors.Wrap(err, "get_task_request_failed")
	}

	modelName := firstNonEmpty(info.UpstreamModelName, req.Model, info.OriginModelName)
	if modelName == "" {
		return nil, fmt.Errorf("model is required (渠道模型映射与请求 model 均为空)")
	}

	// OpenAI /v1/videos 风格用 input_reference 传条件图;公共校验只归一化了
	// image→Images,这里补上,否则合法的 i2v 请求会被下方防呆误拒。
	if !req.HasImage() && strings.TrimSpace(req.InputReference) != "" {
		req.Images = []string{req.InputReference}
	}

	// 引擎可识别的可选参数(negative_prompt / seed / target_video_length /
	// aspect_ratio 等)经 metadata 整体透传;门面会剥掉 engine-owned 字段,
	// 下面的保留字段随后覆盖同名键,防止篡改核心语义。
	//
	// 白名单加固:若该模型配了时长白名单,剔除 metadata 里的引擎原生别名键
	// (target_video_length / num_frames 等),否则客户端可绕过顶层 duration 的校验,
	// 用 metadata 直接注入被禁值。被锁维度只允许走(已校验的)顶层字段。
	// 尺寸无对应加固:sizes 不再做接口校验,没有可绕过的校验,metadata 里的尺寸类键照常透传。
	// task_type 在这里一次解析、下面复用:早前 allowedDurations 与 body["task_type"]
	// 各推一次,前者用公开名、后者用上游名,做了模型重定向时会分叉。
	resolvedTaskType, err := taskTypeOfRequest(&req, modelName, req.Model, info.OriginModelName)
	if err != nil {
		return nil, err
	}
	allowedDurations, _ := common.VideoDurationsAllowedForModel(
		resolvedTaskType, req.Model, info.OriginModelName)
	durationLocked := len(allowedDurations) > 0
	body := make(map[string]any, len(req.Metadata)+8)
	for k, v := range req.Metadata {
		lk := strings.ToLower(strings.TrimSpace(k))
		if durationLocked && durationOverrideKeys[lk] {
			continue
		}
		body[k] = v
	}
	// 剥掉遗留输入 / 引擎路径字段(§N4):输入统一走 input_refs,残留会被门面整单拒。
	for k := range body {
		if legacyInputKeys[strings.ToLower(strings.TrimSpace(k))] {
			delete(body, k)
		}
	}
	body["model"] = modelName
	body["prompt"] = req.Prompt
	// user_id 用字符串:与 NFS 输入路径的 <user_id> 段一致,门面校验 parent_dir_name == user_id。
	body["user_id"] = fmt.Sprintf("%d", info.UserId)
	if _, ok := body["task_type"]; !ok {
		body["task_type"] = resolvedTaskType
	}
	// 转发顶层 size:同时给 size 与由它换算的 aspect_ratio,兼容不同引擎读法。
	// 顶层 size 优先级高于 metadata 同名键(在上面的透传之后赋值,覆盖之)。
	if s := strings.TrimSpace(req.Size); s != "" {
		body["size"] = s
		if ar := common.AspectRatioFromSize(s); ar != "" {
			body["aspect_ratio"] = ar
		}
	}
	taskType, _ := body["task_type"].(string)
	// task_type 白名单校验(§N2):它可能来自 metadata,非法值既会让 NFS 写盘路径异常,
	// 也会被门面拒;就地本地 400,不进后续物化 / 提交。
	if !validTaskTypes[taskType] {
		return nil, localBadRequest(fmt.Errorf("不支持的 task_type: %q(允许:t2i/i2i/t2v/i2v/l2va/flf2v/tts/s2v/r2va/sr/v2a/v2v/rv2v/r2v/mv2v/ads2v/t2m/cover/repaint/t2a/v2m/tv2m/svs)", taskType))
	}
	// SoulX svs 的文本仅占位(引擎按 prompt_audio/target_audio 生成歌声),但引擎 input 需非空、
	// 且真机验证过的请求带 "soulx-singer" 标签。ValidateBasicTaskRequest 已豁免 svs 的空 prompt,
	// 这里为空时兜底一个 label,避免直连空 prompt 传到引擎(v2a/v2m 纯视频输入,空 prompt 是正确
	// 语义,不兜底)。
	if taskType == "svs" && strings.TrimSpace(req.Prompt) == "" {
		req.Prompt = "soulx-singer"
		body["prompt"] = req.Prompt
	}
	// v2a(视频配音)的提示词整形。LTX-2.3 挂的是 Foley LoRA,它的训练字幕里带「无人声/
	// 无音乐」的抑制句,官方与社区模型卡给的示例提示词也一律以这两句结尾。不加时该模型
	// 的典型失效就是配出一段与画面无关的背景音乐——社区那个 foley LoRA 正是把「LTX-2.3
	// 自己加背景音乐,而你想要真实音效」列为主要使用场景。
	// 想要音乐配乐请用 v2m(视频→音乐),那是另一个 task_type、另一套模型。
	if taskType == "v2a" {
		req.Prompt = withFoleySuppression(req.Prompt)
		body["prompt"] = req.Prompt
		// 负向词只在客户端没给时兜底。
		//
		// ⚠ 当前部署下这个字段「传了但不生效」,是有意保留的,别当死代码删掉。
		//
		// 为什么不生效(链路在最后一步断):new-api 塞进 body → gpustack 门面原样透传 →
		// LightX2V 的 ltx2_runner.run_text_encoder 判 `if config["enable_cfg"]`,为 false
		// 时走 else 分支、压根不编码负向文本(那里有明注)。而我们的 v2a 配置正是
		// enable_cfg=false。
		//
		// 为什么不能直接把 enable_cfg 打开:v2a 挂的是**引导蒸馏**的
		// ltx-2.3-22b-distilled-1.1(cfg=1 + 8 步硬编码 sigma 表)。引导已经烤进权重,
		// 再叠一层外部 CFG 只会过饱和,且蒸馏模型从未被训练做无条件预测,uncond 分支不可信。
		// 佐证:LightX2V configs/ltx2/ 下 13 个配置里,蒸馏 ckpt 一律 enable_cfg=false /
		// scale=1 / 8 步,非蒸馏一律 true / 3~4 / 30~40 步,相关性 100%。
		//
		// 什么时候会活过来:v2a 换到非蒸馏 dev ckpt 时(那才是 Foley LoRA 模型卡
		// 30 步 / guidance 6 的适用前提,代价约 4 倍推理耗时)。届时要同步改的是
		// LightX2V configs/ltx2/{,a100/}ltx2_3_v2a.json 三项:dit_original_ckpt 换成
		// 非蒸馏权重、enable_cfg=true、sample_guide_scale=6.0,并删掉 distilled_sigma_values
		// (它是配 8 步的固定表)、infer_steps 提到 30。本文件这侧不用动。
		if !hasKeyFold(body, "negative_prompt") {
			body["negative_prompt"] = foleyNegativePrompt
		}
	}
	// 输入兼容性防呆必须在物化之前(§N2 复审):否则 t2v/t2i 带图、flf2v 只给 1 张等非法
	// 组合会先把图写到 NFS 再被拒,留下孤儿输入文件。这些检查只依赖 taskType / req,不需物化。
	if imageRequiredTaskTypes[taskType] && !req.HasImage() {
		return nil, localBadRequest(fmt.Errorf("模型 %s 的任务类型 %s 需要图片输入,必须提供 image/input_reference", modelName, taskType))
	}
	if textOnlyTaskTypes[taskType] && req.HasImage() {
		return nil, localBadRequest(fmt.Errorf("模型 %s 的任务类型 %s 不接受图片输入;要按图生成请改用支持图片输入的任务类型(i2v/l2va/flf2v)或对应模型", modelName, taskType))
	}
	if taskType == "flf2v" && len(req.Images) < 2 {
		return nil, localBadRequest(fmt.Errorf("模型 %s 的任务类型 flf2v(首尾帧)需要首帧和尾帧两张图:请提供 images=[首帧,尾帧]", modelName))
	}
	// 反向防呆:i2v 只物化 images[0](materializeVideoInputs 仅在 flf2v 分支读 images[1]),
	// 多传的尾帧会被静默丢弃 —— 引擎侧同样如此(I2VInputInfo 没有 last_frame_path 字段,
	// 多余键被 update_input_info_from_dict 丢掉),用户拿到的是一条只用首帧的普通 i2v 却
	// 毫无提示。宁可 400 也不要静默降级。
	if taskType == "i2v" && len(req.Images) > 1 {
		return nil, localBadRequest(fmt.Errorf("模型 %s 的任务类型 i2v(图生视频)只接受首帧一张图,多传的图不会生效;首尾帧请显式指定 metadata.task_type=flf2v 并提供 images=[首帧,尾帧]", modelName))
	}
	// 同上,l2va(只给尾帧)也只物化 images[0]。引擎侧 frame_indices 的索引数量必须与
	// 图片数量相等(fl2va 只接受 [0] / [-1] / [0,-1] 三种),门面给 l2va 回填的是
	// 单元素 [-1],多传的图会让两者对不上而被引擎拒——与其让它烂在下游,不如就地 400。
	if taskType == "l2va" && len(req.Images) > 1 {
		return nil, localBadRequest(fmt.Errorf("模型 %s 的任务类型 l2va(只给尾帧)只接受尾帧一张图,多传的图不会生效;首尾帧请显式指定 metadata.task_type=flf2v 并提供 images=[首帧,尾帧]", modelName))
	}
	// 同上:s2v 只物化 images[0](materializeS2VInputs),多传的人物图静默丢弃。
	// 这条防呆不能靠 taskTypesCompatibleWithInputs 收紧谓词代劳 —— 单玩法模型走
	// 「候选集只剩一个」的快捷路径、显式 metadata.task_type 走第 1 级,两条都不看谓词;
	// 且谓词收紧后兼容集为空,报出来的是"无法判定是哪种玩法",指错方向。
	if taskType == "s2v" && len(req.Images) > 1 {
		return nil, localBadRequest(fmt.Errorf("模型 %s 的任务类型 s2v(数字人)只接受一张人物图,多传的图不会生效", modelName))
	}
	if taskType == "tts" {
		// 语音合成不接受图片输入(参考音走 metadata.voice,下面单独物化)。
		if req.HasImage() {
			return nil, localBadRequest(fmt.Errorf("模型 %s 的任务类型 tts 不接受图片输入", modelName))
		}
		if strings.TrimSpace(req.Prompt) == "" {
			return nil, localBadRequest(fmt.Errorf("模型 %s 的任务类型 tts 需要合成文本(prompt)", modelName))
		}
		// 字数上限(AudioModelConfig,按模型/全局默认;0=不限制):就地本地 400,防前端绕过。
		if err := common.ValidateAudioTextForModel(taskType, req.Prompt, req.Model, info.OriginModelName, modelName); err != nil {
			return nil, localBadRequest(err)
		}
	}
	if taskType == "t2m" || taskType == "cover" || taskType == "repaint" ||
		taskType == "t2a" || taskType == "tv2m" {
		// 字数上限(MusicModelConfig,按模型/全局默认;0=不限制):就地本地 400,防前端(含
		// 直连 /pg/videos)绕过。ACE-Step 校验 prompt/lyrics/sample_query;AudioX 文本类
		// (t2a/tv2m)也归「音乐」大类,同样受 MusicModelConfig 字数限制,只有 prompt。
		// (tv2a 已随 AudioX 视频配乐下线;v2a 现属视频大类,不走音乐字数限制。)
		// 任一字段超限即拒。
		for _, txt := range []string{
			req.Prompt,
			metadataString(req.Metadata, "lyrics"),
			metadataString(req.Metadata, "sample_query"),
		} {
			if strings.TrimSpace(txt) == "" {
				continue
			}
			if err := common.ValidateMusicTextForModel(taskType, txt, req.Model, info.OriginModelName, modelName); err != nil {
				return nil, localBadRequest(err)
			}
		}
	}

	// 输入物化:每个 task_type 落齐自己需要的输入到 NFS,统一发 input_refs 相对路径(不再
	// 发 base64/URL 给门面,方案见 gpustack 仓 docs/lightx2v-nfs-input-design.md)。物化顺序
	// 一律"先写全部输入 → 再提交",任一路失败回滚已写文件(见各 materialize 函数),避免孤儿。
	// URL 下不到 / SSRF 拒 / 写盘失败:本地 400 skip-retry,不触发跨渠道重试(§N3)。
	var refs map[string][]string
	switch taskType {
	case "tts":
		if IsOmniTTSModel(modelName) {
			// vLLM-Omni:参考音走 ref_audio(+ MOSS-TTSD 第二说话人 ref_audio_2),
			// 均可选;预设音色走标量 speaker 透传(不物化)。VoiceGenerator/SoundEffect
			// 纯文本无参考音。
			refs, err = materializeOmniTTSInputs(c, info, taskType, modelName, req)
		} else {
			// IndexTTS-2(现由 vLLM-Omni 引擎服务):情感合成前端仍用 IndexTTS 语义键
			// (voice 参考音色 + emotion_audio 情感参考音),但引擎读 ref_audio/emo_audio。
			// materializeTTSInputs 物化为 ref_audio→ref_audio_path、emotion_audio→emo_audio_path。
			refs, err = materializeTTSInputs(c, info, taskType, modelName, req)
		}
	case "s2v":
		// 数字人:人物图(image/input_reference)+ 驱动音频(metadata.audio)。
		refs, err = materializeS2VInputs(c, info, taskType, modelName, req)
	case "r2va":
		// 参考生视频(H3 Ref2VA):参考图 1~N + 可选音色参考(metadata.audio)。
		refs, err = materializeR2VAInputs(c, info, taskType, modelName, req)
	case "sr":
		// 超分:源视频(metadata.video);倍率 sr_ratio 随 metadata 透传,不物化。
		refs, err = materializeSRInputs(c, info, taskType, modelName, req)
	case "v2v", "rv2v", "r2v", "mv2v", "ads2v":
		// Bernini:v2v 单源视频 / rv2v 源视频+参考图 / r2v 仅参考图(图生视频)/
		// mv2v·ads2v 双源视频(多源编辑 / 广告植入)。
		refs, err = materializeBerniniInputs(c, info, taskType, modelName, req)
	case "t2m", "cover", "repaint":
		// 音乐生成:t2m 无输入;cover 需参考音频(metadata.reference_audio);
		// repaint 需源音频(metadata.src_audio)。
		refs, err = materializeMusicInputs(c, info, taskType, modelName, req)
	case "t2a":
		// AudioX 文本→音效/音乐:纯文本 prompt,无输入物化。
	case "v2a":
		// 视频配乐(LTX-2.3 首发):源视频(metadata.video)+ 可选 prompt(透传)。
		// 归视频大类 → 走 VideoModelConfig 上限(newVideoMaterializer),与下线的
		// AudioX v2a(音乐大类上限)不同,故独立物化函数。
		refs, err = materializeDubInputs(c, info, taskType, modelName, req)
	case "v2m", "tv2m":
		// AudioX 视频→音乐:物化视频(metadata.video);tv2m 另有文本 prompt(透传)。
		refs, err = materializeAudioXVideoInputs(c, info, taskType, modelName, req)
	case "svs":
		// SoulX-Singer 集成 preprocess:物化 prompt_audio(音色参考)+ target_audio(目标曲/伴奏)。
		refs, err = materializeSingingInputs(c, info, taskType, modelName, req)
	default:
		// t2v/i2v/l2va/flf2v/i2i/t2i:有图才物化(纯文本 t2v/t2i 无输入)。
		// 首尾帧(flf2v):images[0]=首帧→image,images[1]=尾帧→last_frame;其余只取首帧。
		// l2va(只给尾帧)同样只取 images[0] 写 image —— 帧位置由门面回填的
		// extra_params.frame_indices=[-1] 表达,不靠字段名,见 materializeVideoInputs。
		if req.HasImage() {
			refs, err = materializeVideoInputs(c, info, taskType, modelName, req)
		}
	}
	if err != nil {
		return nil, localBadRequest(err)
	}
	if len(refs) > 0 {
		body["input_refs"] = refs
	}
	// OpenAI 风格 duration/seconds → wan 帧数约定(4n+1,16fps:5s → 81 帧)。
	durationSec := req.Duration
	if durationSec == 0 && strings.TrimSpace(req.Seconds) != "" {
		if v, convErr := strconv.Atoi(strings.TrimSpace(req.Seconds)); convErr == nil {
			durationSec = v
		}
	}
	// MiniMax H3 是另一套约定,整体绕开下面的 wan/InfiniteTalk 帧数与时长逻辑:
	// 24fps + 17n+5 帧栅格 + extra_params.duration(float 秒),且画布要 width/height。
	// 判据是**配置声明的引擎族**而不是模型名 —— 前端拿公开名、后端拿重定向后的上游名,
	// 靠名字判必然分叉(见 common.VideoEngineFamilyForModel 的注释)。
	isMiniMaxH3 := common.VideoEngineFamilyForModel(req.Model, info.OriginModelName, modelName) == common.VideoEngineMinimaxH3
	if isMiniMaxH3 {
		// durationLocked 必须传进去:上游那道 durationOverrideKeys 只剥顶层键,
		// 剥不到 H3 走的 extra_params 嵌套时长,不在这里补就是一条白名单绕过口。
		//
		// 步数按模型取(蒸馏版与基座共用引擎族但标定步数不同),取不到回落基座档。
		// 候选名与引擎族用同一组:公开名与重定向后的上游名都可能是配置里的键。
		steps := common.VideoInferenceStepsForModel(req.Model, info.OriginModelName, modelName)
		applyMiniMaxH3Request(body, taskType, durationSec, durationLocked, steps)
	}
	// s2v(数字人)除外:引擎不读 target_video_length,下发它没有任何效果,只会让人误以为
	// 时长可控。s2v 的时长走下面的 video_duration。别恢复。
	if durationSec > 0 && taskType != "s2v" && !isMiniMaxH3 {
		if _, ok := body["target_video_length"]; !ok {
			body["target_video_length"] = durationSec*16 + 1
		}
	}
	// s2v 的输出时长 = min(驱动音频时长, video_duration, 参考视频时长)。不下发
	// video_duration 时它回落到引擎实例配置(实测某些实例是 30),于是 60 秒音频只出
	// 30 秒——2026-08 线上就这么截过一次。
	//
	// 注意别再把这里写成「产出长度完全由驱动音频决定」:那个结论来自一次 10 秒音频的
	// 实测,10 < 30 压根没触发截断,是把"这次没截"过度推广了。
	//
	// 上限取 maxAudioSec + 容差,与物化层那道音频时长闸门(newVideoMaterializer →
	// checkAudioDuration)用同一个阈值。必须同一个:那边放行到 maxAudioSec+容差,这里若
	// 只给 maxAudioSec,被容差放进来的那一截就会被引擎截掉——s2v 是嘴型对齐音频,末尾被
	// 砍就是"最后一个字没说完",比画面早结束显眼得多,等于容差白放。
	// 未配则不下发,维持引擎实例配置的行为。
	//
	// 客户端在 metadata 里显式给了 video_duration 就尊重它,不覆盖也不 clamp。理由:
	//   - 那是明确的用户意图(「这条 60 秒音频我只要前 30 秒」),覆盖会让直连调用方
	//     没法请求短于配置上限的视频;体验区两端都不发这个字段,只有直连才会走到;
	//   - 它也不构成绕过 maxAudioSec 的路子。真正的硬约束是音频时长——物化层已按
	//     真实时长拒掉超限输入,这里传再大的值,引擎的 min(音频时长, video_duration)
	//     也吃不到更多算力。往小传只是少生成,更无危害。
	// 别改成无条件覆盖:上面那句「与物化层同一阈值」约束的是我们下发的默认值,不是
	// "任何来源的 video_duration 都得等于这个数"。
	//
	// H3 除外(isMiniMaxH3):它的音频是**音色样本**,引擎按 prompt 里的台词文本生成语音、
	// 音色向参考靠拢,音频长度与输出时长毫无关系 —— 下发 video_duration 会把输出错误地
	// 卡在音频时长配置上。这与 InfiniteTalk「用现成音轨驱动口型」不是同一件事。
	if taskType == "s2v" && !isMiniMaxH3 {
		if _, ok := body["video_duration"]; !ok {
			if maxSec, cfgOK := common.VideoMaxAudioSecForModel(taskType, req.Model, info.OriginModelName, modelName); cfgOK && maxSec > 0 {
				body["video_duration"] = maxSec + nfsinput.AudioDurationToleranceSec
			}
		}
	}
	// IndexTTS-2 情感标量:引擎(vLLM-Omni IndexTTS2 talker)只从 extra_params 读
	// emo_vector/emo_alpha/…,顶层同名键会被引擎 AudioTaskRequest(继承
	// OpenAICreateSpeechRequest,extra=ignore)静默丢弃。前端经 metadata 平铺发来,
	// 这里把它们从 body 顶层收进 body["extra_params"](门面非控制键,原样透传)。
	if taskType == "tts" {
		foldEmotionParamsIntoExtra(body)
	}

	data, err := common.Marshal(body)
	if err != nil {
		return nil, errors.Wrap(err, "marshal_request_body_failed")
	}
	return bytes.NewReader(data), nil
}

// 门面 task_type 的输入约束(与 gpustack routes/videos.py 的 _VALID_TASK_TYPES 对应)。
// s2v(数字人)也需要人物图,故列入 imageRequiredTaskTypes;它额外需要驱动音频,由
// materializeS2VInputs 校验。sr / v2v/rv2v/r2v 的输入是视频或参考图(走 metadata,非
// image 字段),各自的 materialize 函数校验,不进这两张表。
// 注:r2va 不在此表 —— 它的参考图走 metadata.src_ref_images 而非顶层 images,
// 且允许"纯参考视频、无图"。它自己的必填校验在 materializeR2VAInputs 里。
var imageRequiredTaskTypes = map[string]bool{"i2v": true, "l2va": true, "flf2v": true, "i2i": true, "s2v": true}
var textOnlyTaskTypes = map[string]bool{"t2v": true, "t2i": true}

// 时长白名单锁定时需剔除的引擎原生别名键——metadata 里这些键会绕过顶层 duration
// 校验(小写匹配)。尺寸无对应表:sizes 不做接口校验,无校验可绕过。
var durationOverrideKeys = map[string]bool{
	"target_video_length": true, "video_length": true, "num_frames": true, "frames": true,
}

// IndexTTS-2 情感标量键:vLLM-Omni 的 IndexTTS2 talker 只从 request.extra_params 读它们
// (见 vllm-omni tts_adapters/indextts2.py 的 _INDEXTTS2_EMOTION_KEYS)。作为顶层字段下发
// 会被引擎 AudioTaskRequest(继承 OpenAICreateSpeechRequest,Pydantic extra=ignore)丢弃。
var indexTTS2EmotionKeys = []string{
	"emo_vector", "emo_alpha", "emo_text", "use_emo_text", "use_random",
}

// foldEmotionParamsIntoExtra 把 IndexTTS-2 情感标量从 body 顶层挪进 body["extra_params"]:
// 引擎只认 extra_params 里的这些键。已有 extra_params 保留、同名不覆盖(caller 显式值优先);
// 顶层原键删除,避免"既顶层又嵌套"的歧义。门面 extra_params 非控制/引擎拥有/输入键,原样
// 透传到引擎 body 顶层,而 AudioTaskRequest 有 extra_params 字段,故能完整到达 talker。
func foldEmotionParamsIntoExtra(body map[string]any) {
	extra, _ := body["extra_params"].(map[string]any)
	for _, k := range indexTTS2EmotionKeys {
		v, ok := body[k]
		if !ok {
			continue
		}
		if extra == nil {
			extra = make(map[string]any)
		}
		if _, exists := extra[k]; !exists {
			extra[k] = v
		}
		delete(body, k)
	}
	if len(extra) > 0 {
		body["extra_params"] = extra
	}
}

// ── task_type 解析 ────────────────────────────────────────────────────────
//
// 玩法是**请求**的属性,不是**模型**的属性。一个部署同时服务文生/图生/首尾帧是我们期望
// 的部署形态(省显存),这时模型名里不可能编码出"这一次是哪种玩法"。历史实现只有
// inferTaskType 一条路,等于把 GPUStack 的部署命名当成了跨系统 API 契约 —— 而部署名是
// 运营随手起的,没有任何地方能强制,判错的后果还是静默的(落 t2v 兜底,按错误玩法派发)。
//
// 现在按四级解析,名字推断降为最后兜底:
//
//	1. 显式 metadata.task_type            —— 体验区非文生玩法都走这条
//	2. 体验区配置声明的候选集只有一个     —— 单玩法模型,直接定
//	3. 候选集 ∩ 输入形态 恰好剩一个       —— 多玩法模型的主力判据,见下
//	4. 模型没配进体验区                   —— 退回 inferTaskType,维持改造前语义
//	4'. 第 3 级剩多个,但 inferTaskType 的答案就在其中 —— 采信名字。名字带任务标识的
//	    存量模型(wan2.2-flf2v-a14b 收到首帧+尾帧两张图)改造前就这么判,不裁决会把
//	    存量直连请求打成 400
//	   都不成立 → 明确报错,要求显式指定,而不是默默猜一个发上去
//
// 第 3 级只对视频大类生效(候选集全部落在 videoFamilyTaskTypes 里时)。图像/语音/音乐
// 大类要么候选集本就唯一(语音四个 tab 共用 tts),要么输入形态区分度不足(音乐 t2m/t2a/
// svs 都是纯文本),一律走第 4 级 —— 不改变这些大类的现有行为。
//
// 由此推出一个刻意留下的边界:同名模型被配进多份配置时(如既在 VideoModelConfig 又在
// MusicModelConfig),候选集是四份的并集(见 common.PlaygroundTaskTypeCandidates),必然
// 混入非视频 task_type,于是必然退化到第 4 级名字兜底 —— 视频那部分也跟着失去输入推导。
// 这不是回归(退化后的行为与改造前逐字相同),而是"少赚一笔"。要在这种配置下继续推导,
// 得先知道本次请求属于哪个大类;但四个大类共用同一个提交入口,大类恰恰要靠 task_type
// 才能定 —— 循环,故不做。这种配置形态本身就该在管理页上改掉。

// videoFamilyTaskTypes 视频链路的 task_type 全集;只有候选集完全落在这里面时才做输入
// 形态推导(见 taskTypesCompatibleWithInputs)。
var videoFamilyTaskTypes = map[string]bool{
	"t2v": true, "i2v": true, "l2va": true, "flf2v": true, "s2v": true,
	"r2va": true,
	"sr":   true, "v2a": true,
	"v2v": true, "rv2v": true, "r2v": true, "mv2v": true, "ads2v": true,
}

// taskTypesCompatibleWithInputs 按请求实际带了哪些输入,返回**兼容的** task_type 集合。
//
// 返回集合而不是"猜的那一个"是关键:判据不足时必须暴露出来,不能替调用方拍板。
// 每条规则都要求"该玩法需要的输入齐 + 没有它不认的外来输入",后半句才是区分度的来源。
//
// 各玩法的输入契约取自本文件的 materialize* 函数,两处必须同步:
//   - 首帧图(i2v/flf2v/s2v)读顶层 req.Images(image/images/input_reference 已归一);
//   - 参考图(r2v/rv2v)读 metadata.src_ref_images —— 与首帧图是**不同的键**,
//     "都是一张图"并不代表分不开;两个键同时给才是真歧义,落到报错。
//   - sr/v2a 读 metadata.video;Bernini 读 metadata.src_video。
//
// 无法区分、只能靠显式 task_type 的两处:
//   - metadata.video 单独出现 → sr(超分)与 v2a(配乐)输入完全一致;
//   - src_video 恰好 2 个     → mv2v(多源编辑)与 ads2v(广告植入)输入完全一致。
//
// 「多传了该玩法用不上的输入」不在这里表达,由物化前的防呆逐条 400(i2v/s2v 多图、
// v2v/mv2v/ads2v 带参考图)。两个原因:显式 metadata.task_type 与「候选集只剩一个」
// 的快捷路径都不经过本函数,写在这里拦不全;且收紧谓词只会让兼容集变空,报出来的是
// 「无法判定是哪种玩法」,而调用方的真实问题是「多传了输入」,指错方向。
func taskTypesCompatibleWithInputs(req *relaycommon.TaskSubmitReq) []string {
	images := len(req.Images)
	hasAudio := metadataString(req.Metadata, "audio") != ""
	hasVideo := metadataString(req.Metadata, "video") != ""
	srcVideos := len(metadataStringList(req.Metadata, "src_video"))
	refImages := len(metadataStringList(req.Metadata, "src_ref_images"))

	noBernini := srcVideos == 0 && refImages == 0
	noFrames := images == 0 && !hasAudio
	var out []string
	add := func(ok bool, taskType string) {
		if ok {
			out = append(out, taskType)
		}
	}
	// 帧图链路:顶层图,不碰 metadata.video / Bernini 键。
	add(images == 0 && !hasAudio && !hasVideo && noBernini, "t2v")
	add(images >= 1 && !hasAudio && !hasVideo && noBernini, "i2v")
	add(images >= 2 && !hasAudio && !hasVideo && noBernini, "flf2v")
	// ⚠️ **l2va(只给尾帧)故意不在这里** —— 别"补全"它。
	//
	// l2va 与 i2v 的输入形态**完全相同**(1 张图、无音频无视频),区别纯在语义:
	// 这张图是首帧还是尾帧。本函数只看输入形态,从形态上推不出语义,加进来只会让
	// 候选集永远二义。
	//
	// 后果是实打实的回归:关键帧 tab 的候选集(见 constant/playground_tab.go 的反向
	// 索引)现在是 {i2v, flf2v, l2va},若 l2va 也"兼容 1 张图",现有 wan 关键帧模型
	// 收到 1 张图就会从「收敛到 i2v」变成「i2v/l2va 分不开 → 400」。
	//
	// 正确的来源是**显式 metadata.task_type**(taskTypeOfRequest 第 1 级直接短路):
	// 前端关键帧三态按用户填了哪个槽派生,直连调用方自己声明。
	add(images >= 1 && hasAudio && !hasVideo && noBernini, "s2v")
	// 整段视频输入:sr 与 v2a 同形,靠显式 task_type 或 tab 声明分。
	add(hasVideo && noFrames && noBernini, "sr")
	add(hasVideo && noFrames && noBernini, "v2a")
	// Bernini 编辑链路:自有键,不碰顶层图与 metadata.video。
	add(srcVideos == 1 && refImages == 0 && noFrames && !hasVideo, "v2v")
	add(srcVideos == 1 && refImages >= 1 && noFrames && !hasVideo, "rv2v")
	add(srcVideos == 0 && refImages >= 1 && noFrames && !hasVideo, "r2v")
	add(srcVideos == 2 && noFrames && !hasVideo, "mv2v")
	add(srcVideos == 2 && noFrames && !hasVideo, "ads2v")
	return out
}

// taskTypeOfRequest 复原本次请求的 task_type;判据不足时返回错误(见上方解析链说明)。
// 校验阶段与 BuildRequestBody 都调它,保证两处判据一致 —— 早前两处分别用公开名和映射后
// 的上游名各推一次,重定向时会分叉。
//
// 两个名字参数刻意分开,别合并:
//   - configNames 查体验区配置,配置按**公开名**键控,两个调用点都只能传公开名,
//     否则两处候选集不同,分叉就又回来了;
//   - inferName 只喂最后的名字推断兜底,该用当下能拿到的最准的名字(BuildRequestBody
//     阶段是映射后的上游名,校验阶段只有公开名)。
func taskTypeOfRequest(req *relaycommon.TaskSubmitReq, inferName string, configNames ...string) (string, error) {
	fallback := inferTaskType(inferName)
	if req == nil {
		return fallback, nil
	}
	// 1. 显式声明。
	if v, ok := req.Metadata["task_type"].(string); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v), nil
	}
	// 2/4. 体验区配置声明的候选集。
	cands := common.PlaygroundTaskTypeCandidates(configNames...)
	if len(cands) == 0 {
		return fallback, nil // 未配进体验区(纯直连模型):维持改造前语义
	}
	if len(cands) == 1 {
		return cands[0], nil
	}
	for _, tt := range cands {
		if !videoFamilyTaskTypes[tt] {
			return fallback, nil // 非视频大类不做输入推导,行为不变
		}
	}
	// 3. 用输入形态在候选集里收敛。
	compatible := map[string]bool{}
	for _, tt := range taskTypesCompatibleWithInputs(req) {
		compatible[tt] = true
	}
	var narrowed []string
	for _, tt := range cands {
		if compatible[tt] {
			narrowed = append(narrowed, tt)
		}
	}
	if len(narrowed) == 1 {
		return narrowed[0], nil
	}
	// 输入形态分不开、但模型名带任务标识时按名字收口:wan2.2-flf2v-a14b 收到首帧+尾帧
	// 两张图,i2v/flf2v 都兼容 —— 改造前正是按名字判的,不裁决会把存量直连请求打成 400。
	// 只在 narrowed 里裁决:名字给的答案必须既是该模型声明过的玩法,又与本次输入相容。
	for _, tt := range narrowed {
		if tt == fallback {
			return fallback, nil
		}
	}
	// 判不出来:明确报错,不猜。narrowed 为空说明输入形态不匹配该模型声明的任何玩法
	// (这时**不**拿名字兜底 —— 名字给的答案同样发不得,如只挂「关键帧」的模型收到
	// src_ref_images,按名字会当 t2v 发出去,本来就是错的);多于一个且名字也推不进
	// 候选集,说明这几种玩法的输入形态本就相同(sr/v2a、mv2v/ads2v,或无特征名模型收到
	// 2 张图时的 i2v/flf2v)—— 两种情况都只有调用方自己知道意图。
	ambiguous := narrowed
	if len(ambiguous) == 0 {
		ambiguous = cands
	}
	return "", fmt.Errorf(
		"模型 %s 同时服务多种玩法(%s),本次请求的输入形态无法判定是哪一种:请在 metadata.task_type 里显式指定",
		firstNonEmpty(configNames...), strings.Join(ambiguous, "/"))
}

// inferTaskType 按模型名推断门面 task_type;显式 metadata.task_type 优先于此推断。
func inferTaskType(modelName string) string {
	m := strings.ToLower(modelName)
	switch {
	// 扩散音频(vLLM-Omni audiogen)放最前,免被下面的 tts/兜底吞掉:
	//   AudioX 默认 t2a(文生音效);v2m/tv2m 由 metadata.task_type 显式指定
	//  (v2a/tv2a 已随 AudioX 视频配乐下线,v2a 契约改判给视频配乐,见 validTaskTypes)。
	//   SoulX-Singer 默认 svs(歌声合成)。
	case strings.Contains(m, "audiox"):
		return "t2a"
	// 视频配乐(v2a,LTX-2.3 首发):只匹配任务 token(v2a/dub,如部署名 ltx2-v2a),
	// 不匹配模型家族名——裸 "ltx" 会把 LTX-Video t2v/i2v、ltx2 t2av 等生成类部署
	// 误判成配音。生成类 LTX 落 t2v 兜底;不带任务后缀的配音部署用显式
	// metadata.task_type(优先于本推断)。与 gpustack-ui task-inputs.ts 保持镜像。
	case strings.Contains(m, "v2a") || strings.Contains(m, "dub"):
		return "v2a"
	case strings.Contains(m, "soulx") || strings.Contains(m, "singer"):
		return "svs"
	// 语音合成:含 "tts" 的名字(qwen3-tts/glm-tts/moss-ttsd/indextts)+ vLLM-Omni
	// 里名字不含 "tts" 的 TTS 家族(voxcpm/cosyvoice 克隆、moss-voicegenerator
	// 声音设计、moss-soundeffect 音效)。都走 /v1/audio/speech 异步契约。
	case strings.Contains(m, "tts") || strings.Contains(m, "indextts") ||
		strings.Contains(m, "voxcpm") || strings.Contains(m, "cosyvoice") ||
		strings.Contains(m, "moss"):
		return "tts"
	// MiniMax H3:按 checkpoint 分区名匹配(部署名如 minimax-h3-fl2va /
	// minimax-h3-ref2va),**不要匹配裸 "h3"** —— 误伤面太大(任何带 h3 的名字都会中)。
	// 与 gpustack-ui task-inputs.ts 的 inferVideoTaskType 保持镜像。
	//
	// fl2va 分区同时服务 t2va + fl2va 两种玩法,名字给不出是哪一种,只能给兜底默认
	// t2v ——(与不加分支时的 default 同值,写出来是为了"这是想清楚的选择"而非巧合)。
	// 带图的直连请求**必须**显式声明 metadata.task_type:i2v / l2va / flf2v 三者中
	// i2v 与 l2va 输入形态相同,名字和输入形态都裁决不了(见 taskTypesCompatibleWithInputs)。
	// 配进体验区的走第 2/3 级,到不了这里。
	case strings.Contains(m, "fl2va"):
		return "t2v"
	// ref2va 分区只服务引擎的 ref2va 任务,门面词表里对应 r2va(「参考生视频」)。
	//
	// ⚠️ 注意两层命名别搞混:**ref2va 是引擎的** checkpoint 分区名与 extra_params.task
	// 取值(也是权重目录名),**r2va 是我们门面的** task_type(与 MiniMax 公开 API 的命名
	// 一致)。翻译在门面完成(_H3_TASK_MAP: r2va → ref2va),与 t2v→t2va、
	// i2v/l2va/flf2v→fl2va 是同一套分层。这里匹配的是**模型名里的分区 token**,
	// 返回的是**门面词表值**。
	//
	// 早前这里返回 s2v —— 那是「参考生视频」tab 还不存在、Ref2VA 只能挂数字人时的写法。
	// 现在返回 s2v 会让直连请求走 InfiniteTalk 的物化路径(1 图 + 单值音频),
	// 拿不到多参考能力。gpustack-ui 的同名镜像返回的也是 r2va,两处必须一致。
	case strings.Contains(m, "ref2va"):
		return "r2va"
	// 数字人 / 超分 / 编辑放在通用 i2v/i2i 之前:InfiniteTalk 名里常含 "talk",
	// SeedVR2 含 "seedvr"/"sr",Bernini 视频编辑含 "bernini" —— 显式匹配免落到 t2v 兜底。
	case strings.Contains(m, "infinitetalk") || strings.Contains(m, "s2v"):
		return "s2v"
	// SwiftVR 要单独一个 token:"swiftvr" 三条老判据一条都不中 —— 没有 "seedvr" 子串、
	// 没有 "-sr"、结尾是 "vr" 不是 "sr"。漏了它不会报错,而是静默落到 t2v 兜底,
	// materializeSRInputs 不被调用、源视频压根不物化,超分请求直接走不通。
	case strings.Contains(m, "swiftvr") || strings.Contains(m, "seedvr") ||
		strings.Contains(m, "-sr") || strings.HasSuffix(m, "sr"):
		return "sr"
	// Bernini 一个模型出 v2v/rv2v/r2v 三种玩法,模型名只能给兜底默认(v2v);
	// 真实玩法由前端体验区按输入组合显式下发 metadata.task_type(优先于此推断)。
	case strings.Contains(m, "bernini"):
		return "v2v"
	// 音乐生成:acestep 系模型默认 t2m;cover/repaint 由 metadata.task_type 显式指定。
	case strings.Contains(m, "acestep"):
		return "t2m"
	case strings.Contains(m, "flf2v"):
		return "flf2v"
	case strings.Contains(m, "i2v"):
		return "i2v"
	case strings.Contains(m, "edit") || strings.Contains(m, "i2i"):
		return "i2i"
	case strings.Contains(m, "t2i"):
		return "t2i"
	default:
		return "t2v"
	}
}

// materializeVideoInputs 把视频链路的输入图统一物化落 NFS,返回 input_refs(field → 相对路径数组)。
// 视频链路为 JSON-only:req.Images 里是 URL 或 base64/data-uri 字符串。
// flf2v:images[0]=首帧(image)、images[1]=尾帧(last_frame);i2v/s2v 只取首帧(image)。
// 用 info.PublicTaskID 作 input-group id,info.UserId 作 <user_id> 段(与门面 user_id 一致)。
func materializeVideoInputs(c *gin.Context, info *relaycommon.RelayInfo, taskType, modelName string, req relaycommon.TaskSubmitReq) (map[string][]string, error) {
	if len(req.Images) == 0 {
		return nil, fmt.Errorf("缺少图片输入")
	}
	m := newVideoMaterializer(info, taskType, modelName, req)
	ctx := c.Request.Context()

	// 首帧(image),单值。多输入中途失败时回滚已写文件,避免孤儿(§N2 复审)。
	//
	// l2va(只给尾帧)也走这里,同样写 FieldImage —— **不要改成 FieldLastFrame**。
	// H3 的 FL2VA checkpoint 吃的是「图片列表 + frame_indices」,帧位置由门面回填的
	// extra_params.frame_indices 表达([0] / [-1] / [0,-1]),不靠字段名;而且引擎要求
	// 索引数量与图片数量相等,l2va 是单图单索引 [-1],把它写进 last_frame 反而会变成
	// 「有尾帧没首帧」的畸形输入。
	if err := m.AddString(ctx, nfsinput.FieldImage, 0, false, req.Images[0]); err != nil {
		m.Cleanup()
		return nil, err
	}
	// flf2v 尾帧(last_frame),单值。
	if taskType == "flf2v" {
		if len(req.Images) < 2 {
			m.Cleanup()
			return nil, fmt.Errorf("模型 %s 的任务类型 flf2v(首尾帧)需要首帧和尾帧两张图", modelName)
		}
		if err := m.AddString(ctx, nfsinput.FieldLastFrame, 0, false, req.Images[1]); err != nil {
			m.Cleanup()
			return nil, err
		}
	}
	return m.Refs(), nil
}

// 「参考生视频」(MiniMax H3 Ref2VA)的参考上限,取引擎的真实能力
// (pipeline_minimax_h3._validate_ref2va_reference_counts:≤9 图 + ≤3 视频 +
// ≤3 独立音频,总计 ≤12)。
//
// **对外 API 给全量,体验区自己收窄**:体验区本期只暴露「图 + 单音频」那一档,
// 但那是产品选择,不该把公开接口一起卡死在引擎能力之下。体验区的收窄靠 tab 配置
// 与前端槽位实现,不靠这里。
//
// 与门面 _TASK_INPUT_CAPS["r2va"] 是成对约定,抬上限必须两边同时改 ——
// 只改一边会让请求在另一边被拒。
const (
	maxR2VARefImages = 9
	maxR2VARefVideos = 3
	maxR2VARefAudios = 3
	maxR2VARefTotal  = 12
)

// metadataStringListAny 按给定顺序取第一个非空的多值键。
// metadataStringList 只认单个键,而参考视频/音频要同时兼容单复数两种写法。
func metadataStringListAny(metadata map[string]any, keys ...string) []string {
	for _, k := range keys {
		if v := metadataStringList(metadata, k); len(v) > 0 {
			return v
		}
	}
	return nil
}

// materializeR2VAInputs 物化「参考生视频」(MiniMax H3 Ref2VA)的输入。
//
// **字段名与 doubao/Ark(Seedance 2.0)保持一致,这是刻意的**:体验区的「参考生视频」
// tab 会同时挂自建 H3 与 Seedance 这类第三方模型,若两边各用一套键名,前端就得按渠道
// 分支发不同字段 —— 那是"用户传了音频却静默没生效"的典型来源。统一为:
//
//	metadata.src_ref_images  参考图   (与「图生视频」r2v 同键,doubao 亦读)
//	metadata.reference_videos 参考视频 (doubao 同名,兼容单数 reference_video)
//	metadata.reference_audios 参考音频 (doubao 同名,兼容单数 reference_audio)
//
// 特别不要复用 metadata.video / metadata.audio:那两个键现有的语义是**被加工的素材**
// (SeedVR2 的源视频、InfiniteTalk 的驱动音轨),而这里是**参考**。重载会让两种语义在
// 同一个键上打架,也会让 taskTypesCompatibleWithInputs 的输入形态判定失准。
//
// 也不用顶层 images:doubao 侧对顶层 images 按张数推断 role(1 张 = 首帧),单张参考图
// 会被误判成首帧;src_ref_images 的 role 是固定的 reference_image,无歧义。
//
// 音频语义:H3 的参考音频是**音色/说话风格参考**,不是要逐字复制的现成对白 ——
// 目标台词要在 prompt 里用 <d>[语言] ...</d> 显式写出(vllm-omni 交接文档 §Ref2VA)。
// 这与 s2v(InfiniteTalk 用现成音轨驱动口型)不是一回事,故两者不能合并成一个 task_type。
func materializeR2VAInputs(c *gin.Context, info *relaycommon.RelayInfo, taskType, modelName string, req relaycommon.TaskSubmitReq) (map[string][]string, error) {
	images := metadataStringList(req.Metadata, "src_ref_images")
	// 兼容单复数两种键名:doubao 侧两者都收,这里保持一致,免得同一个 tab 下换个模型
	// 就得改键名。复数优先(它是多值语义的规范形态)。
	videos := metadataStringListAny(req.Metadata, "reference_videos", "reference_video")
	audios := metadataStringListAny(req.Metadata, "reference_audios", "reference_audio")

	if len(images) == 0 && len(videos) == 0 {
		return nil, fmt.Errorf("模型 %s 的任务类型 r2va(参考生视频)需要至少 1 张参考图(metadata.src_ref_images)或 1 个参考视频(metadata.reference_videos)", modelName)
	}
	if len(images) > maxR2VARefImages {
		return nil, fmt.Errorf("模型 %s 的任务类型 r2va(参考生视频)最多 %d 张参考图,收到 %d 张", modelName, maxR2VARefImages, len(images))
	}
	if len(videos) > maxR2VARefVideos {
		return nil, fmt.Errorf("模型 %s 的任务类型 r2va(参考生视频)最多 %d 个参考视频,收到 %d 个", modelName, maxR2VARefVideos, len(videos))
	}
	if len(audios) > maxR2VARefAudios {
		return nil, fmt.Errorf("模型 %s 的任务类型 r2va(参考生视频)最多 %d 段参考音频,收到 %d 段", modelName, maxR2VARefAudios, len(audios))
	}
	// 引擎另有一道跨模态总数闸(≤12)。就地拦下,免得写完 NFS、占了队列槽才被引擎拒。
	if total := len(images) + len(videos) + len(audios); total > maxR2VARefTotal {
		return nil, fmt.Errorf("模型 %s 的任务类型 r2va(参考生视频)参考素材总数最多 %d 个(图+视频+音频),收到 %d 个", modelName, maxR2VARefTotal, total)
	}

	m := newVideoMaterializer(info, taskType, modelName, req)
	ctx := c.Request.Context()

	// 三类都按多值写(multi=true),即便只有 1 个 —— 否则"1 个走单值路径、2 个走多值
	// 路径"会产生两种文件名形态,门面与引擎两边都要分别处理。
	//
	// 落到门面的字段:image→image_path、video→video_path、audio→audio_path,
	// 多值逗号拼接(门面 _MULTI_INPUT_FIELDS + _TASK_INPUT_CAPS["r2va"])。
	for i, img := range images {
		if err := m.AddString(ctx, nfsinput.FieldImage, i, true, img); err != nil {
			m.Cleanup()
			return nil, err
		}
	}
	for i, v := range videos {
		if err := m.AddString(ctx, nfsinput.FieldVideo, i, true, v); err != nil {
			m.Cleanup()
			return nil, err
		}
	}
	// 音色参考可选:不给就由模型自行决定音色(引擎允许纯图/纯视频参考,0024 实测通过)。
	for i, a := range audios {
		if err := m.AddString(ctx, nfsinput.FieldAudio, i, true, a); err != nil {
			m.Cleanup()
			return nil, err
		}
	}
	return m.Refs(), nil
}

// materializeS2VInputs 物化数字人(InfiniteTalk)的输入:人物图(image/input_reference,
// 取首帧)+ 驱动音频(metadata.audio)。两者同一 gid,先写全部 → 再提交,失败回滚。
// 门面把 image→image_path、audio→audio_path 映射给 InfiniteTalk 引擎。
func materializeS2VInputs(c *gin.Context, info *relaycommon.RelayInfo, taskType, modelName string, req relaycommon.TaskSubmitReq) (map[string][]string, error) {
	if len(req.Images) == 0 {
		return nil, fmt.Errorf("模型 %s 的任务类型 s2v(数字人)需要人物图:请提供 image/input_reference", modelName)
	}
	audio := metadataString(req.Metadata, "audio")
	if audio == "" {
		return nil, fmt.Errorf("模型 %s 的任务类型 s2v(数字人)需要驱动音频:请在 metadata.audio 提供音频 URL 或 base64", modelName)
	}
	m := newVideoMaterializer(info, taskType, modelName, req)
	ctx := c.Request.Context()

	if err := m.AddString(ctx, nfsinput.FieldImage, 0, false, req.Images[0]); err != nil {
		m.Cleanup()
		return nil, err
	}
	if err := m.AddString(ctx, nfsinput.FieldAudio, 0, false, audio); err != nil {
		m.Cleanup()
		return nil, err
	}
	return m.Refs(), nil
}

// materializeSRInputs 物化视频超分(SeedVR2)的源视频(metadata.video)。倍率 sr_ratio 不
// 物化——它随 metadata 透传进 body,门面转交引擎(引擎按 config 目标尺寸封顶)。
// 门面把 video→video_path 映射给 SeedVR2 引擎。
func materializeSRInputs(c *gin.Context, info *relaycommon.RelayInfo, taskType, modelName string, req relaycommon.TaskSubmitReq) (map[string][]string, error) {
	video := metadataString(req.Metadata, "video")
	if video == "" {
		return nil, fmt.Errorf("模型 %s 的任务类型 sr(超分)需要源视频:请在 metadata.video 提供视频 URL 或 base64", modelName)
	}
	m := newVideoMaterializer(info, taskType, modelName, req)
	if err := m.AddString(c.Request.Context(), nfsinput.FieldVideo, 0, false, video); err != nil {
		m.Cleanup()
		return nil, err
	}
	return m.Refs(), nil
}

// Foley 抑制句与负向词取自官方模型卡 Lightricks/LTX-2.3-22b-LoRA-Foley-V2A 的示例提示词
// (「A barista uses an espresso machine to steam milk. No speech is present. No music is
// present」)。该 LoRA 的训练字幕就带这两句,补上是在对齐训练分布。
const foleySuppression = "No speech is present. No music is present."

const foleyNegativePrompt = "music, melody, song, singing, vocals, score, soundtrack, beat, rhythm bed, instrumental backing, speech, dialogue"

// withFoleySuppression 在提示词末尾补上 Foley 抑制句;已含则原样返回。
// 空提示词同样补——v2a 收到空 prompt 时最容易配出与画面无关的背景音乐。
func withFoleySuppression(prompt string) string {
	p := strings.TrimSpace(prompt)
	if strings.Contains(strings.ToLower(p), "no music is present") {
		return p
	}
	if p == "" {
		return foleySuppression
	}
	// 中英文句末标点都收掉再接,避免出现「…在林间小路上散步。No speech…」这种断裂。
	return strings.TrimRight(p, "。．.!！?？,，;；、 \t\n") + ". " + foleySuppression
}

// hasKeyFold 忽略大小写与首尾空白判断键是否已存在。metadata 是原样透传的,
// 客户端可能写成 Negative_Prompt,不能只比对精确键名。
func hasKeyFold(m map[string]any, key string) bool {
	for k := range m {
		if strings.EqualFold(strings.TrimSpace(k), key) {
			return true
		}
	}
	return false
}

// materializeDubInputs 物化视频配乐(v2a,LTX-2.3 首发)的源视频(metadata.video)。
// 契约:原视频画面逐帧不动,只补 AI 音轨,产物 mp4。门面把 video→video_path 给引擎;
// prompt(声音描述,可选)/negative_prompt/seed 等标量随 metadata 透传,不物化。
// 与 SR 同走视频大类上限(VideoModelConfig.maxInputMB)。
func materializeDubInputs(c *gin.Context, info *relaycommon.RelayInfo, taskType, modelName string, req relaycommon.TaskSubmitReq) (map[string][]string, error) {
	video := metadataString(req.Metadata, "video")
	if video == "" {
		return nil, fmt.Errorf("模型 %s 的任务类型 v2a(视频配乐)需要源视频:请在 metadata.video 提供视频 URL 或 base64", modelName)
	}
	m := newVideoMaterializer(info, taskType, modelName, req)
	if err := m.AddString(c.Request.Context(), nfsinput.FieldVideo, 0, false, video); err != nil {
		m.Cleanup()
		return nil, err
	}
	return m.Refs(), nil
}

// materializeBerniniInputs 物化 Bernini 视频玩法(视频编辑 + 图生视频)的输入。按输入
// 组合区分(与前端体验区自动分流规则一致):
//   - v2v  :纯提示词编辑,须有且只有 1 个源视频(metadata.src_video),参考图忽略;
//   - rv2v :1 个源视频 + 参考图(metadata.src_ref_images,单串或数组,≤MaxImageRefs);
//   - r2v  :参考图生视频(体验区「图生视频」),1~maxR2VRefImages 张参考图、无源视频;
//   - mv2v :双源视频多源编辑(metadata.src_video 为 2 元数组/逗号串),无参考图;
//   - ads2v:双源视频广告植入 —— 输入与 mv2v 相同(引擎侧 system prompt/guidance
//     不同),自动分流分不出,只能显式 task_type(预置示例带)。
//
// 门面把 src_video/src_ref_images 原样(无 _path 后缀)映射给 Bernini 引擎;多值字段
// 逗号拼接(门面 _MULTI_INPUT_FIELDS,src_video ≤2)。Bernini 无 mask,不处理 src_mask。
func materializeBerniniInputs(c *gin.Context, info *relaycommon.RelayInfo, taskType, modelName string, req relaycommon.TaskSubmitReq) (map[string][]string, error) {
	srcVideos := metadataStringList(req.Metadata, "src_video")
	refImages := metadataStringList(req.Metadata, "src_ref_images")
	if len(refImages) > nfsinput.MaxImageRefs {
		return nil, fmt.Errorf("模型 %s 的 metadata.src_ref_images 最多 %d 张,收到 %d 张", modelName, nfsinput.MaxImageRefs, len(refImages))
	}
	if len(srcVideos) > maxBerniniSrcVideos {
		return nil, fmt.Errorf("模型 %s 的 metadata.src_video 最多 %d 个视频,收到 %d 个", modelName, maxBerniniSrcVideos, len(srcVideos))
	}
	// 按 task_type 精确校验输入(前端已按输入组合分流,这里是服务端兜底,防直连绕过)。
	//
	// v2v/mv2v/ads2v 一并拒收参考图:下面物化时它们只写源视频(见 rv2v/r2v 的分支条件),
	// 参考图会被静默丢弃 —— 与 i2v 多图那条防呆同一个理由,宁可 400 也不要静默降级。
	// 这里而不是 taskTypesCompatibleWithInputs 里拦:显式 metadata.task_type 与「候选集
	// 只剩一个」的快捷路径都不看谓词,只有落在物化前才拦得全。
	switch taskType {
	case "v2v":
		if len(srcVideos) != 1 {
			return nil, fmt.Errorf("模型 %s 的任务类型 v2v(视频编辑)需要且只需要 1 个源视频(metadata.src_video);两个视频请用 mv2v/ads2v", modelName)
		}
		if len(refImages) != 0 {
			return nil, fmt.Errorf("模型 %s 的任务类型 v2v(视频编辑)不接受参考图(metadata.src_ref_images);带参考图请用 rv2v", modelName)
		}
	case "rv2v":
		if len(srcVideos) != 1 || len(refImages) == 0 {
			return nil, fmt.Errorf("模型 %s 的任务类型 rv2v(参考视频编辑)需要 1 个源视频(metadata.src_video)和至少 1 张参考图(metadata.src_ref_images)", modelName)
		}
	case "r2v":
		if len(refImages) == 0 || len(refImages) > maxR2VRefImages {
			return nil, fmt.Errorf("模型 %s 的任务类型 r2v(图生视频)需要 1~%d 张参考图(metadata.src_ref_images)", modelName, maxR2VRefImages)
		}
		if len(srcVideos) != 0 {
			return nil, fmt.Errorf("模型 %s 的任务类型 r2v(图生视频)不接受源视频;含源视频的编辑请用 v2v/rv2v", modelName)
		}
	case "mv2v", "ads2v":
		if len(srcVideos) != 2 {
			return nil, fmt.Errorf("模型 %s 的任务类型 %s 需要恰好 2 个源视频(metadata.src_video 数组);单视频编辑请用 v2v/rv2v", modelName, taskType)
		}
		if len(refImages) != 0 {
			return nil, fmt.Errorf("模型 %s 的任务类型 %s 只用源视频,不接受参考图(metadata.src_ref_images);带参考图的编辑请用 rv2v/r2v", modelName, taskType)
		}
	default:
		return nil, fmt.Errorf("模型 %s 的视频编辑任务类型 %s 不支持", modelName, taskType)
	}
	m := newVideoMaterializer(info, taskType, modelName, req)
	ctx := c.Request.Context()

	// 先写全部输入 → 再提交,任一路失败回滚已写文件避免孤儿。
	multiVideo := len(srcVideos) > 1
	for i, v := range srcVideos {
		if err := m.AddString(ctx, nfsinput.FieldSrcVideo, i, multiVideo, v); err != nil {
			m.Cleanup()
			return nil, err
		}
	}
	// v2v/mv2v/ads2v 只用源视频(参考图忽略);rv2v/r2v 物化参考图。
	if taskType == "rv2v" || taskType == "r2v" {
		for i, img := range refImages {
			if err := m.AddString(ctx, nfsinput.FieldSrcRefImages, i, true, img); err != nil {
				m.Cleanup()
				return nil, err
			}
		}
	}
	return m.Refs(), nil
}

// Bernini 输入基数上限(服务端兜底,与门面/产品约定对齐):
// maxBerniniSrcVideos 与门面 _MAX_INPUT_VIDEOS 一致(mv2v/ads2v 双视频);
// maxR2VRefImages 是「图生视频」产品档位(验证报告:5 张可控、8 张难控,产品定 3)。
const (
	maxBerniniSrcVideos = 2
	maxR2VRefImages     = 3
)

// inputGroupID 取本次请求的 input-group id:优先 info.PublicTaskID,空则新 uuid。
func inputGroupID(info *relaycommon.RelayInfo) string {
	if gid := strings.TrimSpace(info.PublicTaskID); gid != "" {
		return gid
	}
	return common.GetUUID()
}

// newVideoMaterializer 构造视频输入物化器,并按 VideoModelConfig 的 maxInputMB 设置单文件
// 大小上限(吃上传的 i2v/flf2v/s2v/sr/vace 通用护栏;0/未配=不限;服务端兜底防直连绕过前端)。
func newVideoMaterializer(info *relaycommon.RelayInfo, taskType, modelName string, req relaycommon.TaskSubmitReq) *nfsinput.Materializer {
	m := nfsinput.NewMaterializer(taskType, modelName, fmt.Sprintf("%d", info.UserId), inputGroupID(info))
	if maxBytes, ok := common.VideoMaxInputBytesForModel(taskType, req.Model, info.OriginModelName, modelName); ok {
		m.SetMaxBytes(maxBytes)
	}
	// 音频时长上限:与体积上限正交。物化层按字段判定,只有 s2v 会写音频字段(见
	// materializeS2VInputs),其余任务共用本构造器也不受影响,故无需按 taskType 分流。
	if maxSec, ok := common.VideoMaxAudioSecForModel(taskType, req.Model, info.OriginModelName, modelName); ok {
		m.SetMaxAudioSeconds(maxSec)
	}
	return m
}

// metadataStringList 从 metadata 取一个字符串列表:支持数组([]any 里的字符串)、
// 逗号分隔的单串、或单个字符串。用于 VACE 的 src_ref_images(可多张)。
func metadataStringList(md map[string]any, key string) []string {
	if md == nil {
		return nil
	}
	v, ok := md[key]
	if !ok {
		return nil
	}
	var out []string
	switch t := v.(type) {
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			break
		}
		// A data URL carries a comma in its own payload (data:...;base64,XXXX),
		// so never comma-split it — treat the whole string as one image. Only
		// plain URL/path lists are comma-separated; multiple data URLs must be
		// sent as a JSON array (handled by the []any/[]string cases below).
		if strings.HasPrefix(s, "data:") {
			out = append(out, s)
		} else {
			for _, part := range strings.Split(s, ",") {
				if p := strings.TrimSpace(part); p != "" {
					out = append(out, p)
				}
			}
		}
	case []any:
		for _, e := range t {
			if s, ok := e.(string); ok {
				if s = strings.TrimSpace(s); s != "" {
					out = append(out, s)
				}
			}
		}
	case []string:
		for _, s := range t {
			if s = strings.TrimSpace(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// metadataString 从请求 metadata 里安全取一个字符串值(容忍 nil / 非字符串)。
func metadataString(md map[string]any, key string) string {
	if md == nil {
		return ""
	}
	if v, ok := md[key]; ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// materializeTTSInputs 物化 IndexTTS-2 情感合成的参考音色(必填,metadata.voice)与可选
// 情感参考音(metadata.emotion_audio),返回 input_refs(field → 相对路径)。voice /
// emotion_audio 是 URL 或 base64/data-uri 音频字符串,与视频输入复用同一物化机制。
// IndexTTS-2 现由 vLLM-Omni 引擎服务(取代独立 IndexTTS),引擎读 ref_audio/emo_audio,
// 故:voice→ref_audio(门面映射 ref_audio→ref_audio_path)、emotion_audio→emo_audio_path
// (引擎 AudioTaskRequest 折叠 emo_audio_path→emo_audio)。情感向量/强度(emo_vector/
// emo_alpha)是标量,不在此物化——由 foldEmotionParamsIntoExtra 收进 body.extra_params。
func materializeTTSInputs(c *gin.Context, info *relaycommon.RelayInfo, taskType, modelName string, req relaycommon.TaskSubmitReq) (map[string][]string, error) {
	voice := metadataString(req.Metadata, "voice")
	if voice == "" {
		return nil, fmt.Errorf("模型 %s 的任务类型 tts 需要参考音色:请在 metadata.voice 提供音频 URL 或 base64", modelName)
	}
	m := nfsinput.NewMaterializer(taskType, modelName, fmt.Sprintf("%d", info.UserId), inputGroupID(info))
	// 参考音大小上限(AudioModelConfig,按模型/全局默认;0=不限):服务端兜底,防直连绕过
	// 前端上传限制(校验 base64 解码后 / URL 下载后的字节数,见 nfsinput.addBytesExt)。
	if maxBytes, ok := common.AudioRefAudioMaxBytesForModel(taskType, req.Model, info.OriginModelName, modelName); ok {
		m.SetMaxBytes(maxBytes)
	}
	ctx := c.Request.Context()

	// 参考音色(必填),单值;失败回滚。物化为 ref_audio(vLLM-Omni 引擎的克隆参考音字段)。
	if err := m.AddString(ctx, nfsinput.FieldRefAudio, 0, false, voice); err != nil {
		m.Cleanup()
		return nil, err
	}
	// 情感参考音(可选),单值。
	if emo := metadataString(req.Metadata, "emotion_audio"); emo != "" {
		if err := m.AddString(ctx, nfsinput.FieldEmotionAudio, 0, false, emo); err != nil {
			m.Cleanup()
			return nil, err
		}
	}
	return m.Refs(), nil
}

// materializeAudioXVideoInputs 物化 AudioX 视频→音乐(v2m/tv2m)的源视频
// (metadata.video)。门面把 video→video_path 映射给引擎(AudioX 用 av.open 读裸路径,无需
// file://)。audiox_task/seconds_total/num_inference_steps 等标量随 metadata 透传,不物化。
// 注:v2a/tv2a 已随 AudioX 视频配乐下线;v2a 契约改判给视频配乐,走 materializeDubInputs。
func materializeAudioXVideoInputs(c *gin.Context, info *relaycommon.RelayInfo, taskType, modelName string, req relaycommon.TaskSubmitReq) (map[string][]string, error) {
	video := metadataString(req.Metadata, "video")
	if video == "" {
		return nil, fmt.Errorf("模型 %s 的任务类型 %s(视频→音频/音乐)需要源视频:请在 metadata.video 提供视频 URL 或 base64", modelName, taskType)
	}
	// AudioX 归「音乐」大类,视频上限配在 MusicModelConfig.videoMaxMB(不是 VideoModelConfig)
	// —— 故不用 newVideoMaterializer(读视频模型配置),改直接建物化器 + 音乐视频上限兜底。
	m := nfsinput.NewMaterializer(taskType, modelName, fmt.Sprintf("%d", info.UserId), inputGroupID(info))
	if maxBytes, ok := common.MusicVideoMaxBytesForModel(req.Model, info.OriginModelName, modelName); ok {
		m.SetMaxBytes(maxBytes)
	}
	if err := m.AddString(c.Request.Context(), nfsinput.FieldVideo, 0, false, video); err != nil {
		m.Cleanup()
		return nil, err
	}
	return m.Refs(), nil
}

// materializeSingingInputs 物化 SoulX-Singer SVS 集成 preprocess 的输入:prompt_audio(音色
// 参考人声,必填)+ target_audio(目标曲/伴奏,必填)。服务器内联抽歌词/音符/音高,免预计算
// 元数据。门面把 prompt_audio/target_audio 原样(引擎 extra_args 同名键)映射给引擎;
// language/control/num_inference_steps 等标量随 metadata 透传。
func materializeSingingInputs(c *gin.Context, info *relaycommon.RelayInfo, taskType, modelName string, req relaycommon.TaskSubmitReq) (map[string][]string, error) {
	promptAudio := metadataString(req.Metadata, "prompt_audio")
	targetAudio := metadataString(req.Metadata, "target_audio")
	if promptAudio == "" || targetAudio == "" {
		return nil, fmt.Errorf("模型 %s 的任务类型 svs(歌声合成)需要 metadata.prompt_audio(音色参考)与 metadata.target_audio(目标曲/伴奏)", modelName)
	}
	m := nfsinput.NewMaterializer(taskType, modelName, fmt.Sprintf("%d", info.UserId), inputGroupID(info))
	// SoulX 归「音乐」大类,参考音上限配在 MusicModelConfig.refAudioMaxMB(不是 AudioModelConfig)。
	if maxBytes, ok := common.MusicRefAudioMaxBytesForModel(taskType, req.Model, info.OriginModelName, modelName); ok {
		m.SetMaxBytes(maxBytes)
	}
	ctx := c.Request.Context()
	if err := m.AddString(ctx, nfsinput.FieldPromptAudio, 0, false, promptAudio); err != nil {
		m.Cleanup()
		return nil, err
	}
	if err := m.AddString(ctx, nfsinput.FieldTargetAudio, 0, false, targetAudio); err != nil {
		m.Cleanup()
		return nil, err
	}
	return m.Refs(), nil
}

// IsOmniTTSModel 判断 tts 任务的模型是否由 vLLM-Omni 引擎服务(区别于旧 IndexTTS)。
// 二者共用 task_type=tts,但参考音契约不同:IndexTTS 用必填 voice→spk_audio_path;
// vLLM-Omni 用可选 ref_audio/ref_audio_2 + 标量 speaker 预设音色。按模型名前缀区分
// (indextts 走旧路径,其余 TTS 家族走 Omni)。与 inferTaskType 的 tts 判定同源。
//
// 导出供同步语音链路(relay/channel/gpustackplus/speech.go)复用:两条链路必须对
// 「预设音色 vs 参考音」用同一套判定,否则同一个模型走 /v1/audio/speech 与
// /v1/videos 会得到不同的音色语义。
func IsOmniTTSModel(modelName string) bool {
	m := strings.ToLower(modelName)
	if strings.Contains(m, "indextts") {
		return false
	}
	for _, k := range []string{
		"qwen3-tts", "voxcpm", "cosyvoice", "glm-tts", "moss",
	} {
		if strings.Contains(m, k) {
			return true
		}
	}
	return false
}

// materializeOmniTTSInputs 物化 vLLM-Omni TTS 的参考音输入(全部可选):
//   - ref_audio:克隆参考音(VoxCPM2/CosyVoice3 零样本克隆、MOSS-TTSD 说话人一),单值;
//   - ref_audio_2:MOSS-TTSD 双人对话第二说话人参考音,单值(需与 ref_audio 同时给)。
//
// 预设音色(Qwen3-TTS/GLM-TTS)走标量 metadata.speaker 透传,不在此物化;声音设计
// (MOSS-VoiceGenerator)与音效(MOSS-SoundEffect)纯文本,无参考音。因此本函数可能返回
// nil(无参考音输入),与 IndexTTS 的 voice 必填不同。门面把 ref_audio→ref_audio_path、
// ref_audio_2→ref_audio_2_path 注入引擎,引擎再转 file:// URI 交给 speech handler。
func materializeOmniTTSInputs(c *gin.Context, info *relaycommon.RelayInfo, taskType, modelName string, req relaycommon.TaskSubmitReq) (map[string][]string, error) {
	refAudio := metadataString(req.Metadata, "ref_audio")
	refAudio2 := metadataString(req.Metadata, "ref_audio_2")
	if refAudio == "" {
		if refAudio2 != "" {
			return nil, fmt.Errorf("模型 %s 提供了 ref_audio_2 却缺少 ref_audio:双人对话需先给第一说话人参考音", modelName)
		}
		return nil, nil // 预设音色 / 声音设计 / 音效:无参考音输入
	}
	m := nfsinput.NewMaterializer(taskType, modelName, fmt.Sprintf("%d", info.UserId), inputGroupID(info))
	if maxBytes, ok := common.AudioRefAudioMaxBytesForModel(taskType, req.Model, info.OriginModelName, modelName); ok {
		m.SetMaxBytes(maxBytes)
	}
	ctx := c.Request.Context()

	// 克隆参考音(单值);失败回滚。
	if err := m.AddString(ctx, nfsinput.FieldRefAudio, 0, false, refAudio); err != nil {
		m.Cleanup()
		return nil, err
	}
	// 第二说话人参考音(可选,MOSS-TTSD),单值。
	if refAudio2 != "" {
		if err := m.AddString(ctx, nfsinput.FieldRefAudio2, 0, false, refAudio2); err != nil {
			m.Cleanup()
			return nil, err
		}
	}
	return m.Refs(), nil
}

// materializeMusicInputs 物化音乐生成(ACE-Step)的音频输入:t2m 无输入(纯 prompt);
// cover 需参考音频(metadata.reference_audio);repaint 需源音频(metadata.src_audio)。
// 门面把 reference_audio → reference_audio_path、src_audio → src_audio_path 映射给引擎。
func materializeMusicInputs(c *gin.Context, info *relaycommon.RelayInfo, taskType, modelName string, req relaycommon.TaskSubmitReq) (map[string][]string, error) {
	if taskType == "t2m" {
		return nil, nil // 纯文本生成,无音频输入
	}
	var field nfsinput.Field
	var meta, label string
	switch taskType {
	case "cover":
		field, meta, label = nfsinput.FieldReferenceAudio, "reference_audio", "cover(覆盖生成)需要参考音频:请在 metadata.reference_audio"
	case "repaint":
		field, meta, label = nfsinput.FieldSrcAudio, "src_audio", "repaint(音乐重绘)需要源音频:请在 metadata.src_audio"
	default:
		return nil, fmt.Errorf("模型 %s 的音乐任务类型 %s 不支持", modelName, taskType)
	}
	audio := metadataString(req.Metadata, meta)
	if audio == "" {
		return nil, fmt.Errorf("模型 %s 的任务类型 %s 提供音频 URL 或 base64", modelName, label)
	}
	m := nfsinput.NewMaterializer(taskType, modelName, fmt.Sprintf("%d", info.UserId), inputGroupID(info))
	// 参考音/源音大小上限(MusicModelConfig,按模型/全局默认;0=不限):服务端兜底,防直连绕过。
	if maxBytes, ok := common.MusicRefAudioMaxBytesForModel(taskType, req.Model, info.OriginModelName, modelName); ok {
		m.SetMaxBytes(maxBytes)
	}
	// 单值音频(必填),失败回滚。
	if err := m.AddString(c.Request.Context(), field, 0, false, audio); err != nil {
		m.Cleanup()
		return nil, err
	}
	return m.Refs(), nil
}

func (a *TaskAdaptor) DoRequest(c *gin.Context, info *relaycommon.RelayInfo, requestBody io.Reader) (*http.Response, error) {
	return channel.DoTaskApiRequest(a, c, info, requestBody)
}

func (a *TaskAdaptor) DoResponse(c *gin.Context, resp *http.Response, info *relaycommon.RelayInfo) (taskID string, taskData []byte, taskErr *dto.TaskError) {
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		taskErr = service.TaskErrorWrapper(err, "read_response_body_failed", http.StatusInternalServerError)
		return
	}
	_ = resp.Body.Close()

	var sr submitResponse
	if err := common.Unmarshal(responseBody, &sr); err != nil {
		taskErr = service.TaskErrorWrapper(errors.Wrapf(err, "body: %s", responseBody), "unmarshal_response_body_failed", http.StatusInternalServerError)
		return
	}
	if sr.TaskID == "" {
		taskErr = service.TaskErrorWrapper(fmt.Errorf("upstream task_id is empty, body: %s", responseBody), "invalid_response", http.StatusInternalServerError)
		return
	}

	// 返回给客户端 OpenAI 兼容 video 对象(用公开 task_xxxx ID)。
	ov := dto.NewOpenAIVideo()
	ov.ID = info.PublicTaskID
	ov.TaskID = info.PublicTaskID
	ov.Model = info.OriginModelName
	ov.CreatedAt = time.Now().Unix()
	c.JSON(http.StatusOK, ov)

	return sr.TaskID, responseBody, nil
}

func (a *TaskAdaptor) FetchTask(baseUrl, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskID, ok := body["task_id"].(string)
	if !ok || taskID == "" {
		return nil, fmt.Errorf("invalid task_id")
	}
	uri := fmt.Sprintf("%s/v1/videos/%s", strings.TrimRight(baseUrl, "/"), taskID)
	req, err := http.NewRequest(http.MethodGet, uri, nil)
	if err != nil {
		return nil, err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	client, err := service.GetHttpClientWithProxy(proxy)
	if err != nil {
		return nil, fmt.Errorf("new proxy http client failed: %w", err)
	}
	return client.Do(req)
}

func (a *TaskAdaptor) ParseTaskResult(respBody []byte) (*relaycommon.TaskInfo, error) {
	var sr statusResponse
	if err := common.Unmarshal(respBody, &sr); err != nil {
		return nil, errors.Wrap(err, "unmarshal task result failed")
	}
	ti := &relaycommon.TaskInfo{Code: 0, TaskID: sr.TaskID}

	// 门面状态机:queued(排队/等重派)→ assigned(已派发实例)→ running → done;
	// failed/canceled 终态。旧引擎态(pending/processing/completed)保留兼容。
	switch strings.ToLower(strings.TrimSpace(sr.Status)) {
	case "queued", "assigned", "pending", "submitted":
		ti.Status = model.TaskStatusQueued
	case "running", "processing", "in_progress":
		ti.Status = model.TaskStatusInProgress
		// 只在真拿到进度时才覆盖固定档位:门面返回 0 既可能是"刚开始"也可能是
		// "老版本门面没这个字段",两种都该退回 ProgressInProgress 的 30%。
		if sr.Progress > 0 {
			ti.Progress = scaleProgress(sr.Progress)
		}
	case "done", "completed", "succeed", "success":
		ti.Status = model.TaskStatusSuccess
		// 关键:把成品在 SFS 上的绝对路径交给落盘钩子(显式 nfs_path,非启发式)。
		ti.NFSPath = sr.NFSPath
	case "failed", "cancelled", "canceled", "error":
		ti.Status = model.TaskStatusFailure
		ti.Reason = firstNonEmpty(sr.Error, sr.ErrorType, "task failed")
	default:
		// 未知/空状态:保持排队,交后续轮询与超时兜底,避免误杀刚提交的任务。
		if strings.TrimSpace(sr.Status) != "" {
			common.SysLog(fmt.Sprintf("[gpustackplus] unrecognized task status %q, body: %s", sr.Status, string(respBody)))
		}
		ti.Status = model.TaskStatusQueued
	}
	return ti, nil
}

// ConvertToOpenAIVideo 供 /v1/videos/:id 查询走 OpenAI 兼容格式;url metadata 里的
// 结果链接由 model.Task.ToOpenAIVideo 经 ResolveResultURL 实时签成 OBS URL。
func (a *TaskAdaptor) ConvertToOpenAIVideo(task *model.Task) ([]byte, error) {
	ov := task.ToOpenAIVideo()
	data, err := common.Marshal(ov)
	if err != nil {
		return nil, errors.Wrap(err, "marshal openai video failed")
	}
	return data, nil
}

func (a *TaskAdaptor) GetModelList() []string {
	return ModelList
}

func (a *TaskAdaptor) GetChannelName() string {
	return ChannelName
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
