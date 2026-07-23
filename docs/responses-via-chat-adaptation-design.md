# Responses → Chat Completions 适配层架构设计

> 目标：让 new-api 网关能接收 **OpenAI Responses API**（`/v1/responses`）请求，并把它转发给**只支持 Chat Completions**（`/v1/chat/completions`）的上游模型；同时**保持同一个模型/通道对两种接口都可用**。
>
> 主要驱动场景：OpenAI **Codex CLI**（当前版本已移除 Chat Completions 线路，`wire_api` 只剩 `responses`）需要直连我们平台的模型，而我们的模型是标准 OpenAI Chat 接口。

---

## 1. 背景与约束

- **Codex 端硬约束**：最新 Codex 的 `WireApi` 枚举只剩 `Responses`，`wire_api = "chat"` 会直接报错。所有请求打到 `POST {base_url}/responses`，并使用 `/responses/compact` 做上下文压缩。因此 Codex 无法直接对接只会 Chat 的后端。
- **我们的约束（关键）**：同一个模型必须**两种接口同时可用**：
  - `/v1/chat/completions` —— 现有客户端继续用（原生透传，不改）。
  - `/v1/responses` —— 供 Codex 等只会 Responses 的客户端使用（本设计新增，转成 Chat 打上游）。
- **落点选择**：不改 Codex（保持官方原版，只改 `base_url`），把协议差异**在 new-api 网关内**消化。相比 fork Codex 客户端（Rust，需永久跟随上游 rebase），网关侧改动可控、可复用、零客户端维护负担。

---

## 2. 现状盘点：已具备的积木

new-api 已内建**反方向**（Chat 入 → Responses 上游）的完整实现，可作为镜像模板：

| 能力 | 位置 | 说明 |
|------|------|------|
| `/v1/responses` 路由 | `router/relay-router.go:121` | `controller.Relay(c, types.RelayFormatOpenAIResponses)` |
| `/v1/responses/compact` 路由 | `router/relay-router.go:124` | compact 端点已注册 |
| Responses 请求处理器 | `relay/responses_handler.go:23` `ResponsesHelper` | 解析请求 → 选 adaptor → `ConvertOpenAIResponsesRequest` → `DoRequest`/`DoResponse` |
| Chat→Responses **请求**转换 | `service/openaicompat/chat_to_responses.go:76` | 我们要写它的**反向** |
| Responses→Chat **响应**转换（非流式） | `service/openaicompat/responses_to_chat.go:10` | 已有响应方向；缺请求方向 |
| Responses→Chat **流式** handler | `relay/channel/openai/chat_via_responses.go:93` `OaiResponsesToChatStreamHandler` | **反向流式模板**，照镜像写 |
| 原生 Responses 流式 handler | `relay/channel/openai/relay_responses.go:71` `OaiResponsesStreamHandler` | 生成端要对齐的**事件序列参考** |
| “借道”编排模板 | `relay/chat_completions_via_responses.go:73` `chatCompletionsViaResponses` | 我们新编排文件的**结构模板** |
| Responses 流式事件 DTO | `dto/openai_response.go:392` `ResponsesStreamResponse` + `:387` 事件常量 | 生成端直接复用 |
| per-channel 策略 | `setting/model_setting/global.go:10` `ChatCompletionsToResponsesPolicy` | 我们镜像一份反向策略 |
| Codex 通道 adaptor | `relay/channel/codex/adaptor.go` | 已有，处理 Codex 特有的 `/backend-api/codex/responses` 上游 |

---

## 3. 缺口分析（精确）

grep 确认 `Chat流 → Responses流` 的生成端**完全不存在**（`ChatToResponses*` 全空）。要补的东西如下——**三个转换函数 + 一个编排文件 + 一个能力标记 + 一个覆盖策略 + 接线**：

