> 本文件是 H3 视频提示词增强的设计说明与实测记录。数据来源：8 条用例（`scripts/h3vid-pe/vcases.py`）
> 与官方 Context-IR 基线逐条对照；实验包 `docs/h3pe_h3vid_实验包.tar.gz`（`docs/` 不入库）。
> 文中出现的 `runs/…` 目录都是实验时的工作目录，不在仓库里；复跑方法见最后一节。

# H3 视频提示词增强：现网复刻 vs 官方，逐条差异与修复

更新时间：2026-11-20（第二轮：PR #97 已合并，视频改动已 rebase 并继续推进）。实验数据在 `docs/h3pe_h3vid_实验包.tar.gz`（本地解包目录 `.h3exp/`，不入库）。

## 0. 结论摘要

| 问题 | 现状 | 状态 |
| --- | --- | --- |
| 尾帧对齐那句的秒数 | 我们写 `5.00`，官方写 `5.17`（H3 帧栅格 17k+5，5 秒实际交付 124 帧） | **已修**：证据里带 `effective_duration_seconds`，复跑写出 5.17 |
| 输出被截断 | 上游默认输出上限把正文截在半句台词上，返回的 JSON 仍闭合可解析，只是 `h3_prompt` 为空 | **已修**：显式 `max_tokens`（默认 8192，可配） |
| 首帧/参考图清单 | 关思考时基本不清点（i2v 1019–1699 字符），官方 3779–3850 | **已修**：合规清单进生产提示词后 i2v 2341 / l2v 2114 |
| `non_diegetic_music` | 我们常写 `N/A`，官方 8/8 都谱了曲子 | **已修**：清单第 5 条 |
| 段落纪律 | r2va 4 份样本里 3 份正文节名写成帧族那个名字，1 份 `summary` 写两遍 | **已修**：节名归一（确定性改名）+ 重复节校验 |
| 保留标记语义 | 狗穿平铺夹克，官方判 `attribute_transfer`，我们三次都判 `fully_preserved` | **已修**：清单第 7 条，复跑判对且理由与官方一致 |
| 参考音频 | 证据里根本没有音频，提示词无从提音色 | **已修标签**：音频进证据（`<Audio N>`），内容仍读不到 |
| `response_format` 引发 500 | singlecall 一直在发 `json_object`，引擎 xgrammar 把首 token 判非法 → 整类 500（`grammar rejected tokens`） | **已修**：不发该字段，与 qwen_pe / h3_image_pe 一致 |
| 参考视频（r2va） | 500 已随上一条消失（74.5s 返回）；剩下的是引擎自己拉 CDN 视频偶发超时（30s→120s→480s 重试链 → 504） | **绕过已验证**：视频以 base64 data URI 内联，5.2s 成功 |

一句话：能自己修的都修完了（代码 + 提示词 + 校验），剩两件要外部输入 —— 参考视频在我们 MaaS 上不稳、以及"准不准用收费模型/ASR 处理音频"这个决定。

## 1. 方法

- 用例：`vcases.py` 8 条（t2v×3、i2v×1、l2v×1、r2v×2，含官方示例的原素材）。
- 官方基线：`runs/official/*.json`，8/8 成功，21–111 秒，正文 2033–4881 字符。
- 现网复刻：`scripts/h3vid-pe/run_sc.py`（现网形状）与 `run_s2.py`（候选配置，支持 `MAXTOK`、`EFF`、附加规则），系统提示词的拼接顺序与 `buildSingleCallSystem` 逐字一致。
- **结构合规用确定性检查器**（`check.py`）：节名是否齐、镜头编号、切点格式与时长、素材标号是否越过实际素材数、台词是否逐字、引号外是否漏中文、`or` 二选一计数、段落是否重复。检查器先在**官方 8 条**上跑一遍做基线校准，官方必须全绿（校准中修掉 3 个误报）。
- **语义判断由 agent 逐条对照素材做**，不用模型当评委：读图/读原视频，核对清点是否虚报、保留标记判得对不对、有没有编造小字。

## 2. 数据

正文长度（字符）：

| 用例 | 官方 | none（现网默认） | low | low+max_tokens+有效时长 | +附加规则（实验） | +清单进生产 |
| --- | --- | --- | --- | --- | --- | --- |
| t2v_space | 2716 | 1502 | 1282 | 1777 | 1689 | 1643 |
| t2v_dialog | 2445 | 1397 | 1643 | 1787 | 1656 | **1989** |
| t2v_product | 2033 | 746 | 1297 | 1522 | 1513 | 1162 |
| i2v_ramen | 3779 | 1019 | 1689 | 1699 | 2270 | **2341** |
| i2v_cat | 3850 | 993 | 1683 | 1765 | 2116 | 1776 |
| l2v_soda | 4881 | 1330 | 1557 | 1887 | 2078 | **2114** |
| r2v_shiba | 2168 | 1834 | 3379 | 1439 | 1631 | 3669 |
| 单条耗时 | 21–111s | 14–34s | 38–80s | 23–117s | 23–75s | 20–75s |

