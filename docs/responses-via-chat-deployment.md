# Responses → Chat 适配层：部署与兼容性指南

> 面向运维/接入。讲清楚「`/v1/responses`（Codex 等）打到 chat-only 上游」这套转换在**各种真实拓扑**下的行为、边界与配置方式。
>
> 设计与实现细节见 [`responses-via-chat-adaptation-design.md`](./responses-via-chat-adaptation-design.md)。

---

## 1. 一句话原理

网关对客户端**同时暴露** `/v1/chat/completions` 与 `/v1/responses` 两个端点。上游缺哪个，网关自动「借道转换」补齐：

- `/v1/responses` 进来，**上游只会 chat** → 网关把请求转成 chat 打上游，再把 chat 响应转回 Responses 事件流返回。
- `/v1/responses` 进来，**上游原生支持 responses** → 原样透传，不转换。
- `/v1/chat/completions` 路径**完全未改动**——chat 客户端（Cherry Studio、各类 SDK）照常原生透传，与 responses 路径互不干扰、共存。

---

## 2. 路由判定（`/v1/responses` 进来后如何选路）

判定顺序（`decideResponsesRoute`）：

1. **策略强制覆盖**（可选，默认空）：
   - `ForceNative` 命中 → 原生透传。
   - `ForceConvert` 命中 → 强制转换（**受信任的运维覆盖，绕过下面第 4 步的线格式守卫**，只应指向 OpenAI 线上游）。
2. **上游原生支持 responses？** → 是则原生透传（"协议匹配优先"，永不多此一举地转换）。
3. **上游是 chat 且线格式为 OpenAI 兼容？** → 是则转换（借道 chat）。
4. **都不满足** → **明确报错**（不静默降级、不产生乱码）。

### 2.1 「上游是否原生支持 responses」怎么判（base_url 智能判定）

因为 `OpenAI` 渠道类型同时服务官方和大量第三方（自定义 base_url），无法用单一默认值区分，故按 **base_url** 判：

| 通道 | `SupportsNativeResponses` |
|---|---|
| Azure 渠道 | `true`（原生 responses） |
| OpenAI 渠道，base_url 为空或含 `api.openai.com` | `true`（官方，原生） |
| OpenAI 渠道，**自定义 base_url**（第三方/自建/中转） | `false`（转换） |
| Codex 渠道 | `true`（只会 responses） |
| 其它所有渠道（各自 adaptor） | `false`（默认，转换） |

例外用策略钉死（见 §5）。

### 2.2 「线格式是否 OpenAI 兼容」守卫（第 3 步的关键）

转换路径**按 OpenAI chat SSE/JSON 解析上游响应**，所以只对「上游响应本身就是 OpenAI 线格式」的供应商成立。判定基于上游 ApiType：

- **OpenAI 线（可自动转换 ✅）**：OpenAI、OpenRouter、Xinference、Ali(Qwen)、BaiduV2、DeepSeek、MiniMax、Moonshot、Perplexity、SiliconFlow、VolcEngine、ZhipuV4(GLM)、xAI、Mistral、Submodel。
- **私有线（不自动转换，明确报错 ❌）**：AWS Bedrock、Gemini、Anthropic、百度 v1、腾讯、PaLM、Cohere、讯飞、Ollama、Cloudflare 等。

> 私有线上游若确需从 Codex 使用，最稳妥的方式是**在它前面放一层 OpenAI 兼容中转**（见 §4 场景 2/3），中转层会把私有线归一成 OpenAI 线，网关这边即可正常转换。

---

## 3. 兼容性总表

| 客户端 → | 上游类型 | 行为 | 结果 |
|---|---|---|---|
| `/responses`(Codex) | 官方 OpenAI / Azure | 原生透传 | ✅ |
| `/responses` | Codex 渠道 | 原生透传 | ✅ |
| `/responses` | 第三方 OpenAI 线（自定义 base_url、GLM/MiniMax/Qwen 等） | 转 chat | ✅ |
| `/responses` | **私有线直连**（AWS/Gemini/Anthropic…） | 明确报错 | ⚠️ 需中转或原生 |
| `/chat/completions` | 任意（未改动） | 原生透传 | ✅ |

---

## 4. 部署拓扑逐个说明

### 场景 1：new-api 直连 OpenAI / Azure / AWS，下游 Codex

- **OpenAI 官方 / Azure**：`/responses` 原生透传，Codex 原生可用。✅
- **AWS Bedrock**：私有线格式，**直连不支持转换 → 明确报错**。⚠️
  - 解法：在 AWS 前放一层 OpenAI 兼容中转（另一台 new-api 配 AWS 通道），Codex 指向本网关、本网关上游指向那层中转 → 中转把 Bedrock 归一成 OpenAI 线 → 本网关正常转换。见场景 2。

### 场景 2：new-api → 其他供应商（供应商再接 OpenAI/AWS/Azure），下游 Codex

