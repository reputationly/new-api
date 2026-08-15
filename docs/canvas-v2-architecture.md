# 画布 v2 架构与使用文档（重新 vendor 到 v0.15.1 + 自研功能）

> 版本：v2.0
> 日期：2026-08-15
> 状态：**开发完成，未上线**。画布仍处于灰度默认隐藏（`7fef4eb54`），实机验证通过前保持关闭。
> 上游基线：`basketikun/infinite-canvas` **v0.15.1**（commit `a2576d5`，2026-08-07，**MIT**）
>
> 本文档面向后续维护者（人或 agent）自包含编写。前置背景见 `canvas-integration-design.md`（v1 集成设计）与 `canvas-orchestration-design.md`（能力编排设计）。

---

## 一、这一版做了什么

v1 把上游 **v0.4.0**（Next.js 16 + AGPL-3.0）vendored 进来做成内置画布。上游在 v1 落地三天后（2026-06-26）一次性迁到 **Vite 7 + React Router 7**，此后又走了 11 个版本，累计 191 个提交、`web/` 下 187 文件 +36845/−7514，并在 v0.15.1 把协议从 AGPL-3.0 改成 **MIT**。

产品决策是**采用上游新版作为新基线**（界面更清爽、协议更宽松），把我们的适配、Agent 与从 tigerowo 借鉴的功能全部移植过去。这不是一次合并，是**重新 vendor + 全量移植**。

### 提交序列

| 提交 | 内容 |
|---|---|
| `9b61331cf` | M1 重新 vendor 到 v0.15.1，Next.js → Vite + React Router，删插件系统 |
| `744cb4dc7` | M2 回填 BUILTIN_MODE：`/pg` 渠道锁定、鉴权、服务端持久化 |
| `f410562f3` | M3 回填能力编排：19 能力注册表、任务链路、官方模板 |
| `eb8db6559` | 对 vendored 上游源码统一跑本仓 prettier（纯格式化，单独成提交） |
| `0675aa5ea` | M4 在线 Agent 回填：34 工具 + 按意图装配的技能手册 |
| `76c0a7b02` | M5 摄像机参数、工作流，并修分组名称不显示 |
| `7ffa99970` | M6-2 移植 tigerowo 创作工作流工作区 |
| `f25abbd24` | M6-3 自研 3D 导演台，程序化人体零外部资源 |

### 规模

- `web/canvas/src` 共 200 个 TS/TSX 文件、约 44800 行
- 其中 **37 个文件**含 `BUILTIN_MODE` 分支（我们的适配面）

---

## 二、整体架构

### 2.1 分层

```
浏览器
  └─ /canvas-app/*  （Go 单二进制 go:embed 伺服的 Vite SPA）
       ├─ pages/          路由页：canvas / image / video / prompts / assets / workflows / config
       ├─ components/     画布节点、面板、Agent、导演台、创作工作流
       ├─ lib/            纯逻辑：画布几何、Agent 工具、导演台 rig、工作流
       ├─ services/       API 客户端 + 能力注册表
       └─ stores/         zustand 状态
              │
              │  所有 AI 请求走相对路径 /pg/*（同源 → 同一个 Go 进程）
              ▼
new-api（Go）
  ├─ router/canvas-router.go   静态伺服 + SPA fallback + 开关门禁
  ├─ /pg/*                     UserAuth + KYCRequired + Distribute → relay → 计费
  ├─ /api/canvas/projects      画布工程服务端持久化
  ├─ /api/canvas/assets        素材库（二进制进 OBS）
  └─ /api/prompts              提示词库（服务端供数，不让浏览器直连 GitHub）
```

### 2.2 BUILTIN_MODE：一个开关收敛所有改动

上游是面向个人用户的 BYO-key 应用（自己填 baseUrl 和 API key、可加多个外部渠道、可连 WebDAV）。内置版是多租户 SaaS 的一个功能页，这两者的假设在鉴权、计费、存储三条链路上全不一样。

所有差异收敛到一个**构建期常量** `__BUILTIN_MODE__`（`vite.config.ts` 的 `define`，由 `VITE_BUILTIN_MODE=1` 打开）：