| # | 缺失件 | 类型 | 参考镜像 | 规模 |
|---|--------|------|----------|------|
| G1 | `ResponsesRequestToChatCompletionsRequest` | Responses 请求 → Chat 请求 | `chat_to_responses.go:76` 反向 | 中（~150–250 行） |
| G2 | `ChatCompletionsResponseToResponsesResponse` | Chat 响应 → Responses 响应（非流式） | `responses_to_chat.go:10` 反向 | 小（~80–150 行） |
| G3 | `OaiChatToResponsesStreamHandler` | Chat SSE → Responses 事件流 | `chat_via_responses.go:93` 反向 | **大（~250–400 行，主工作量）** |
| G4 | `responsesViaChatCompletions` 编排 | 收 Responses → 转 Chat → 打上游 → 转回 Responses | `chat_completions_via_responses.go` 镜像 | 中 |
| G5a | `SupportsNativeResponses()` 能力标记 | adaptor 能力查询，驱动自动推导 | 新增于 `channel.Adaptor` 接口 | 小 |
| G5b | `ResponsesToChatCompletionsPolicy` | **覆盖开关**（ForceNative/ForceConvert 白黑名单，非默认判定） | `global.go:10` 镜像 | 小 |
| G6 | 接线：`ResponsesHelper` 分支 | **能力自动推导为主 + 策略覆盖**（见 §4.1.1） | — | 小 |

依赖的 DTO、双向参考实现、端点注册、compact、SSE scanner、事件发送 helper **全部已存在**，不需要新造基础设施。

依赖的 DTO、双向参考实现、端点注册、compact、SSE scanner、事件发送 helper **全部已存在**，不需要新造基础设施。

---

## 4. 架构设计

### 4.1 双接口同时支持（核心要求）

网关是一个**协议归一层**：对客户端**始终同时暴露** `/v1/chat/completions` 与 `/v1/responses` 两个端点，**不论上游支持哪一个**。上游缺的那个端点，由网关自动“借道转换”补齐；上游原生支持的端点，直接透传。

```
                    ┌──────────────────── new-api 网关（协议归一层）────────────────────┐
 客户端 (Chat) ─────▶ POST /v1/chat/completions ─┐                                     │
                    │                            ├─▶ [协议匹配? 原生透传 : 借道转换] ─▶ 上游
 Codex (Responses)─▶ POST /v1/responses ─────────┘        ▲                            │
                    │                                     └── 能力自动推导(§4.1.1)      │
                    └────────────────────────────────────────────────────────────────┘
```

- **`/v1/chat/completions` 路径**：上游会 Chat → 原生透传（`router/relay-router.go:116`，不改动）；上游只会 Responses → 现有 `chat_completions_via_responses.go` 转换。
- **`/v1/responses` 路径**：上游会 Responses → 原生透传（现有）；上游只会 Chat → G1–G4 新增转换。
- 判定粒度是**通道 + 模型**，不同模型可各走各的，**共存**。

#### 4.1.1 判定逻辑：能力自动推导（协议匹配优先），策略为覆盖

**核心原则：协议匹配优先，转换仅作补齐。** 客户端用哪个协议，就优先走上游**同协议**的原生端点；只有上游**缺**该端点时才转换。因此**上游两个都支持时，永不转换**（两条路都原生）。

**两个能力标记**（升级自单布尔）：给通道 adaptor 增加两个查询——
- `SupportsNativeResponses(info) bool`：上游是否原生支持 `/v1/responses`。
- `SupportsNativeChat(info) bool`：上游是否原生支持 `/v1/chat/completions`（绝大多数通道为 `true`）。

**完整决策矩阵**（客户端端点 × 上游能力）：

| 客户端 ↓ \ 上游 → | 只会 Chat | 只会 Responses | **两个都支持** |
|---|---|---|---|
| `/v1/chat/completions` | 原生透传 | 转 chat→responses（已有） | **原生 chat 透传（不转）** |
| `/v1/responses` | 转 responses→chat（G1–G4） | 原生透传 | **原生 responses 透传（不转）** |

