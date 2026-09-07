package common

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
)

// 参考素材与时长的**唯一取值口径**。
//
// 存在的理由:同一个请求要被两处读 —— 生成段(适配器物化输入)和增强段(中间件给增强
// 模型编事实)。这两处过去各自手工解读 metadata,于是每加一种合法编码就分裂一次:
// 逗号分隔的单串、单复数键名、duration 的 seconds 回落 —— 每一种都出现过
// 「生成段读得到、增强段读不到」,而症状全是**不报错**:片子照常出、账照常记,
// 只有改写出的提示词描述的是另一个输入。
//
// 三轮检视抓到的都是这一类。根治办法是把口径收敛到请求对象自己身上:新增一种合法
// 编码时只改这里,两处自动跟上。调用方不要再直接读 Metadata 里的这几个键。
//
// 键名本身是平台契约,与 doubao/Ark 对齐(见 materializeR2VAInputs 的字段注释):
// 参考图 src_ref_images、参考视频 reference_videos(单数 reference_video 亦收)、
// 参考音频 reference_audios(单数亦收)。

// RefImages 参考图。R2VA 与 R2V 共用同一个键,与首帧图(顶层 images)是**不同的键** ——
// "都是一张图"不代表分得开,混用会让输入形态判定失准。
func (t *TaskSubmitReq) RefImages() []string {
	if t == nil {
		return nil
	}
	return common.MetadataStringList(t.Metadata, "src_ref_images")
}

// RefVideos 参考视频,兼收单复数键名。
func (t *TaskSubmitReq) RefVideos() []string {
	if t == nil {
		return nil
	}
	return common.MetadataStringListAny(t.Metadata, "reference_videos", "reference_video")
}

// RefAudios 参考音频,兼收单复数键名。
func (t *TaskSubmitReq) RefAudios() []string {
	if t == nil {
		return nil
	}
	return common.MetadataStringListAny(t.Metadata, "reference_audios", "reference_audio")
}

// TaskType 显式指定的任务类型(metadata.task_type);未指定返回空串。
//
// 只读显式值,不做任何推断 —— 推断是适配器四级解析链的职责(见 taskTypeOfRequest),
// 那里要看渠道配置与输入形态,不是请求对象一个人能决定的。
func (t *TaskSubmitReq) TaskType() string {
	if t == nil || t.Metadata == nil {
		return ""
	}
	s, _ := t.Metadata["task_type"].(string)
	return s
}

// EffectiveDuration 实际生效的时长(秒)。
//
// ⚠️ **只有确实会读 Seconds 的渠道才能调**,判据同 VideoSecondsFallback:
// gpustackplus 明确「Duration 为 0 时回落 Seconds」,而 kling/vidu/jimeng 完全忽略它 ——
// 对后者用这个回落,拿到的是一个上游根本不会采纳的时长。计费侧对同一件事设了渠道闸
// (video_billing.go 的 videoBillingSeconds 先判 TaskPlatform),这里不设闸是因为
// 请求对象拿不到渠道,约束只能靠调用方遵守。
//
// 渠道未知的调用方(如聚合展开中间件,它跑在选渠道**之前**)不要用这个方法,
// 直接读 Duration。
func (t *TaskSubmitReq) EffectiveDuration() int {
	if t == nil {
		return 0
	}
	if t.Duration > 0 {
		return t.Duration
	}
	return VideoSecondsFallback(t)
}

// FrameImages 顶层的条件图(首帧 / 首尾帧 / 数字人形象图等),按平台的**回落优先级**取值:
// images 非空即用它,否则回落 image,再否则回落 input_reference。
//
// **是回落不是并集**。公共校验(relay/common/relay_utils.go)与 gpustackplus 适配器
// 都只在 Images 为空时才填另外两个键 —— 调用方同时给了 images 和 image 时,生成段
// 只看 images。若在别处取并集,同一张图会被数两遍:增强段据此编出的 <Picture N> 标号
// 整体后移,flf2v 会把"<Picture 2> 是尾帧"指到重复的首帧上,对齐指令正好写反,
// 而生成段对多余的别名毫无反应 —— 全程不报错。
func (t *TaskSubmitReq) FrameImages() []string {
	if t == nil {
		return nil
	}
	if len(t.Images) > 0 {
		return t.Images
	}
	if s := strings.TrimSpace(t.Image); s != "" {
		return []string{s}
	}
	if s := strings.TrimSpace(t.InputReference); s != "" {
		return []string{s}
	}
	return nil
}