```ts
// src/stores/use-config-store.ts
export const BUILTIN_MODE = __BUILTIN_MODE__;
export const BUILTIN_CHANNEL_ID = "newapi-builtin";
export function builtinChannel(models: ChannelModel[] = []): ModelChannel {
    return { id: BUILTIN_CHANNEL_ID, name: "站内", baseUrl: "/pg", apiKey: "", apiFormat: "openai", models };
}
```

**为什么用构建期常量而不是运行时配置**：非内置模式下这些分支会被 tree-shake 掉，上游行为一字不改；同时 `grep BUILTIN_MODE` 能一次列全我们的改动面（当前 37 个文件），下次跟进上游时这就是逐条对账的清单。

关键收敛点：

| 关注点 | 做法 |
|---|---|
| 渠道 | `normalizeChannels` 在 BUILTIN_MODE 下丢弃一切持久化的外部渠道，只留 `/pg` |
| URL 构造 | `buildApiUrl()` 是唯一汇流点，把 `/pg` 当作「已带版本号」不再拼 `/v1` |
| 鉴权 | `lib/builtin-auth.ts` 注入 `New-Api-User` 头；401 时跳 `/login?expired=true` |
| 模型分类 | 按后端 `supported_endpoint_types` 判定（`image-generation`→图、`openai-video`→视频、`audio-speech`→音频，其余文本），**不按模型名关键词猜** |
| 外观 | 主题锁浅色（默认值、`setTheme` 短路、persist merge 三处缺一不可），语言锁 `zh-CN`，两个切换入口都不挂载 |
| 隐藏 | WebDAV、版本检查（`checkLatestVersion` 会在 `useEffect` 自动跑，两个函数都要挡）、URL 注入 `?baseUrl=&apiKey=` |

### 2.3 SPA fallback（迁移到 Vite 后的必改项）

v0.4.0 是 Next.js 静态导出，未知深链返回 404.html；Vite 是单页应用，`/canvas-app/canvas/<id>` 这类深链**必须回落到 index.html**，否则刷新即 404。

```go
// router/canvas-router.go
if !canvasFileExists(httpFS, strings.TrimPrefix(c.Request.URL.Path, "/canvas-app")) {
    // 只有真实静态资源(带扩展名的 js/css/图片等)缺失才算 404
    if path.Ext(c.Request.URL.Path) != "" {
        c.Status(http.StatusNotFound)
        return
    }
    c.Data(http.StatusOK, "text/html; charset=utf-8", indexPage)
    return
}
```

同时 `main.go` 的 embed 从 `web/canvas/out` 改成 `web/canvas/dist`。

### 2.4 插件系统：整体移除

上游支持远程加载第三方节点插件。多租户下远程插件同源执行能读 session cookie 与 `localStorage['uid']`，等于给第三方代码开账号权限，因此**整体移除**远程加载层。

**注意**：`lib/canvas/node-registry.ts` **保留**——内置的 text/image/video/audio/config/group/director 七种节点都以 `pluginId="builtin"` 注册在这里，删掉会让所有节点失效。移除的只是远程加载与插件管理 UI。

代价：失去上游的 Markdown/SVG/HTML/3D 全景/便利贴节点；与上游后续版本差异变大。

---

## 三、四个功能子系统

### 3.1 能力编排（19 个能力）

画布节点不再只有「图/视频/音频」三种粗粒度生成，而是由**能力注册表**驱动：

```
图片(2)  t2i i2i
视频(8)  t2v i2v flf2v s2v sr vace repaint v2a
音频(4)  tts_emotion tts_synth tts_dialogue tts_design
音乐(5)  t2m cover t2a r2va svs
```

注册表（`services/capabilities/registry.ts`）为每个能力定义：模型下拉的筛选条件、参数白名单、输入槽位（slot）及其必选性。节点面板按 `capability` 渲染，而不是按节点类型。

**任务链路**：异步能力（gpustackplus）返回 `taskId`，下游节点以 `task:<id>` 引用产物——后端 NFS 直读，前端零二进制搬运。`taskMediaKey` 与 `storageKey` 一致才允许引用，节点媒体被替换后即失配，防止下游消费旧产物。

