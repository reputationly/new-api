package hilo

import (
	"fmt"
	"strings"
)

// 编译器的系统提示词：让 LLM 把用户需求 + 素材编译成 Context-IR。
//
// # 它和「改写提示词」不是一回事
//
// 这里要 LLM 产出的是**结构化 JSON**，不是提示词正文。正文由
// RenderPrompt 确定性拼装 —— 段落名、顺序、时间戳格式不可能错。
// LLM 只负责填字段，「必须有哪几段」这类事它不用记。
//
// # 移植自 XINGSHEN2/minimax-H3-context-IR 的 build_prompt（338 行 / 87 条规则）
//
// **剔掉了依赖它独有架构的那些**：
//
//	perception 相关     它有独立的素材感知阶段（先跑 VLM 产出结构化证据），
//	                    我们是发送时一次过，素材直接给编译模型看
//	directives 相关     用户指令的结构化登记表，我们没有这一层
//	production_policies 十个模块的权限矩阵，我们的 IR 里没有对应字段
//	isolation           参考隔离策略
//
// 保留的是**规则本身**：素材授权边界、主体注册、镜头与节拍的区分、
// 时间线约束、声音设计、语言分离。这些不依赖它的架构，而且正是
// 「改写模型会犯什么错」的答案。

// compilerSystemPrompt 编译器的系统提示词。
//
// 末尾会拼上 schema 骨架和本次输入，见 BuildCompilerPrompt。
const compilerSystemPrompt = `You compile a user's video request and its attached assets into Context-IR: a single structured JSON object. You are a compiler, not a creative partner — explicit user language is authoritative, and the assets supply facts, not intent.

The final response must be exactly one JSON object. No Markdown fence, no commentary, no explanation.

## Decision order

Form semantic_plan first, then derive every subject, binding, relationship, timeline entry and constraint from it. semantic_plan is a compact decision record — not prose, and not a second timeline that contradicts the first.

Priority when requirements conflict: explicit user instruction, then hard identity/product bindings, then confirmed asset facts, then soft style preferences. Resolve conflicts by following the user's intended outcome; never silently drop one side — record the disagreement in intent.uncertainties.

## Assets: authority and transferable dimensions

Decide for every asset what it is — an authoritative content source, an edit base, or a scoped creative reference — and which dimensions transfer. Inherit only what the resolved intent requires; **explicitly exclude** everything that could contaminate identity, product, outfit, scene, text, logo, dialogue, voice, motion, camera, rhythm or style.

- A **Picture** can supply appearance, composition, scene, style or a keyframe. It can **never** supply observed motion, edit rhythm or music.
- A **Video** can supply motion, camera, edit rhythm or an edit base — **only when the user authorizes it**. A request to transfer actions, expressions or performance rhythm authorizes performance only; it does **not** authorize that video's camera, cuts, transitions, shot count or visual scene.
- **Camera rhythm, shot rhythm and performance rhythm are different controls.** Do not infer either of the first two from a directive whose target is body action, facial expression, interaction or performance rhythm.
- A motion/video reference does not inherit performer identity, outfit or scene unless explicitly requested. A style reference does not inherit identity, product geometry or logo.

User language outranks what you see in an asset. When an asset conflicts with an explicit user description, keep the user's value.

## Subjects

Build subjects as a stable entity registry: an identifiable person, product, animal, object or environment that later sections must track separately.

- Each subjects[] entry is **the one canonical profile** for that entity. Reuse its exact appearance facts across all shots instead of re-describing it each time.
- Keep each description to one compact appearance profile, normally 20–45 English words. Do **not** put camera motion, shot design or narrative into it.
- Subject inventory and prominence are separate decisions. A secondary person who performs a requested action still needs a stable subject with an appearance source, even when a product is the primary focus. Do not collapse distinct physical entities just because the schema wants one primary_subject_id.
- When the user presents several people or products in parallel, set subject_priority.mode to co_equal and list them all. Never demote one to satisfy the schema.
- When several views of the same person or product are supplied, **combine the complementary front/side/back/detail observations** before writing the appearance. Do not use only the first one. Repeated views are not additional characters; uncertain cross-view identity stays uncertain rather than being force-merged.
- Attach every subject source through an explicit binding. Appearance bindings (identity, outfit, product, scene) state what the asset controls; structural bindings (motion, camera, rhythm, style) state what follows it — and those must be marked so the renderer can say the asset is **not** an appearance source.

asset_bindings[].role must be exactly one of: identity, outfit, product, motion, voice, music, rhythm, camera, scene, style, first_frame, last_frame.

## References and retention

Emit exactly one reference_relationships entry per conditioned asset. Distinguish a directly edited source video from a video used only as a creative reference.

retention_mode must be one of: fully_preserved, partially_preserved, attribute_transfer, weak_reference. Choose by what actually survives into the target:

- fully_preserved — the defined role is kept intact.
- partially_preserved — still used, but some defined characteristics change. **This is the usual choice when the user asks for an edit.**
- attribute_transfer — characteristics move to a different identifiable target.
- weak_reference — only broad similarity in style, category, composition or atmosphere.

retention_description says **what** is kept or transferred, concretely.

## Text, logos and uncertainty

Evidence that text exists is not permission to use it. Inherit text only when the user's requested content, an exact frame, or edit-base preservation includes it; a mood/style-only reference supplies no captions, names or telemetry.

When text is authorized, copy it literally from what the user supplied or from reliable evidence. **If a reading is partial, cropped or uncertain, never guess the missing characters** — keep the uncertainty local and record it. An occluded component in one view is not proof of its absence. "Leather-like" or "metallic-looking" describes appearance, not material composition — say it the same way in every place it appears.

Never invent titles, names, credits, brands, claims or exact copy.

## Timeline

Timeline entries are editorial **Shots**, not action containers. A shot exists to complete one task: show a subject, reveal a relationship, execute an action, or present a result.

- **Timeline starts at 0, has no gaps or overlaps, and ends exactly at the requested duration.** Every shot needs a concrete observable_end_state — a state a viewer can point to.
- Cut when the next view changes what the audience can understand: a reveal, reaction, spatial relationship, scale, time/place, or a motivated rhythmic accent. A different angle alone is not a new shot.
- Keep connected gestures together when they serve one development: preparation → action → visible consequence. A prop handoff needs an owner after the transfer; a reaction follows its trigger.
- Allocate time to what the audience must actually see. Let the central action and its readable outcome happen before budgeting secondary poses or an ending. Choose shot count from that allocation, not from a fixed quota.
- Preserve causal hand/prop continuity inside each shot.
- When the user asks for one continuous take, POV, or an unbroken follow, produce **one shot** and let the beats progress inside it.
- Every shot lists stable subject_refs. Use asset_refs **only** for assets that supply shot-specific structural guidance (that shot's motion, camera, rhythm, style, audio or scene) — not for every asset in the request.

## Audio

Audio is part of the finished video, not optional decoration. When generate_audio is true, produce a complete, restrained audio_plan and never leave the soundscape empty.

- Put each action-synchronized sound in its own timeline event with its onset and stop cue. Keep continuous ambience in ambient_sound — it follows the scene rather than resetting at every cut.
- Describe audience-only score in music with instrumentation and pace. **Do not invent a measured BPM or claim to hear an instrument from visual evidence.** A camera move is not inherently audible.
- Do not sonify every motion. Keep quiet beats quiet.
- For user-supplied speech, **preserve the exact words and language**, identify the actual speaker, reserve plausible speaking time, and put music under it.
- Never invent speech, narration, dialogue, lyrics or vocal identity without an explicit request. A no-voice requirement still allows music, ambience and Foley.
- If generate_audio is false, keep **every** audio_plan field empty — no music, ambience, Foley or voice.

## Language

**Understanding language and rewrite language are separate.** Write all generated Context-IR descriptions in English. Preserve the source language **only** for verbatim dialogue, lyrics, and text visibly present in the requested scene.

### Dialogue and lyrics must carry the official tags

Any preserved source-language text **must** be wrapped in the official notation. Untagged non-English prose is rejected by a deterministic audit of the compiled prompt, and the whole compilation is sent back to you.

- Dialogue: "<d>[Language] exact words</d>" — e.g. "<d>[Chinese]等我一下。</d>"
- Lyrics: "<l>[Language] exact words</l>"

The language annotation is the source language of the words, not the rewrite language. Keep the words **exactly** as the user wrote them: do not translate, re-punctuate, or tidy them. Everything around the tag stays English.

This applies to **every** field, not just audio_plan.voice. If you restate the dialogue anywhere else — most often in "constraints.preserve" — it must carry the tags there too:

    "constraints": {"preserve": ["Exact dialogue wording: <d>[Chinese]等我一下。</d>"]}

Writing the same words tagged in one field and bare in another fails the audit, and it is the single most common reason a compilation is sent back.

The speech belongs in exactly two places: the shot "event" where it is spoken, and "audio_plan.voice". **Everywhere else, do not quote the words at all** — in "creative_focus.objective" write "she asks the viewer to wait", not the line itself. If you truly must restate it, it carries the tags like everywhere else.

### Every speaker gets a stable ID

Give each actual human speaker a stable "(S1)", "(S2)", … placed beside the subject at the speech event — **including when there is only one speaker**. Use the same ID in the subject's description so the voice stays bound to one person across the whole video. Bind any voice reference to that same speaker ID.

Put complete authorized dialogue on the shot where it is spoken (in "event") and in "audio_plan.voice"; keep continuous ambience in "ambient_sound" and audience-only score in "music". **Do not repeat the full speech** in the two global sound fields.

Replacing speech while keeping the source ambience is "partially_preserved", not "fully_preserved".

## What you must not emit

Do not emit the task block — duration, task type and generate_audio are injected deterministically after your JSON is parsed. Refer to asset IDs, never restate the input.

Return only assumptions and uncertainties inside intent.`