```
Codex → 本 new-api → 供应商 → 真实后端(OpenAI/AWS/Azure)
        (responses→chat)  (chat)
```

- 本网关到供应商的通道通常是 **OpenAI 类型 + 供应商 base_url（自定义）** → 判定为转换 → 发 `/v1/chat/completions` 给供应商。
- 供应商收到**普通 chat 请求**，转发给它的真实后端，返回 **OpenAI 线** chat 响应 → 本网关正常转回 responses。✅
- **关键收益**：即便真实后端是 AWS/Gemini 这类私有线，**供应商那层已把它归一成 OpenAI 线**，所以场景 1 的「私有线直连」问题在这里**自动消失**。
- 若真实后端原生支持 responses、且你想端到端保留 reasoning：给「本网关→供应商」这条通道加 **ForceNative**，本网关原样透传 `/responses`，由供应商就近处理（供应商也须支持 responses）。

### 场景 3：new-api → GLM / MiniMax / Qwen，下游 Codex

- 无论配成各自原生渠道类型（ZhipuV4 / MiniMax / Ali）还是 OpenAI 类型 + base_url，**都是 OpenAI 线** → 转换正常工作。✅
- **注意**：Codex 重度依赖 function calling（shell / apply_patch）。这些模型需支持工具调用，Codex 才能正常干活——这是**模型能力**问题，非转换问题（网关会如实透传 tools）。

### 场景 4：下游也是 new-api 中转（且是原始/stock 代码），指向本网关

```
终端用户/Codex → 下游 new-api(原始代码) → 本 new-api(改过) → 真实上游
```

- 下游发 `/chat/completions` 给我们 → 我们**未改 chat 链路**，按 stock 处理。**零影响。** ✅
- 下游发 `/responses` 给我们 → 我们返回**合法的 Responses 事件流**（原生或重建）。下游 stock 的 `OaiResponsesStreamHandler` 对每个事件**无条件转发**给它的客户端，不认识的事件也照转，不会卡。✅
- **无硬破坏 / 无循环 / 无回归**：
  - 我们上游 chat-only：以前 stock 在我们这层就 `/responses` 失败，现在能转换成功 → **改善**。
  - 我们上游原生 responses：透传，行为不变。
  - `/responses/compact`：我们**显式排除**（仍走原生透传），行为与 stock **完全一致**，无回归。

### 场景 5：Chat 客户端（Cherry Studio 等）与 Codex 并存

- Cherry Studio 等走 `/v1/chat/completions`，**路径未改**，原生透传。✅
- 同一通道/模型对两个接口同时可用：chat 客户端走 chat，Codex 走 responses，互不影响。

---

## 5. 配置：覆盖策略（可选）

全局配置 `responses_to_chat_completions_policy`（默认两个名单皆空，完全交给自动判定）：

```jsonc
{
  "responses_to_chat_completions_policy": {
    "force_native": {          // 强制走原生 responses 透传
      "enabled": true,
      "channel_ids": [12],     // 或 channel_types / all_channels
      "model_patterns": ["^gpt-5"]  // 空 = 该通道所有模型
    },
    "force_convert": {         // 强制转成 chat（仅可指向 OpenAI 线上游）
      "enabled": true,
      "channel_types": [1]
    }
  }
}
```

典型用途：

- **ForceNative**：上游是原生 responses、但 base_url 自定义被误判为转换时；或 new-api→供应商链路想端到端保留原生 responses。
- **ForceConvert**：某 OpenAI 线供应商不在 §2.2 白名单里（新供应商），手动放开自动转换。⚠️ **不要**对私有线上游（AWS/Gemini…）用 ForceConvert，会产生乱码。

---

## 6. `/responses/compact` 上下文压缩

chat-only 上游没有 compact 端点，网关**借道 chat 实现**：

- compact 请求本身**已含 Codex 客户端侧的"总结历史"指令**，故网关只需：用 G1 把它转成一次**非流式 chat** 调用打上游 → 用 G2 把模型返回的摘要包成 `OpenAIResponsesCompactionResponse{output:[message 项], usage}` 返回。
- **不伪造专有的加密压缩项**（`encrypted_content`）——返回普通 message 项即可，Codex 接受（与 CodexPlusPlus 的做法一致）。
- 路由同样受 §2 判定：上游原生支持 responses（官方/Azure）→ 走原生 compact；chat-only OpenAI 线上游 → 借道转换；私有线 → 报错。
- **计费**：镜像原生 compact（`ModelPriceHelper` + 按真实 chat token 结算），与原生行为一致。
- **当前范围**：compact 转换适用于 `OpenAI` ApiType 通道（即 Codex 最常见的「OpenAI 类型 + 自定义 base_url」接法）。其它 OpenAI 兼容原生渠道类型（GLM/MiniMax/Qwen 的原生类型）的 compact 仍受早期端点守卫限制——但这些场景下 Codex 通常走客户端 local 压缩，不依赖此端点。