**`stalled` 状态**：轮询超时 ≠ 失败，任务仍在服务端跑。节点进入 `stalled`，点一下即按 `taskId` 恢复轮询。

详见 `canvas-orchestration-design.md`。

### 3.2 在线 Agent（34 个工具）

上游在 v0.6/v0.7 **删掉了**前端直调 responses 的在线 Agent，改成只连本地 Codex + MCP。内置模式下用户没有本地 Codex，这条链路必须我们自己维护。

- 恢复了 `requestToolResponse` 与三个类型（上游只删了导出，底层调用链完整）
- `canvas-assistant-panel.tsx` 成为 new-api 自有文件，挂进上游 `agent-panel`：BUILTIN_MODE 且有画布上下文时走在线 Agent，否则回落上游本地面板
- 34 个工具：能力工具（建能力节点、连线、跑生成、等待）+ 图结构工具（读节点、追溯上下游、排版、分组）

**技能手册按意图装配**，不是一股脑塞给模型：

| 输入 | 装配结果 | 提示词长度 |
|---|---|---|
| 闲聊 | 仅 core | 4.8k 字 |
| 「整理一下画布」 | core + 整理 | 5.5k |
| 「用温柔女声念旁白」 | core + 声音 | 6.0k |
| 「接着上段视频继续拍」 | core + 视频 + 续接 | 7.0k |
| 「做一支 30 秒宣传片」 | core + 剧本拆解 + 视频 + 多镜头 | 7.8k |

**排版与分组的一个坑**：分组节点没有连线，走拓扑分层会被算成第 0 层从而与成员分离。正确做法是把分组排除在分层之外，成员排完后按包围盒重算组框。

### 3.3 摄像机参数（M5）

8 机身 / 8 镜头 / 11 焦距 / 10 光圈，挂在提示词面板（能力节点按 `modality` 判断，音频与纯文本不显示）。

**核心约束：参数只进上游请求体，绝不写回 `metadata.prompt`。** 实现上另起 `requestPrompt` / `capRequestPrompt` / `retryRequestPrompt` 三个变量喂给 8 处请求调用，8 处 metadata 落盘仍用原始提示词。

这不是洁癖——若回写，**重试与续接会把镜头描述反复叠进提示词**，越叠越长且用户看不出来源。

### 3.4 工作流（两套，不要混淆）

| | 画布工作流（M5） | 创作工作流工作区（M6-2） |
|---|---|---|
| 位置 | 画布工具栏 | 独立页 `/workflows` |
| 形态 | 节点子图模板 | 表单驱动的批量出图 |
| 存什么 | 节点、连线、能力、参数、提示词 | 提示词模板 + 类型化变量 |
| 变量 | `{{名字}}` 占位符 | 文本/多行/数字/下拉/开关 |
| 特有 | id 重映射、几何归组 | 系列图、AI 起草、出图历史与分类 |

两者都**只存本机 IndexedDB**，不跟账号同步（产品决策：先不做服务端表），UI 上如实告知，跨设备用「导出 JSON」。

**画布工作流的存储原则**：存「怎么做」不存「做出来的东西」。媒体 `content`/`storageKey`/`taskId`/`taskMediaKey` 全剥离——`blob:` 刷新即失效，`data:` 会把工作流 JSON 撑到几 MB；文本节点正文保留（那是配方的一部分）；状态重置 `idle`。

### 3.5 3D 导演台（M6-3）

给图片/视频生成提供**构图与姿势参考图**。摆好人和道具、架好机位、截图，截图直接进画布当参考。

**与 tigerowo 的做法不同**：他们是塞在 `public/director/` 的独立预构建 SPA（1.4MB 无源码 bundle + 750KB GLB 模型），靠 7 条 postMessage 跟宿主通信。我们拆开 bundle 还原协议与数据结构后**重写成应用内 React 组件**——省掉黑盒与独立构建产物，省掉 Sketchfab 模型授权，能直接用我们的主题。

