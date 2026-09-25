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

## 别带 `response_format`

H3 singlecall 那条路带着 `response_format: {"type":"json_object"}`，这里**刻意不带**：
实测 qwen3.8（MTP 投机解码）+ 关思考 + json_object 约一半请求被引擎以
`grammar rejected tokens … Terminating request` 终止，返回 500。
