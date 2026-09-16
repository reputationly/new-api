// 编译这次请求需要的**结构化事实**。
//
// 这些事实由请求侧判定、不让模型猜:玩法是从客户端传了哪个字段推出来的
// (见 frames.go 的 ResolveFrameRoles),素材的角色与官方标号也一样。
// 模型看图看不出"这张是尾帧",那是客户端用哪个字段装它决定的。
//
// 消费方是 service/aggregate_enhance_singlecall.go —— 它把这些事实编成
// evidence JSON 交给上游 v20 编译器。
package hilo

import (
	"fmt"
	"strings"
)

type CompilerInput struct {
	// UserRequest 用户的原始提示词，逐字。
	UserRequest string
	// TaskType 平台的 task_type，由 ResolveFrameRoles 定，不由模型决定。
	TaskType TaskType
	// DurationSeconds 请求时长。镜头时长加起来必须等于它。
	DurationSeconds float64
	// GenerateAudio 要不要出声。
	GenerateAudio bool
	// Assets 本次的素材，已经按角色标好。
	Assets []CompilerAsset
}

// CompilerAsset 一个素材及其**已经确定的**角色。
//
// 角色由 ResolveFrameRoles 判定（单一来源），不让模型自己猜 —— 它推不出
// "这张图是尾帧"，那是客户端用哪个字段装它决定的。
type CompilerAsset struct {
	AssetID   string
	MediaType string // image | video | audio
	Role      string // first_frame | last_frame | reference
	URL       string
}

// describe 把这些事实编成一段纯文本。
//
// **目前只有测试在用。** 生产侧的消费者(singlecall)自己拼 evidence JSON,
// 不走这个格式。留着是因为它是"该给模型看什么、不该给什么"这条规则的
// 可执行说明 —— 角色要给、URL 不能给,两条都由测试钉住。下一个文本形态的
// 消费者出现时直接可用;若一直没有,连同它的测试一起删掉即可。
func (in CompilerInput) describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "user_request: %s\n", strings.TrimSpace(in.UserRequest))
	fmt.Fprintf(&b, "task.type: %s\n", in.TaskType)
	fmt.Fprintf(&b, "task.duration_seconds: %g\n", in.DurationSeconds)
	fmt.Fprintf(&b, "task.generate_audio: %t\n", in.GenerateAudio)
	if len(in.Assets) == 0 {
		b.WriteString("assets: (none)\n")
		return b.String()
	}
	b.WriteString("assets:\n")
	for _, a := range in.Assets {
		// **只给 asset_id 和角色，不给 URL。**
		//
		// 素材本身是随消息一起发给模型看的（多模态），URL 写进文字里
		// 只会让它把那串地址抄进描述。角色必须给 —— 模型看图看不出
		// "这张是尾帧"。
		fmt.Fprintf(&b, "  - asset_id: %s, media_type: %s", a.AssetID, a.MediaType)
		if a.Role != "" {
			fmt.Fprintf(&b, ", role: %s", a.Role)
		}
		b.WriteString("\n")
	}
	return b.String()
}