```
lib/director/
  rig.ts        15 组 36 个语义控制器 + 稀疏姿势规范化 + 左右镜像
  poses.ts      20 个姿势预设（数值从 bundle 标定回来）
  project.ts    工程数据形状 + 规范化（纯 JSON，随节点 metadata 走）
  humanoid.ts   程序化人体：18 关节骨骼层级，8 种体型，零外部资源
  scene.ts      three.js 场景：增量对账同步、拾取、离屏截图
components/director/
  director-viewport.tsx   轨道相机 + 取景遮罩 + 九宫格
  director-stage.tsx      主界面：对象列表、属性/姿势面板、截图
```

**语义控制器而不是裸欧拉角**：直接给关节 XYZ 三个角度，用户要试好几次才知道哪个轴对应「抬手」。这里每个控制器对应一个人能直接说出口的动作（前举/外展/扭转），值是角度，稀疏字典存储——站立就是空字典 `{}`。

**坐标与符号约定**（整套 rig 建立在此，改一处要连带改预设）：

```
右手系，Y 轴向上，角色面朝 +Z，角色自身的左手边是 +X
绕 X 正转：向上的骨骼向前(+Z)倒，朝前的面朝下 → 「点头」是 +rotX
绕 Y 正转：面朝转向角色自身左侧
绕 Z 正转：下垂的肢体摆向角色自身左侧(+X)
```

符号由预设值反推：髋前抬为正、膝屈为正、躯干前倾为**负**、点头为正。**肘与膝的弯曲方向相反是解剖学正确**（肘向前屈、膝向后屈），不是笔误。

**几个刻意的设计取舍**：

- **人体程序化生成**：导演台只需要「体块与朝向对、能摆姿势」的参考人形，不需要皮肤与面部。换体型只是换一组比例系数，不是换文件。
- **儿童不做等比缩小**：头身比明显更大、腿更短，等比缩下去看着像远处的成年人。
- **头上加鼻锥**：没有五官时那是唯一能一眼看出朝向的东西，而截图当参考图时朝向是关键信息。
- **脚掌厚度由脚踝高度反推**：脚踝在解剖学正确的 9cm，脚掌必须正好垫到 y=0——地面接触是判断姿势可信度的第一眼线索。
- **截图走离屏渲染**：另开一张目标比例的画布单独渲一次，取景遮罩、九宫格、选中框天然不会进产物。
- **three.js 懒加载**：571KB 独立 chunk，只有真打开导演台才下载。
- **截图不自动连线**：导演台本身不产出媒体，连过去下游读不到东西反而误导。

---

## 四、怎么用

### 4.1 开发

```bash
cd web/canvas
bun install
bun run dev                                   # 开发服务器（非内置模式，可自填渠道）

# 内置模式构建（Go embed 用的就是这个产物）
VITE_BASE=/canvas-app/ VITE_BUILTIN_MODE=1 bun run build

cd ../..
go build -o /tmp/newapi .                     # dist/ 被 go:embed 进二进制
```

四线检查（提交前都要绿）：

```bash
cd web/canvas
bunx tsc --noEmit -p tsconfig.json
bunx prettier --write "src/**/*.{ts,tsx}"
VITE_BASE=/canvas-app/ VITE_BUILTIN_MODE=1 bun run build
cd ../.. && go build -o /tmp/newapi .
```

### 4.2 画布基本流程

1. 顶部导航 →「画布」→ 新建项目
2. 工具栏加节点：文本 / 图片 / 视频 / 音频 / 配置 / 分组 / **3D 导演台** / **工作流** / 能力节点（「扩展」面板里按产物分类）
3. 连线表达依赖：上游产物自动作为下游输入；同类多输入用**槽位绑定**区分（如首帧/尾帧、说话人 1/2）
4. 节点面板选模型、调参数、写提示词 → 生成
5. 异步能力产出 `taskId`，下游以 `task:<id>` 引用，不搬运二进制

### 4.3 在线 Agent

右侧面板 → 描述你要什么（「做一支 30 秒运动鞋宣传片」）。Agent 会：列能力 → 建节点 → 连线 → 跑生成 → 等待落地 → 排版分组。技能手册按你的话自动装配，无关领域的不会塞进上下文。

### 4.4 3D 导演台