稳定性（同输入 3 次，字符数）：

| 用例 | #1 | #2 | #3 | 结构是否 3/3 合规 |
| --- | --- | --- | --- | --- |
| t2v_space | 1689 | 1586 | 2055 | 3/3（镜头数 1–2 之间变） |
| t2v_dialog | 1656 | 2161 | 1925 | 3/3 |
| i2v_cat | 2116 | 1967 | 1903 | 3/3 |
| r2v_shiba | 1631 | 3587 | 3968 | **1/3**（另两次缺三节 / 正文节名用错） |

## 3. 逐条差异

### 3.1 尾帧对齐的秒数用错了（已修）

官方 `l2v_soda` 首行写 `aligns with the 5.17-second mark`，我们写 `5.00`。官方规范 `base-en.txt:34`：

> Here, `N` is the index of the actual final shot, and `S.SS` is the **effective** video duration formatted to exactly two decimal places.

而 H3 出片帧数是 17k+5（`h3ir/grid.py` 与 ComfyUI `nodes_minimax_h3.py` 一致）：请求 5 秒 = 124 帧 = 5.167 秒。我们只给了 `duration_seconds: 5`，模型只能写 5.00。差 0.17 秒意味着"画面在 5.00 秒定住"而最后一帧在 5.17 秒 —— 尾帧提前到位或根本没到，且不报错。

修法：证据加 `effective_duration_seconds`，由 `common.H3DeliveredDuration` 算（该函数同时服务计费回显，栅格口径单一）。

### 3.2 输出被截断（已修）

`low` 档一次 `h3_prompt` 为空：原始回复 1505 字符，断在 `A bold white text reading "` 处，模型自己补了收尾括号 → **JSON 合法、能解析，只是正文没了**。传输检查报 `h3_prompt must be nonempty`，重修一轮还是同一份截断输出，最后静默回落 text。

根因：我们从不发 `max_tokens`，用的是上游默认上限，而一次完整产物是 content_plan（几千 token）+ 整段 h3_prompt。

### 3.3 首帧/参考图清点不完整 → 合规清单进生产（已修，差距缩到官方 60–70%）

官方 i2v 第一段是逐元素清点首帧：碗的蓝白花纹、两片叉烧的焦边、七位家人各自的衣着、障子门、纸灯笼 —— 然后才写动作。我们关思考时只有一句 `seen in <Picture 1> remains ...`。

清单第 1 条（"先清点再写动作，`as shown in <Picture 1>` 不算清点"）把 i2v 从 1699 拉到 2341、l2v 从 1887 拉到 2114。剩下的差距是修饰密度（材质、光线、景深），不是漏项。

反直觉结果：**开思考的模型转录小字比官方还准**。`l2v_soda` 上黑板 `今日特饮 / 柠檬气泡水 / ¥12` 与 `OPEN 10:00-22:00` 全对（对着原图逐字核过）；`r2v_shiba` 上读出夹克左胸深色织标 —— 官方漏了。

### 3.4 `non_diegetic_music` 写 N/A（已修）

官方 8/8 都谱曲；我们 `none` 有 3/7 写 `N/A`。规范只在"用户要求全程静音"时允许 N/A。清单第 5 条写明判据。

### 3.5 段落纪律：缺节、重复节、节名用错（已修）

- r2va 4 份样本里 3 份把正文节写成 `integrated_multimodal_description`（帧族名）。两者指同一件事，**改名是确定性的结构修复**：新增 `normalizeSectionHeaders`，只在行首那个节名上改名，正文里出现的同名字样不动（有测试钉住）。
- 1 份把 `summary` 整段写了两遍。`missingSections` 只问"在不在"，查不出来 → 新增 `duplicateSections`，只认行首的节名形态（允许 `**`、`#`、`-` 装饰），正文里 "the overall soundscape of the video" 这种句式不误判。
- 真正的缺节仍然触发重修（上一轮把上游的警告改成硬错误的那个决定，这次继续生效）。

### 3.6 保留标记判错（已修）

用例是给狗穿上平铺拍摄的人用夹克。官方判 `attribute_transfer`（颜色/绒条/口袋/纽扣转到一个狗穿的成衣上）；我们三次都判 `fully_preserved`。清单第 7 条给出判据（换载体/换形态 → attribute_transfer，且必须写明变了什么），复跑判对且理由与官方一致：