## 7. Codex 工具适配层（apply_patch / 自定义工具 / MCP）

Codex 用的很多工具**不是标准 JSON function 工具**，而是 Responses 专有的 freeform/custom 类型（尤其 `apply_patch` 改文件、`local_shell` 跑命令）。通用 chat 模型不懂这些，若直接丢弃 Codex 就无法编码。网关**对齐 CodexPlusPlus 的做法**做了双向适配：

- **请求侧（拆解）**：
  - `apply_patch`（custom/freeform）→ 拆成 5 个标准 JSON function 子工具（`*_add_file/_delete_file/_update_file/_replace_file/_batch`，各带 schema），任何 chat 模型都能调。
  - 其它 custom/freeform 工具、`local_shell`/`web_search`/`computer_use` → 包装成带单个 `input` 字符串参数的 function。
  - `namespace`（MCP）工具 → 扁平化为 `父__子` 的 function 工具。
- **响应侧（名回映射）**：模型调用 `apply_patch_add_file(...)` 等子工具时，网关把它**收敛回** `apply_patch` 的 Responses `custom_tool_call` 形状，并把结构化 JSON hunks **转回 apply_patch 文本补丁**；namespace 工具还原 `namespace` 字段。流式下 custom 工具抑制中间参数增量，收尾一次性下发重建好的 `custom_tool_call_input.delta`。
- **历史重建**：后续请求里 Codex 回传的 `custom_tool_call` / `custom_tool_call_output`（含 apply_patch 文本）会被重新拆解成子工具名+参数，保证与模型看到的工具集一致。

这套适配对**两类上游都成立**：原生懂 Codex 工具的模型、以及通用 chat 模型（GLM/Qwen/DeepSeek/自建等）。

**reasoning（思考）——已按 provider 映射**：Codex 的 `reasoning.effort` 会按**模型名**映射到对应字段（GLM/Kimi/GLM→`thinking`、Qwen/SiliconFlow→`enable_thinking`、MiniMax→`reasoning_split`、DeepSeek/OpenRouter→各自 `reasoning`、OpenAI o 系/gpt-5+→`reasoning_effort`），让思考力度在国产模型上真正生效（与各 adaptor 的后缀触发互补）。`o` 系模型的 `max_output_tokens` 映射为 `max_completion_tokens`。

## 8. 请求字段处置表(转换路径审计结论)

`OpenAIResponsesRequest` 全部字段在 responses→chat 转换中的处置:

| 处置 | 字段 |
|---|---|
| **转换/拷贝** | model、input(→messages)、instructions(→system/developer)、max_output_tokens(→max_tokens,o系→max_completion_tokens)、tools/tool_choice(含 Codex 工具适配)、text(→response_format)、reasoning(per-provider 映射)、temperature、top_p、top_logprobs(+logprobs=true)、stream、user、store、metadata、parallel_tool_calls、service_tier、safety_identifier、prompt_cache_key、prompt_cache_retention、enable_thinking(客户端显式优先) |
| **明确拒绝**(服务端状态,chat 无法恢复) | previous_response_id、conversation、prompt(存储 prompt 引用) |
| **有意忽略**(无害降级) | include(合成响应本就不含扩展数据)、context_management(chat 无服务端压缩;Codex 走客户端压缩)、truncation(等同默认 disabled)、max_tool_calls(chat 路径无内建工具)、preset(perplexity 专有,该渠道走原生)、stream_options(编排层按渠道能力重设 include_usage) |

## 9. 借道路径与原生管道等价性(审计结论)

借道 chat 时逐项对齐原生管道:模型映射、stream_options 门控(`SupportStreamOptions`)、渠道系统提示、`RemoveDisabledFields`、参数覆盖、URL/Header(请求阶段临时置 `RelayFormat=OpenAI`,响应阶段恢复)、供应商 usage 归一化(`applyUsagePostProcessing`,普通/流式/compact 三处)、流内错误帧透出、length 截断保真。
**已知行为差异**:渠道设置 `ThinkingToContent` 在原生 chat 路径会把思考内联进 content;借道路径将思考映射为 Responses 的 reasoning summary 事件(对 Responses 客户端语义更正确)。

## 10. 其它已知边界

- **私有线上游直连不支持**（§2.2）——用中转层归一，或用原生 responses 通道。
- **历史里已含 OpenAI `encrypted_content`**：这类专有加密项无法在非 OpenAI 上游重放，可能报 `invalid_encrypted_content`——需切回原供应商或开新会话（网关无法伪造该加密项）。
- **reasoning（思考）**：思考力度按 provider 映射（见 §7）。**思考摘要**：若上游 chat 流返回 `reasoning_content`，网关转成 `response.reasoning_summary_text.delta` 等事件回传，Codex 可显示思考摘要（流式路径；非流式不含 reasoning 项，Codex 走流式不受影响）。**encrypted-reasoning** 这类 chat 协议本身没有的专有信号不产出，Codex 容忍其缺失。
