# Qwen-Image-2.1 官方 PE 系统提示词（逐字移植）

**这个目录里的两份 `.txt` 是原文照抄**，不要在这里改措辞 —— 改了就没法和上游 diff。
需要补充的东西在 Go 侧追加（见下文），或整体替换成新版本。

上游仓库：`QwenLM/Qwen-Image-2.1`，提交 `fb7ae1d`。

| 本地文件 | 上游路径 |
|---|---|
| `system_prompt_t2i.txt` | `prompt_rewrite/prompts/system_prompt_t2i.txt` |
| `system_prompt_edit.txt` | `prompt_rewrite/prompts/system_prompt_edit.txt` |

校验和（用来确认本地没被人顺手改过）：

```
md5
a5e1efc49aad097648079b734969045f  system_prompt_t2i.txt
493264f3621269df0f9ff727671fba28  system_prompt_edit.txt

shasum -a 256 前 12 位
a77c9a06c59b  system_prompt_t2i.txt
e378fea686a1  system_prompt_edit.txt
```

## 谁在什么时候发哪一份

拼装在 `service/aggregate_enhance_qwenpe.go` 的 `buildQwenPESystem`：

- 没有输入图 → `system_prompt_t2i.txt`（产出 `rewritten_prompt` + `wh_ratio`）
- 有 1 张及以上输入图 → `system_prompt_edit.txt`（再多一个 `ratio_follow`）

## Go 侧追加了什么：一段 JSON 转义说明

两份原文都要求把画面里的文字放进**直双引号**（t2i 第 106 行、edit 第 187 行），
又要求整份回复是**严格合法的单行 JSON**，但从头到尾没说过引号要转义。官方 PE
模型是训练出来的、自己会转义；通用模型不会，于是 `reads "今日特调"` 这种裸引号
直接把 JSON 弄坏。

2026-09-25 实测（qwen3.8-flash 关思考，8 个最容易出错的用例 × 5 次）：
不加这段 34/40 可直接解析，加上之后 40/40；全量 27 用例 × 5 次 134/135。

追加位置：

- t2i：追加在末尾。
- edit：原文最后一行是 `The user's edit instruction to rewrite is:`，紧跟着就是
  用户的指令。转义说明**插在这一行之前**，这一行仍留在末尾 —— 插在它后面，
  模型会把这段英文当成要改写的指令本身。

## Go 侧还追加了五条补充规则（只针对 qwen3.8）

拿 qwen3.8 与官方 PE 逐项对比（27 用例 × 3 次），qwen3.8 偏离官方要求的地方有五处：
比例只会默认 3:2（营养表、春联、菜单也给横版）、位置含糊（"or"/"或"/"如"）、
先写光线纹理再写内容、多图合成误用 `ratio_follow`、编辑范围偏宽且保留条款不列文字。
五条规则逐条对应，跟在转义说明后面，原文一字不改（见 `qwenPEEscapeRule`）。
加上后：围栏 11→4、比例与 PE 众数一致 47→51、多图误用 1→0、副标题误改 1→0、
编辑逐字列出的保留文字 2→7 串（PE 8），保字仍 100%。含糊词没改善，PE 自己也有。

## 文生图另加"先分析后改写"，编辑不加，也不提篇幅

关思考后最明显的退化是画幅不稳。让模型把一段 ≤80 词的 analysis 写成 JSON 第一个键
（固定要素 / 画布 / 朝向及理由 / 要给的比例）再写改写结果，t2i 比例自洽 82%→88%，
逼近官方 PE 开思考（84~89%）。编辑侧同样的做法会把多图 `<imageN>` 用齐从 12/12
拖到 10/12，所以只给文生图加（`qwenPEAnalysisRule`）。
篇幅刻意不要求：任何字数要求（450~600 软要求、≥500 硬下限、紧挨朝向规则、与
analysis 叠加）都把比例自洽拉回 67~80%，在 qwen3.8 上篇幅与画幅稳定互斥。

## 别带 `response_format`

H3 singlecall 那条路带着 `response_format: {"type":"json_object"}`，这里**刻意不带**：
实测 qwen3.8（MTP 投机解码）+ 关思考 + json_object 约一半请求被引擎以
`grammar rejected tokens … Terminating request` 终止，返回 500。