> the carrier and proportions change from a flat-laid human product photo to a dog-sized worn jacket

### 3.7 参考音频：只进标签（已修一半，方向已由需求方确认）

现网 `CompilerInput` 里只有图和视频，用户传音色参考而提示词一字不提，H3 只能自己编一条嗓子。现在 `metadata.reference_audios` 进 `CompilerInput`（排在视频之后），证据带 `<Audio N>`，`<Audio N>` 也纳入"引用不存在的素材"检查（一个音轨都没有时不查，避免把用户自己写的"音色参考音频1"判错）。

**做不到的**：增强模型听不到音频。四个免费 flash 模型对 `audio_url` 一律 `400 At most 0 audio(s) may be provided in one prompt`；只有收费的 `MiniMax-M3` 能读（实测能说出"female, young adult, warm, upbeat"）。需求方已确认**四个模型就是不支持，只能忽略音频内容** —— 所以现状（只给 `<Audio N>` 标签、不描述音色）就是最终形态，不再等 ASR。

### 3.8 参考视频（r2va）：两个独立根因，一个是我们的、一个是引擎的

现象是"同一段 4.4MB 素材 5 次尝试 4 次失败（500/504，146–465s）"，但 500 和 504 是两件不同的事。上机看日志（`ssh m` → GPUStack → `newapi-A100-0038` 的 `qwen3.8-flash-fp8-zxbed-run-0`）之后才分清。

**根因一（我们的，已修）：`response_format: json_object` 被语法约束拒掉 → 500。**

```
EngineCore ERROR [backend_xgrammar.py:168] Failed to advance FSM for request … for tokens 0
EngineCore ERROR [scheduler.py:2131] Unexpected: grammar rejected tokens [0] … Terminating request
APIServer  ERROR [serving.py:207] Request … failed with an internal error during generation   → 500
```

引擎是 qwen3.8（MTP 投机解码）+ xgrammar 结构化输出，首 token 就被判非法。关思考那一档尤其容易撞（一段 10 分钟窗口里 3 次）。`qwen_pe` 与 `h3_image_pe` 早前就因为这个不发 `response_format`（前者注释写着"约一半请求 500"），**singlecall 是唯一还在发的**，所以它是唯一还在 500 的。已改为不发，取 JSON 靠容错解析。

复跑对照（low + 8192 + 有效时长 + 内嵌清单）：

| 用例 | 发 response_format | 不发 |
| --- | --- | --- |
| t2v_dialog | 1989 | 1794 |
| i2v_ramen | 2341 | 2194 |
| i2v_cat | 1776 | 1729 |
| l2v_soda | 2114 | 1985 |
| r2v_shiba | 3669 | **3932** |
| r2v_official（视频） | 5 次里 4 次 500/504 | **74.5s 返回**（正文偏短，缺节校验会重修） |

都在跑间方差之内，但整类 500 消失。

**根因二（引擎侧，未修）：vLLM 自己去拉参考视频时超时。**

```
WARNING [connections.py:220] HTTP fetch failed for https://cdn.hailuoai.com/…mp4
        (attempt 1/3, timeout=30.000s):  -- retrying in 1.000s with timeout=120.000s
```

异常文本是空的（`asyncio.TimeoutError`），而**同一个容器里**用 curl、aiohttp、乃至 vLLM 自己的 `global_http_connection.async_get_bytes` 拉同一个 URL 都是 0.1–0.2 秒（4.4MB）。重试链 30s → 120s → 480s，正好解释 465 秒那个 504。属于偶发的连接/会话挂死，不是网络不通。

**方案 A（内联）试过、也跑通了，但没采用。** 把视频以 base64 data URI 内联，模型 5.2 秒就正确描述了画面 —— 这条路本身完全可行。但内联之后暴露了第三个问题：**开着思考会把正文挤没**。

同一段视频（内联后视频编码占 12288 token）：

| 配置 | 耗时 | 结果 |
| --- | --- | --- |
| `reasoning_effort=low`，max_tokens 8192 | 118.4s | 两轮都空正文（completion 7660/4098，reasoning 4095/4095） |
| `reasoning_effort=low`，max_tokens 16384 | 74.2s | 两轮都截断在半句 JSON 上，解析失败 |
| 关思考，max_tokens 8192 | **10.4s** | 1627 字符，六节完整 |