1. 工具栏 →「3D 导演台」建节点 → 点「打开导演台」
2. 左栏加角色（8 种体型）或几何体；视口里点选对象
3. 右栏「姿势」标签：20 个预设一键套用，或用 36 个语义滑杆细调；支持左右镜像与重置
4. 拖动视口转相机，或在右栏精确填 FOV / 位置 / 注视点
5. 选画幅比例（8 种）、开九宫格辅助线
6. 截图：「截图」当前视角 / 「四方位」/ 「十二方位」环绕
7. 「发送到画布」→ 截图落成图片节点排在导演台右侧，自己拉线接给需要的能力节点

**姿势预设是稀疏覆盖不是整体替换**：应用预设 = 用预设的键覆盖当前姿势，未提及的键保持不动。所以可以先摆好上半身再套一个腿部预设。想干净重来用「站立」（空字典）或「重置姿势」。

### 4.5 创作工作流（`/workflows`）

1. 「保存选中」建模板，或点「AI 创建工作流」用自然语言描述需求让模型起草
2. 模板 = 提示词模板 + 一组类型化变量（`{{变量key}}` 在模板里引用）
3. 运行时填变量 → 批量出图；多图系列模式会先让文本模型拆出 N 条分镜提示词，审核后再出图
4. 出图历史与分类库都在本地

---

## 五、可能的问题

### 5.1 高风险（上线前必须实机验证）

| 风险 | 说明 | 为什么编译期看不出来 |
|---|---|---|
| **鉴权链路漏回填** | 某个请求没走 `/pg` 或没带 `New-Api-User` | 类型正确、构建通过，但请求到了服务端才被拒或误记到别人账上 |
| **计费链路漏回填** | 绕过 `/pg` 直连上游 = 不计费 | 同上 |
| **存储链路漏回填** | 项目没落服务端、素材没进 OBS | 本地 IndexedDB 有缓存，单机测试看不出来 |
| **导演台渲染效果** | 姿势/比例/光照实际长什么样 | WebGL 无法无头验证，几何与矩阵数学已测但「看着对不对」得眼睛看 |

BUILTIN_MODE 覆盖鉴权、计费、存储三条链路，**任何一条漏回填都是线上事故**。

### 5.2 中风险

- **上游仍在快速变动**：v0.15.1 到本次开发时才 7 天，`Unreleased` 已有新条目。**锁定 tag 作为基线，不要跟 main**。
- **`pages/canvas/project.tsx` 是重灾区**：3000+ 行，我们的改动分散在生成链路各处。下次跟进上游时这个文件只能逐处理解语义后重新落位，不能机械 diff。
- **主包体积 3.6MB**（gzip 1.15MB）。导演台与 mp4box 已拆出去，主包仍然偏大，首屏慢。若要优化，下一个该拆的是创作工作流工作区与 antd。
- **提示词封面图仍是外链**：`cover_url` 指向 GitHub raw（491 条）、shields.io（254 条，实为误判的徽章）、twimg（19 条），`cover_asset_url` 从未填充。内网或墙内环境下封面加载失败。修法是在 `cmd/canvas-prompts-sync` 里下载并落 OBS（约 100 行 Go + 一个表字段 + 前端优先级），顺带清掉 254 条误判徽章。

### 5.3 已知限制（有意为之）

- 工作流（两套）都不跨设备同步——需要后端加表，是独立的一步
- 导演台没有：全景球背景、FBX/OBJ 导入、群众阵列、多机位管理。前三个依赖外部资源或文件导入，与「零资源、纯 JSON 随节点走」的取向冲突；多机位是纯增量，需要时再加
- 插件系统移除后失去上游的 Markdown/SVG/HTML/3D 全景/便利贴节点
- 只保留浅色主题与中文

### 5.4 维护时容易踩的坑

这些都是本轮实际踩过并修掉的，写下来避免重犯：