**`/v1/responses` 路径的决策顺序（`ResponsesHelper` 内，选完 adaptor 后）**：

```go
p := policy // ResponsesToChatCompletionsPolicy（覆盖用，默认名单空）
switch {
// 1) 策略强制覆盖（个别通道钉死行为）
case p.ForceNative(channelID, channelType, model):
    // 走现有原生 Responses 路径
case p.ForceConvert(channelID, channelType, model):
    return responsesViaChatCompletions(c, info, adaptor, request)

// 2) 默认：协议匹配优先 —— 上游原生支持 Responses 就绝不转换
case adaptor.SupportsNativeResponses(info):
    // 走现有原生 Responses 路径（上游“两个都支持”也落这里 → 不转换）
case adaptor.SupportsNativeChat(info):
    // 上游只会 Chat → 借道转换
    return responsesViaChatCompletions(c, info, adaptor, request)
default:
    // 两个都不支持 → 明确报错，不静默降级
    return errUpstreamNoCompatibleEndpoint
}
```

- **默认零配置即生效**：新增 Chat-only 通道 → `/v1/responses` 自动可用；新增原生 Responses 通道（或两个都支持的通道）→ 自动走原生、**不被误转**。
- **策略仅作例外覆盖**：绝大多数通道不需出现在名单里。典型用途：上游两个都支持、但想让 chat 客户端也强制走 responses 以拿 reasoning → 用现有 `ChatCompletionsToResponsesPolicy` 的 ForceConvert（对称方向）。
- **能力探测不准时**：用 `ForceNative` / `ForceConvert` 显式钉死兜底。

- **默认零配置即生效**：新增一个 Chat-only 通道，`/v1/responses` 自动可用；新增/接入一个原生 Responses 通道，自动走原生、不会被转。
- **策略仅作例外覆盖**：`ResponsesToChatCompletionsPolicy` 退化为“白/黑名单式覆盖”——绝大多数通道不需要出现在里面。这与需要逐条配置的纯策略方案相反。
- **可回退**：若某通道能力探测不准，用策略 `ForceNative` / `ForceConvert` 显式钉死。

### 4.2 请求方向流程（G4 编排）

镜像 `chat_completions_via_responses.go:73`：

1. `ResponsesHelper` 解析出 `dto.OpenAIResponsesRequest`（已有）。
2. 命中 §4.1.1 判定（能力推导为 Chat-only 或策略 ForceConvert）→ 调 **G1** `ResponsesRequestToChatCompletionsRequest` 得到 `dto.GeneralOpenAIRequest`。
3. `info.RelayMode = RelayModeChatCompletions`、`info.AppendRequestConversion(RelayFormatOpenAI)`（标记已转换，供计费/日志）。
4. `adaptor.ConvertOpenAIRequest(...)`（现有 Chat 路径）→ `DoRequest` 打上游 `/chat/completions`。
5. 按 `info.IsStream` 调 **G3**（流式）或 **G2**（非流式）把上游响应转回 Responses 格式回给调用方。

### 4.3 响应方向：流式事件映射表（G3，核心）

上游 Chat SSE chunk → 下发给 Codex 的 Responses 事件序列：

| 时机 | 上游 Chat chunk | 生成的 Responses 事件 |
|------|-----------------|------------------------|
| 首个 chunk | `choices[0].delta.role=assistant` | `response.created` → `response.output_item.added`(item.type=`message`) → `response.content_part.added`(part.type=`output_text`) |
| 文本增量 | `delta.content = "…"` | `response.output_text.delta`(delta="…") |
| 工具调用出现 | `delta.tool_calls[i].id/function.name` | `response.output_item.added`(item.type=`function_call`, call_id, name) |
| 工具参数增量 | `delta.tool_calls[i].function.arguments` | `response.function_call_arguments.delta`(delta) |
| 文本结束 | `finish_reason` 出现且有文本 | `response.output_text.done` → `response.content_part.done` → `response.output_item.done` |
| 工具结束 | `finish_reason = tool_calls` | `response.function_call_arguments.done` → `response.output_item.done` |
| 收尾 | 最终 chunk / `[DONE]` + usage | `response.completed`(response.status=`completed`, output=[…], usage) |

