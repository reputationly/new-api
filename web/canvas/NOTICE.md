# NOTICE

本目录代码 vendored 自开源项目 [infinite-canvas](https://github.com/basketikun/infinite-canvas)（**MIT**）。

- 来源仓库：`github.com/basketikun/infinite-canvas`
- 基线 tag：**`v0.15.1`**（commit `a2576d5`，2026-08-07）
- vendor 范围：上游仓库 `web/` 子目录（排除 `node_modules/`、`dist/`），另从上游仓库根目录拷入 `LICENSE`、`VERSION`、`CHANGELOG.md`
- 授权：MIT，保留上游 LICENSE 与作者署名（见 `LICENSE`）

> 上游于 v0.15.1（2026-08-07）由 AGPL-3.0 改为 MIT。
> 上一版 vendor 基线是 v0.4.0 @ `bd0ad0a`（Next.js，AGPL-3.0）；上游已于 2026-06-26
> 迁移到 Vite + React Router，本次一并跟进。

## 技术栈差异（相对上一版 vendor）

| | 旧（v0.4.0） | 现（v0.15.1） |
|---|---|---|
| 框架 | Next.js 16 App Router | Vite 7 + React Router 7 |
| 产物 | `output:"export"` → `out/` | `vite build` → `dist/` |
| base path | `basePath:"/canvas-app"` | `VITE_BASE=/canvas-app/` |
| 路由 | 静态导出不支持动态段，被迫改成 `editor/?id=` | `/canvas/:id` 原生支持，workaround 已撤销 |

构建命令：`VITE_BUILTIN_MODE=1 VITE_BASE=/canvas-app/ bun run build`

## 本地修改清单

所有内置模式相关改动以 `__BUILTIN_MODE__` 常量收敛，可用 `BUILTIN_MODE` 关键字 grep 定位。

### 已完成（M1 骨架）

1. `vite.config.ts`：`VERSION`/`CHANGELOG.md` 读取路径 `../` → `./`（vendor 布局下二者与本文件同级）；新增 `__BUILTIN_MODE__` define；移除上游的 `localPluginsManifest()` 插件。
2. **移除节点插件系统的远程加载与管理**：删除 `lib/canvas/plugin-loader.ts`、`plugin-registry.ts`、`plugin-runtime.ts`、`stores/canvas/use-plugin-store.ts`、`components/canvas/canvas-plugin-manager-modal.tsx`、`public/plugins/`，以及顶栏插件入口。
   **保留** `lib/canvas/node-registry.ts`、`plugin-node-context.ts`、`types/canvas-plugin.ts`、`nodes/builtin-nodes.tsx`、`hooks/use-plugin-host.tsx` 的 host 能力对象 —— 内置的 text/image/video/audio/config/group 六种节点本身就注册在 node-registry 上，删掉会连普通节点一起废掉。
   理由：远程插件在画布同源执行，能读 session cookie 与 `localStorage['uid']`，等于给第三方代码开账号权限。工具栏「扩展」按钮的显示条件是注册表里存在非内置节点定义，插件装不进来后恒不渲染，自动失效。
3. **固定浅色 + 简体中文，隐藏切换入口**：
   - `stores/use-theme-store.ts`：默认值改 `light`；`setTheme` 在内置模式短路；`merge` 忽略已持久化的旧值（老用户可能存着 dark）。
   - `i18n/index.ts`：`lng` 固定 `zh-CN` 不读 localStorage，`supportedLngs` 收窄；`changeAppLocale` 短路。`en-US` 词表保留（`fallbackLng` 仍指向它的兄弟，删了要动一堆引用）。
   - 移除 `components/layout/user-status-actions.tsx` 的语言按钮与 `AnimatedThemeToggler`、`components/canvas/canvas-toolbar.tsx` 外观面板里的明暗切换（网格样式等画布配置保留）。
4. `lib/canvas/canvas-generation-helpers.ts:51`：补一个可选链（上游此处直接解引用 `node.metadata`，TS 收窄不过来），行为不变。

### 待回填（M2–M5，见 `~/.claude/plans/`）

- BUILTIN_MODE 全套：`/pg` 渠道锁定、禁 BYO key、`New-Api-User` 头注入、401 跳登录、隐藏 WebDAV/版本检查、模型按 `supported_endpoint_types` 分类、画布项目服务端持久化、素材库 OBS。核心钩子仍是 `stores/use-config-store.ts` 的 `buildApiUrl()`。
- 能力编排（19 能力注册表 + execute + 能力节点面板 + 官方模板）。
- 在线 Agent（assistant-panel + 32 工具 + 领域技能手册 + `requestToolResponse`）。
- 摄像机参数、视频截帧、拼接成片、用户工作流、素材语义角色。