1. **主题锁定要改三处**：默认值、`setTheme` 短路、persist `merge`。少一处，老用户 localStorage 里存的 `dark` 就会复活。
2. **版本检查有两个入口**：`checkLatestVersion` 会在 `useEffect` 自动跑，只挡手动那个没用。
3. **`node.type` 现在是开放字符串** `CanvasNodeTypeId`：索引查找要么widen 成 `Record<string, ...>` 要么显式断言。
4. **`nodeContentRenderers` 对 `CanvasNodeType` 是穷尽的**：加节点类型必须同时加渲染器，否则 `satisfies` 报错。
5. **id 重映射要先按旧 id 过滤再映射**：`.map(id => idMap.get(id)).filter(id => idMap.has(id))` 是错的——映射完再查 `idMap.has(newId)` 恒为 false，会把绑定全清空。
6. **工作流剥离 `content` 要区分节点类型**：文本节点的 content 是正文（必须留），媒体节点的 content 是产物地址（必须删）。
7. **上游分组节点忽略 `node.title`**：`GroupNodeContent` 写死显示「分组」，也没有颜色支持。已修，但下次跟进上游会被覆盖回去。
8. **摄像机参数不能回写 `metadata.prompt`**：见 3.3。
9. **`update_node` op 的 `patch` 和 `metadata` 都是合法一等字段**，applier 两者都合并——看到两种写法不要以为是 bug。
10. **`registerBuiltinNodes()` 在 `project.tsx` 模块顶层调用**：任何无头测试若不先调它，`isRegisteredNodeType` 全返回 false，`add_node` 会静默退化成文本节点。

---

## 六、怎么测

### 6.1 自动化（当前手段）

本仓画布部分**没有测试框架**。本轮的验证方式是用 `bun run` 直接跑纯函数的一次性脚本——快、够用，但不留在仓库里。若要沉淀，建议引入 vitest 并把下面这些断言固化。

**能测的（纯逻辑，无需浏览器）**：

```bash
# 示例：直接 import 纯模块跑断言
bun run /tmp/check.ts
```

| 模块 | 该断言什么 |
|---|---|
| `lib/director/rig.ts` | 量程 min<max、0 在量程内、侧向轴左右镜像而前后/弯曲轴相同、规范化丢未知键/夹量程/抹零、镜像两次回原样 |
| `lib/director/poses.ts` | 20 个预设 id 唯一、只用已定义的控制器、值不被量程夹、站立是空字典、merge/replace 语义 |
| `lib/director/project.ts` | 垃圾输入存活、未知体型/图元回落、空机位表补默认、幻影 activeCameraId 纠正、缩放 0 兜底、幂等 |
| `lib/director/humanoid.ts` | 八种体型站立**精确贴地**（`Box3.min.y ≈ 0`）、20 个预设世界坐标符合解剖学、肘膝反向、无 NaN |
| `lib/canvas/canvas-workflow.ts` | 产物剥离、文本正文保留、变量替换、`@[node:]` 与 slotBindings 重映射、未填变量保留占位符 |
| `lib/canvas/agent/*` | 技能手册引用的工具名与能力 key 全部真实存在（零幻觉）、schema 声明参数与代码读取一致、图操作经真实 applier 端到端 |
| `lib/canvas/canvas-camera.ts` | 关闭时原样返回、拼接是纯函数、非法 id 回落、空提示词不产生前导逗号 |

**关键的三个反例断言**（本轮真正抓到问题的）：

1. `Box3.setFromObject(humanoid).min.y ≈ 0` —— 抓到了「脚踝解剖学正确但脚掌网格短了 3.5cm，整个人悬空」
2. 技能手册工具名 ∈ 真实工具集 —— 防止手册里写了不存在的工具，模型照着调必然失败
3. 工程规范化幂等 —— `normalize(normalize(x)) === normalize(x)`，防止读一次改一次

### 6.2 手动（上线前必做）

**M1 骨架**
- [ ] 访问 `/canvas-app/` 打开首页
- [ ] 直接访问 `/canvas-app/canvas/abc` 并刷新，**不 404**
- [ ] 未登录跳 `/login`
- [ ] 后台关闭画布开关后返回 404/403