**Usage 映射**：`prompt_tokens → input_tokens`、`completion_tokens → output_tokens`、`total_tokens → total_tokens`。若上游流不带 usage，沿用 `OaiResponsesStreamHandler` 的兜底：用 `service.CountTextToken` 估算（见 `relay_responses.go:133`）。

**状态机要点**（照 `OaiResponsesToChatStreamHandler` 的镜像）：
- 按 `tool_calls[].index` / `id` 累积 name + arguments（反向就是我们**拆分**成 delta 事件）。
- 维护 `output_index` / `content_index` / `item_id`（可用 `helper.GetResponseID` 生成稳定 ID）。
- 保证 `response.created` 只发一次、`response.completed` 只发一次。

### 4.4 非流式（G2）

上游一次性返回 Chat completion → 组装单个 `OpenAIResponsesResponse`：
- `output = [ {type:"message", role:"assistant", content:[{type:"output_text", text}]}, {type:"function_call", …}? ]`
- `status = "completed"`，填 `usage`。
- 复用 `responses_to_chat.go` 里 `ExtractOutputTextFromResponses` 的对称逻辑。

### 4.5 `/responses/compact` 处理（Codex 上下文压缩）

Chat 上游没有 compact 端点。两种策略：

- **Phase 1（建议先做）**：网关侧**本地实现压缩**——收到 `OpenAIResponsesCompactionRequest`（`ResponsesHelper` 已能解析，见 `responses_handler.go:41`），用上游 Chat 跑一次“总结历史”的请求，返回 Responses 形状的 compaction 结果。
- **Phase 2（可选）**：若上游本身能力有限，退化为**直接返回原输入的裁剪版**（按 token 预算截断），保证 Codex 流程不中断。

> compact 不是打通对话的前置条件，可作为独立里程碑；先保证 `/responses` 主链路可用。

---

## 5. 落地清单（按文件）

### 新增文件

1. **`service/openaicompat/responses_to_chat.go`（在现有文件追加）**
   - `func ResponsesRequestToChatCompletionsRequest(req *dto.OpenAIResponsesRequest) (*dto.GeneralOpenAIRequest, error)` —— **G1**
     - `input` items → `messages`：`message` 项按 role 展开；`function_call` → assistant.tool_calls；`function_call_output` → role=`tool` + tool_call_id。
     - `instructions` → 头部 `system` 消息。
     - `tools`（Responses 工具格式）→ Chat `tools`（`{type:"function", function:{name, parameters}}`）。
     - 透传 `temperature/top_p/max_output_tokens→max_tokens/stream` 等。
   - `func ChatCompletionsResponseToResponsesResponse(resp *dto.OpenAITextResponse, model string) (*dto.OpenAIResponsesResponse, error)` —— **G2**

2. **`relay/channel/openai/responses_via_chat.go`（新文件）**
   - `func OaiChatToResponsesStreamHandler(c, info, resp) (*dto.Usage, *types.NewAPIError)` —— **G3**（镜像 `chat_via_responses.go:93`，用 `helper.StreamScannerHandler` 消费上游 Chat SSE，用 `helper.ObjectData` / `HandleStreamFormat` 下发 Responses 事件）。
   - `func OaiChatToResponsesHandler(c, info, resp) (*dto.Usage, *types.NewAPIError)` —— 非流式包装 G2。

3. **`relay/responses_via_chat_completions.go`（新文件）**
   - `func responsesViaChatCompletions(c, info, adaptor, request *dto.OpenAIResponsesRequest) (*dto.Usage, *types.NewAPIError)` —— **G4**（镜像 `chat_completions_via_responses.go:73`）。

