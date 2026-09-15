# H3 提示词编译所需的上游原文（逐字移植）

**这个目录里的文件全是原文照抄**，不要在这里改措辞 —— 改了就没法和上游 diff。
需要调整时在 Go 侧追加，或整体替换成新版本。

上游仓库：`XINGSHEN2/minimax-H3-context-IR`

## 文件与出处

**各文件的上游提交号不同**，别当成一个版本：官方 skill 与协议规范是 8 月的，
singlecall 编译器是 9 月的。同步时按行核对，不要整目录一把梭。

| 本地文件 | 上游路径 | 提交 | 日期 |
|---|---|---|---|
| `rules.txt` | `backend/single_call_compiler.py` 的 `RULES` | `4714f9f` | 2026-09-10 |
| `writing.txt` | `backend/compact_writer.py` 的 `COMPACT_WRITING_INSTRUCTIONS` | `4714f9f` | 2026-09-10 |
| `skills/prompt-writing.SKILL.md` | `skills/h3-prompt-writing/SKILL.md` | `f02b12c` | 2026-08-18 |
| `skills/base-en.txt` | `skills/h3-prompt-writing/references/base-en.txt` | `f02b12c` | 2026-08-18 |
| `skills/ref-en.txt` | `skills/h3-prompt-writing/references/ref-en.txt` | `f02b12c` | 2026-08-18 |
| `skills/shot-planning.SKILL.md` | `skills/h3-shot-planning/SKILL.md` | `84b88a2` | 2026-09-09 |

移植时上游 HEAD：`33c902a`（2026-09-15）。

校验和（`shasum -a 256` 前 12 位，用来确认本地没被人顺手改过）：

```
50040f5667bc  rules.txt
6ae25ad1c6c6  writing.txt
a48f9db1231c  skills/prompt-writing.SKILL.md
126666f6cd5e  skills/shot-planning.SKILL.md
2cfebc096a6e  skills/base-en.txt
1e574f356716  skills/ref-en.txt
```

## 谁在什么时候发哪几份

拼装在 `service/aggregate_enhance_singlecall.go` 的 `buildSingleCallSystem`：

    prompt-writing.SKILL.md → base-en.txt → [ref-en.txt] → shot-planning.SKILL.md
      → rules.txt → writing.txt → 证据 JSON

`ref-en.txt` **只在参考族（r2va）发**：它有 23 KB、只讲全参考那六节，帧族用不上，
而这段提示词是每个请求都要发一遍的。`base-en.txt` 一律发 —— 上游注释点明参考族
也要用它的台词与运镜规则。

**`AGENTS.md` 刻意不带。** 上游会带，但那是它的仓库指令，里面写着
`Produce only Context-IR JSON in the final response` —— 对 singlecall 是错的
（它要的是 `content_plan` + `h3_prompt`），带上去等于给模型两条互相矛盾的输出
契约。其中唯一属于协议的那句（改写正文用英文、台词保留原语言）在
`prompt-writing.SKILL.md` 的 Output Rules 里已经有了。

## 三件必须知道的事

**一、协议规范必须一起发给模型。** `writing.txt` 第 2 行写着
`Follow the official H3 writing guide and shot-planning skill`，但普通 chat
端点没有文件系统工具，模型执行不了"去读那份指南"。上游的 direct 运行时正是为此
把这几份文件拼进系统提示词，它的注释说得很直白：

> Direct Chat has no filesystem tool. The skill index alone
> cannot execute its instruction to read the protocol guides.

漏掉它们的后果是**静默降质**：三节名称、`[Shot N]` 记法、`<d>[Language]` 台词
标签全都无从谈起，而产出看起来仍然像模像样。移植的第一版就漏了，靠检视才发现。

**二、上游还有个 v27，但没开源。** `examples/showcase` 里发布了 v27
（「连续动作与镜头衔接」）的**成片**，编译器代码和提示词都没进仓库，
`COMPILER_REVISION` 仍是 v20。所以 v20 是目前能拿到的最新实现，不是最新版本。

**三、别再照着 `render_h3_prompt` / `audit_h3_prompt` 移植。** 那是 **v20 之前**
的架构（模型吐 canonical IR → 程序确定性渲染 → 独立审计），上游 2026-09-10 就
换掉了，现在只剩 `agent.py` 里的兼容路径在用，文档明确列为「历史与设计资料，
不应作为当前服务实现说明」。

我们的 `relay/hilo/*`（mode=ir）正是照着那套写的 —— 移植时间 09-14，比上游废弃
它晚了四天。教训：**照当前工作流文档移植，不要照着仓库里还能跑的函数移植。**

v20 自己把这一点写进了提示词：`This single call produces both; do not introduce
an audit stage.`