提到 16384 仍然截断 → 不是调参能救的，只能硬性关思考；而关思考又放弃了资产清点那点收益。加上每个视频固定多 12288 token，**成本与稳定性都不划算**，方案 A 与"带视频关思考"都已 revert（历史里可复活），等引擎侧修好再回来。

**当前采用方案 C：参考视频不发给增强模型。** 视频照常随**生成**请求发给 H3，只是不给增强模型；证据里保留 `<Video N>` 标签、角色与官方标号，并新增 `observable` 字段显式声明"这段你看不见"。

配套两件：

- 提示词侧（appendix 第 10 条）：看不见的素材只按标签+角色引用，**不得编造**画面、人物、服装、运镜；每个看不见的素材仍要有恰好一条定义行与一条保留行。第一版没有这条规则时，模型编出了一个完全无关的场景（"海岸山脊、深色长发、亚麻衬衫"，而素材是粉西装男抱着黑羔羊）。
- 校验侧（`missingAssetMentions`）：参考族里视频/音频标签在提示词中一次都没出现就报错 —— 实测出现过整段不提 `<Video 1>`。

实测（同一用例 `r2v_official`）：

| 做法 | 耗时 | 结果 |
| --- | --- | --- |
| 方案 C | 20–23s | 1747–2584 字符，六节齐，不编造外观 |
| 方案 A（关思考） | 10.4s | 1627 字符，但多 12288 视频 token |
| 原 URL | 74.5s | 382 字符残句 |

C 的产出现在是这种写法：

```text
<Subject 1> is the character from <Video 1>, whose appearance, clothing,
and physical identity are resolved directly from the reference video.
```

同一素材直连官方 Context-IR 54 秒成功。

## 4. 已落地的代码改动（PR #98，rebase 到 #97 合并后的 main）

1. 证据增加 `effective_duration_seconds`（`service/aggregate_enhance_singlecall.go` + `common/h3_duration.go`）。
2. 请求显式 `max_tokens`：默认 8192，配置项 `max_output_tokens` 可覆盖。
3. `reasoning_effort` 放行 singlecall（字段随 #97 已存在），干跑校验同步放行；档位原文不改写（`h3_image_pe` 有"原样透传"的测试）。
4. 参考音频进 `CompilerInput`，证据带 `<Audio N>`，纳入素材标号检查。
5. `service/h3v20/appendix.txt`（九条合规清单）+ 进 `buildSingleCallSystem`；`h3v20/README.md` 注明这是唯一非逐字移植的一份。
6. `normalizeSectionHeaders`（节名归一）与 `duplicateSections`（重复节校验）。
7. 出厂默认 `minimax-h3-2k` / `minimax-h3-ref-2k`：`reasoning_effort: "low"`、`timeout_seconds: 180`。
8. ~~参考视频内联成 data URI~~ / ~~带参考视频时关思考~~：都实现并实测过，按需求方决定**撤回**（方案 A 暂缓，见 §3.8），revert 提交留在历史里可随时复活。
9. **方案 C：参考视频/音频不发给增强模型**，证据新增 `observable` 字段显式声明可见性（图片随 `send_input_images` 为 true，视频/音频恒 false）；appendix 第 10 条要求看不见的素材只按标签与角色引用、不得编造；新增 `missingAssetMentions` 校验参考族的视频/音频标签必须出现。
10. **不发 `response_format`**：`json_object` 会被引擎的 xgrammar 拒成 500（见 §3.8 根因一），与 qwen_pe / h3_image_pe 一致。
11. 实验脚本入仓 `scripts/h3vid-pe/`，提示词拼接与生产逐字一致（含 appendix）。

`go build` 通过；`go test ./service/ ./middleware/ ./common/ ./relay/hilo/` 全绿。

## 5. 下一步

1. **r2va 结构稳定性**还剩一个口子：正文节名修好了，但"缺 `overall_soundscape`/`non_diegetic_music`"仍要靠重修兜（2 次里能修好 1 次）。中期方向仍是 open-h3-ir 那条：**节骨架由程序生成、模型只填正文**。
2. **参考视频**：现在走方案 C（只给 `<Video N>` 标签 + 时长）。等引擎侧修好两件事再回到"发给模型看"：① vLLM 自己拉远程视频偶发挂死；② 带视频时开思考正文被挤没。届时优先试方案 B（抽 3–5 个关键帧，12k token 降到 4–8k），A 次之。
3. **音频**：需求方已确认四个模型不支持音频输入 → 只留 `<Audio N>` 标签，不描述内容，不接 ASR。
4. 生产用量观察：`low` + 180 秒预算下的真实 p90/p99 与回落率（日志里已有 `fallback`/`repaired` 两列）。

## 6. 复现