**M2 BUILTIN_MODE（最高优先级）**
- [ ] 网络面板确认**所有** AI 请求打到 `/pg/*` 且带 `New-Api-User`
- [ ] 配置页看不到外部渠道与 API key 入口
- [ ] 新建项目后 `GET /api/canvas/projects` 能查到
- [ ] 上传素材后 `/api/canvas/assets` 有记录且配额条更新
- [ ] **换浏览器登录同账号能看到项目**（验证服务端持久化真的生效，而不是本地 IndexedDB 在骗你）
- [ ] 手动往 localStorage 写 `{"state":{"theme":"dark"}}` 后刷新**仍是浅色**
- [ ] 写 `infinite-canvas:locale=en-US` 后刷新**仍是中文**
- [ ] 顶栏与画布工具栏都看不到明暗切换与语言切换
- [ ] 会话过期后 401 跳 `/login?expired=true`

**M3 能力编排**
- [ ] 19 个能力逐个建节点 → 生成
- [ ] `t2i → i2v → sr` 三节点链跑通，中间产物以 `task:<id>` 引用
- [ ] 能力标签与 `constant/model_capability.go` 对账零差异
- [ ] 轮询超时后节点进 `stalled`，点一下能恢复轮询

**M4 在线 Agent**
- [ ] 让 Agent 跑一次「列能力 → 建 t2i → 等生成 → 截帧 → flf2v 续接 → 排版」
- [ ] 分组后组框正确包住成员，成员移动后组框跟随

**M5 摄像机与工作流**
- [ ] 开启摄像机生成一次，**检查节点 `metadata.prompt` 里没有镜头描述**（只在请求体里）
- [ ] 同一节点重试三次，提示词不应越来越长
- [ ] 截尾帧画面非黑
- [ ] 两段同参数视频拼接后时长等于两段之和
- [ ] 工作流存取 + 变量替换 + id 重映射；插入后分组归属由几何重算

**M6-2 创作工作流**
- [ ] AI 起草：给一段需求，检查产出的模板与变量合理、告警提示正确
- [ ] 模型返回坏 JSON 时报错而不是产出半成品工作流
- [ ] 单图与系列图两种模式各跑一次

**M6-3 导演台**
- [ ] 打开后人体比例正常、站立贴地、面朝镜头（看鼻锥）
- [ ] 20 个预设逐个点一遍，看着像那个动作
- [ ] 8 种体型切换，儿童明显是儿童比例而非缩小的成人
- [ ] 拖动滑杆实时响应，左右镜像正确
- [ ] 画幅比例切换后取景框跟随；九宫格开关有效
- [ ] 截图产物**不含**取景遮罩、九宫格、选中框
- [ ] 四方位/十二方位环绕后相机**回到原位**
- [ ] 关闭再打开，场景完整恢复（工程随节点 metadata 持久化）
- [ ] 首次打开时才加载 `director-stage` chunk（Network 面板确认）

**全局**
- [ ] `bunx tsc --noEmit`、`bunx prettier --check`、`bun run build`、`go build` 全绿
- [ ] 画布灰度开关开启后完整走一遍上述流程

---

## 七、跟进上游的建议

1. **锁 tag，不跟 main。** 决定升级时先看上游 CHANGELOG 的破坏性变更。
2. **先 `grep -rl BUILTIN_MODE web/canvas/src` 拿到我们的改动面清单**（当前 37 个文件），逐个确认新版本里对应位置是否还在。
3. **`pages/canvas/project.tsx` 单独处理**：逐个函数比对，不要机械 diff。
4. **格式化单独成提交**：上游的 prettier 配置与本仓不同，先跑一遍格式化并单独提交，后续功能 diff 才干净（见 `eb8db6559`）。
5. **对账脚本先跑**：能力 key 对 `constant/model_capability.go`、Agent 工具名对实际工具集。这两个对不上时，UI 看起来正常但运行时必错。

---

## 八、相关文档

- `canvas-integration-design.md` —— v1 集成设计（入口形态、权限模型、`/pg` 复用机制）
- `canvas-orchestration-design.md` —— 能力编排设计（19 能力、槽位、任务链路）
- `web/canvas/NOTICE.md` —— vendor 来源、基线 tag、授权与本地修改清单
- `pkg/billingexpr/expr.md` —— 计费表达式系统（画布生成走的就是这条计费链）