### 改动文件

4. **`relay/channel/adapter.go`（`channel.Adaptor` 接口）+ 各 adaptor** —— **G5a（两个能力标记，自动推导的核心）**
   - 接口新增两个查询：
     - `SupportsNativeResponses(info *relaycommon.RelayInfo) bool`
     - `SupportsNativeChat(info *relaycommon.RelayInfo) bool`
   - 默认实现（多数通道）：`SupportsNativeChat=true`、`SupportsNativeResponses=false`。可在 `channel/adapter.go` 提供默认，减少改动面。
   - Codex 通道（`relay/channel/codex/adaptor.go`）：`SupportsNativeResponses=true`，`SupportsNativeChat=false`（其 `ConvertOpenAIRequest` 已明确不支持 `/chat/completions`）。
   - OpenAI 官方通道：对 Responses-capable 模型两者皆 `true`（→ 决策矩阵“两个都支持”列，永不转换）。
   - 判定可按**模型**细化（`info` 带模型名），支持“同通道不同模型能力不同”。

5. **`setting/model_setting/global.go`** —— **G5b（覆盖策略，非默认判定）**
   - 加 `type ResponsesToChatCompletionsPolicy struct{ ForceConvert / ForceNative 名单 }`，镜像 `:10-38` 的 `ChatCompletionsToResponsesPolicy` 结构（`Enabled/ChannelIDs/ChannelTypes/ModelPatterns`），但语义是**覆盖**而非“唯一开关”；挂到全局 settings + 默认值（默认两个名单皆空 → 完全交给能力推导）。

6. **`relay/responses_handler.go`（`ResponsesHelper`, `:23`）** —— **G6（接线：能力推导为主 + 策略覆盖）**
   - 在选完 adaptor、转换请求**之前**按 §4.1.1 决策：
     ```go
     p := model_setting.GetGlobalSettings().ResponsesToChatCompletionsPolicy
     switch {
     case p.ForceNative(info.ChannelId, info.ChannelType):
         // 走现有原生 Responses 路径（不变）
     case p.ForceConvert(info.ChannelId, info.ChannelType):
         return responsesViaChatCompletions(c, info, adaptor, request)
     case adaptor.SupportsNativeResponses(info):
         // 上游原生支持 → 走现有原生路径（不变）
     default:
         // 上游只会 Chat → 自动转换
         return responsesViaChatCompletions(c, info, adaptor, request)
     }
     ```

7. **（可选）`relay/channel/openai/adaptor.go` `DoResponse`（`:743`）**
   - 若不想在 `ResponsesHelper` 里短路，也可在此按 `info` 上的转换标记选择 `OaiChatToResponsesStreamHandler`；二选一即可，推荐 G6 的编排短路更清晰。

8. **compact**：新增 `relay/responses_compact_via_chat.go`（Phase 1），或在 G4 内按 `RelayMode==RelayModeResponsesCompact` 分流。

---

## 6. 测试计划

- **单元**：G1/G2 的字段映射（含 tool_calls、function_call_output、instructions、多模态 content 降级）。
- **流式快照**：构造上游 Chat SSE 样本 → 断言产出的 Responses 事件序列（对齐 §4.3 表）。参考现有 `relay/channel/openai` 下的流式测试与 `dto.ResponsesStreamResponse`。
- **能力推导（覆盖决策矩阵全部格子）**：
  - Chat-only 上游 → `/v1/responses` 转换、`/v1/chat/completions` 原生。
  - Responses-only 上游 → `/v1/responses` 原生、`/v1/chat/completions` 转换（现有）。
  - **两个都支持的上游 → 两条路都原生，断言无转换发生**（关键回归）。
  - 再验证 `ForceNative`/`ForceConvert` 覆盖生效。