脚本在 `scripts/h3vid-pe/`，用例素材与官方基线在实验包里（解包到 `.h3exp/`，可用 `H3PE` 覆盖路径）。

```bash
# 现网形状 / 候选配置（`-` = 不加额外规则）
MAAS_API_KEY=… python3 scripts/h3vid-pe/run_sc.py none runs/sc_none
MAAS_API_KEY=… MAXTOK=8192 EFF=1 python3 scripts/h3vid-pe/run_s2.py low runs/prod -

# 结构检查（先对官方基线校准，官方必须全绿）
python3 scripts/h3vid-pe/check.py runs/official runs/sc_none runs/prod
```

环境变量：`MAXTOK`（输出上限）、`EFF=1`（证据带有效时长）、`TAG`（输出文件名后缀）、`H3PE`（实验包 h3pe 目录）。

## 7. 上机排查记录（2026-11-20，生产集群）

- 入口：`ssh m`（生产 LLM 集群管理节点）→ GPUStack 服务在 `:80`，token 用运维给的那把。
- `GET /v2/model-instances` 找到 qwen3.8-flash-fp8 的三个副本：`newapi-A100-0038 / 0020 / 0007`（`na38 / na20 / na7`）。
- 我们那批失败全部落在 `na38` 的 `qwen3.8-flash-fp8-zxbed-run-0`：
  - `Failed to advance FSM`（xgrammar）6 次、`HTTP fetch failed` 2 次。
  - 另外两个副本同窗口内 0 次（要么没被调度到，要么日志留存更短）。
- 关键排除实验（都在容器内做）：
  - `curl -I` / `urllib` 拉那段 CDN 视频：200，4.4MB，0.2s；
  - `aiohttp` 同样 0.2s；
  - `from vllm.connections import global_http_connection` 调 `async_get_bytes`：0.1s。
  → 网络、TLS、DNS 都没问题，超时是引擎侧偶发。
- 官方 Context-IR 用同一素材 54 秒 200（`POST /v2/h3_context_ir`）。


## 8. 检视反馈的处理（PR #98 第二轮）

| 编号 | 反馈 | 处理 |
| --- | --- | --- |
| P1 | 参考视频带音轨时 `<Audio N>` 编号与引擎不一致 | **已改**：只要有参考视频，用户音频就不写数字（证据 `label_unknown: true`），提示词侧由 appendix 第 11 条要求按角色措辞引用；`labelIssues` / `missingAssetMentions` 对未知编号的音频相应放宽。没有参考视频时用户音频仍是 `<Audio 1>`（引擎按图→视频→音频排，这是确定的） |
| P2.1 | `max_tokens` 含思考 token | 注释写清：默认 8192 是按关思考标的，开档位后正文只剩扣除思考那部分；要更保险配 `max_output_tokens: 16384` |
| P2.2 | 出厂默认不自动生效 | 代码注释 + 本文说明：已有部署需在系统设置里手动改这两条聚合配置 |
| P2.3 | 最坏延迟 180 + 20 = 200 秒 | 在 `singleCallTimeout` 注释里写明这条链路是同步跑在客户请求里的，上线前要确认负载均衡/客户端超时 |
| P3.1 | `common/h3_duration.go` 超出范围（`IsH3VideoModel` 无人调用） | **已删**该函数与对应测试；文件头注释收窄为「提示词里的时长要用交付值」这一条真实理由 |
| P3.2 | 测试注释 `<Audio 3>` 与断言 `<Audio 2>` 不一致 | **已改** |
| P3.3 | 干跑不检查新字段 | **已加**：档位不在 none/low/medium/high 时警告（值仍原样透传）；`max_output_tokens` 负数或小于 2048 时警告 |
| — | 官方 `ref-en.txt:112` 与引擎对「参考视频自带音轨是否产生 `<Audio N>`」说法不一致 | **待上游确认**，见下 |

### 8.1 待上游确认：参考视频的音轨到底算不算 `<Audio N>`

- 引擎实现（vllm-omni `ordered_references.py` / `encoder_processing.py`）：参考视频带音轨时，会先给它一个 `<Audio j>`，再分配 `<Video k>`。
- 官方写作规范 `ref-en.txt:112`：普通参考视频不会仅因为文件带声音就产生 `<Audio N>`。

本 PR 按**引擎实现**来做（编号以引擎为准才不会指错素材），但因为网关不知道视频有没有音轨，最终采取「用户音频不写数字」的折中。等上游明确口径后，可以在素材物化时用 ffprobe / moov 解析查一次音轨，把编号补精确 —— 那会多一次网络读取，且落在客户的关键路径上，所以没在本 PR 做。