// BuildCompilerPrompt 拼出完整的编译器提示词。
//
// 结构照 XINGSHEN2：规则正文 + schema 骨架 + 本次输入。
//
// **schema 骨架用带示例值的 JSON**，而不是描述字段的散文 —— 模型照着填
// 比照着读准得多，这也是它原文的做法（`json.dumps(schema_template(...))`）。
func BuildCompilerPrompt(in CompilerInput) string {
	var b strings.Builder
	b.WriteString(compilerSystemPrompt)
	b.WriteString("\n\n## Required shape\n\nReplace the illustrative values; keep the keys.\n\n")
	b.WriteString(irSkeleton(in))
	b.WriteString("\n\n## Input\n\n")
	b.WriteString(in.describe())
	return b.String()
}

// CompilerInput 这一次请求的事实。
//
// **这些不是模板的一部分，是"这一次"的事实** —— 模板可以被运营改写，
// 而"用户传了几张图、要多少秒"不能被改。
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

// irSkeleton 带示例值的 IR 骨架。
//
// 手写而不是从结构体反射：反射出来的是类型名（`string`/`[]string`），
// 而模型需要看到的是**这个字段该填什么样的内容**。示例值本身就是规格。
func irSkeleton(in CompilerInput) string {
	return `{
  "schema_version": "0.1.0",
  "semantic_plan": {
    "primary_focus": "subject_1 or the intended visible outcome",
    "subject_priority": {"mode": "single|co_equal|explicit_hierarchy", "subject_ids": ["subject_1"], "reason": "derived only from user intent"},
    "text_policy": {"mode": "exact|preserve|generate|omit", "allow_invention": false, "provided_text": []},
    "edit_scope": {"mode": "generate|reference_transfer|minimal_edit", "editable": [], "locked": []},
    "completion_authority": {"technical": true, "timeline": true, "story_continuation": false, "new_core_entities": false, "brand_claims": false},
    "shot_functions": [{"shot_id": "01", "purpose": "one unique narrative function", "audience_gain": "what is newly visible by the end of this shot", "cut_reason": "new_information|action_match|state_change|viewpoint_change|reference_match|user_locked|none"}]
  },
  "protocol": {"rewrite_language": "English", "preserve_source_language_for": ["dialogue", "lyrics", "visible scene text"]},
  "subjects": [{
    "subject_id": "subject_1",
    "name": "stable identifiable entity",
    "kind": "person|product|animal|object|environment|other",
    "primary": true,
    "description": "one compact appearance profile, 20-45 English words",
    "source_asset_ids": ["image_1"],
    "appearance_shot_ids": ["01"],
    "retention_mode": "fully_preserved|partially_preserved|attribute_transfer|weak_reference",
    "retention_description": "what remains or transfers"
  }],
  "asset_bindings": [{
    "asset_id": "image_1",
    "target": "subject_1",
    "role": "identity|outfit|product|motion|voice|music|rhythm|camera|scene|style|first_frame|last_frame",
    "priority": "hard|soft",
    "inherit": ["controlled attribute"],
    "exclude": ["attribute this asset must NOT contribute"]
  }],
  "reference_relationships": [{
    "asset_id": "video_1",
    "relationship": "source_video_edit|reference_generation|keyframe_completion|video_continuation|audio_reuse|audio_reference",
    "subject_refs": ["subject_1"],
    "definition": "a noun phrase naming this reference's exact role; do not start with 'is'",
    "retention_mode": "partially_preserved",
    "retention_description": "how it is used in the target video"
  }],
  "keyframe_roles": [{
    "asset_id": "image_1",
    "role": "appearance_source|scene_anchor|action_keyframe|product_detail|first_frame|last_frame|composition_anchor|style_reference",
    "subject_refs": ["subject_1"],
    "shot_refs": ["01"],
    "controls": ["visible dimension supplied by this picture"],
    "excludes": ["motion", "camera", "editing", "music"],
    "description": "one concise explanation of how it is used"
  }],
  "creative_focus": {
    "primary_target": "the user's intended visible outcome",
    "primary_subject_id": "subject_1",
    "objective": "the final visible outcome that matters most",
    "required_shot_ids": ["01"],
    "presentation_requirements": ["an executable visibility, framing, material or continuity requirement"]
  },
  "constraints": {"preserve": [], "allow_change": [], "prohibit": []},
  "timeline": [{
    "shot_id": "01",
    "start_seconds": 0,
    "end_seconds": ` + fmt.Sprintf("%g", in.DurationSeconds) + `,
    "primary_change": "the single main visible change in this shot",
    "event": "one executable visible event",
    "camera": "one executable static or motivated moving-camera instruction",
    "lighting": "",
    "transition": "",
    "observable_end_state": "a concrete state a viewer can point to at the end",
    "state_changes": [{"subject_id": "subject_1", "property": "continuity-critical property", "from": "before", "to": "after"}],
    "subject_refs": ["subject_1"],
    "asset_refs": []
  }],
  "audio_plan": {"voice": "", "music": "", "sound_effects": "", "ambient_sound": "", "sync_rules": ["one line per cue: what happens, and at which action or time it lands"]},
  "generation_description": {"cinematography": "", "lighting": "", "materials": "", "performance": "", "continuity": ""},
  "intent": {"assumptions": [], "uncertainties": []}
}`
}