- **端到端**：本地起 new-api，仅配置一个 Chat-only 上游通道（**无需任何逐模型开关**）；用**官方 Codex** 把 `base_url` 指向网关，跑：普通对话（流式）、工具调用、`/responses/compact`。
- **回归**：确认 `/v1/chat/completions` 原生路径与上游原生 Responses 路径**均不受影响**（双接口共存）。

---

## 7. 工作量与风险

| 项 | 估计 |
|----|------|
| G1 请求转换 | 1 天 |
| G2 非流式响应 | 0.5 天 |
| G3 流式生成端（主风险） | 1.5–2 天 |
| G4 编排 + G5a 能力标记 + G5b 覆盖策略 + G6 接线 | 1 天 |
| compact（Phase 1） | 0.5–1 天（可延后） |
| 测试 | 1 天 |
| **合计** | **约 4–6 天** |

**主要风险**：G3 流式事件的**顺序 / ID 稳定性 / 工具参数分片**——但有 `OaiResponsesToChatStreamHandler` 作精确反向参照，风险可控。其余均为对已有双向实现的镜像，无未知基础设施。

**非目标**：不修改 Codex 客户端；不追求 reasoning / encrypted-reasoning 等 Chat 协议本身不具备的能力（这些信号缺失对 Codex 无害）。

---

## 8. 里程碑

1. **M1**：G1 + G2 + G3 + G4 + G5a + G5b + G6 —— `/v1/responses` 非 compact 主链路打通，能力自动推导生效，Codex 可对话 + 工具调用。
2. **M2**：`/responses/compact`（Phase 1 本地压缩）。
3. **M3**：完善测试 / 快照 / 计费日志字段（`AppendRequestConversion` 已铺好埋点）。

---

## 9. 实现状态与偏差（落地记录）

> 部署/拓扑视角的完整说明见 [`responses-via-chat-deployment.md`](./responses-via-chat-deployment.md)。

- **M1：已完成**（G1–G6）。相对本设计的偏差：
  - **G5a 能力判定改为「base_url 智能判定」**，而非单布尔默认。因为 `OpenAI` adaptor 同时服务官方与大量第三方（自定义 base_url），无法用单一默认值区分：Azure / 官方 `api.openai.com` → 原生；自定义 base_url → 转换。既零回归又让第三方近零配置。
  - **G5a 用可选接口 + 默认 helper** 实现，而非直接给 `channel.Adaptor` 主接口加方法（避免改动 40+ adaptor）。仅 openai / codex 实现该接口。
  - **G2 放在 `chat_to_responses.go`**（与转换方向一致），G1 放在 `responses_to_chat.go`。
  - **新增「线格式守卫」**（本设计未预见）：转换路径按 OpenAI chat 线格式解析上游响应，故仅对 OpenAI 线上游成立；私有线上游（AWS/Gemini/Anthropic 等）**明确报错**而非产生乱码。白名单见部署文档 §2.2。
- **M2：已完成**（借道 chat 实现）。原先误判为「无法做」（以为必须复现专有加密项 `encrypted_content`）；参考 CodexPlusPlus 后确认**无需复现**——compact 请求已含 Codex 客户端侧的总结指令，网关只需转 chat 跑一次摘要、用 G2 把结果包成 `OpenAIResponsesCompactionResponse{output:[message 项]}` 返回即可。`decideResponsesRoute` 已放开 compact 分支，`responsesCompactViaChatCompletions` + `OaiChatToResponsesCompactionHandler` 落地，计费镜像原生 compact。当前范围限 `OpenAI` ApiType 通道（受早期端点守卫），覆盖 Codex 主用例。
- **M3：已完成**。G3 流式事件序列快照测试（文本 / 工具调用，对齐 §4.3）；计费/日志埋点核对通过（`RequestConversionChain` = `[OpenAIResponses → OpenAI]`，最终格式解析为 OpenAI，走标准 chat 计费）。
