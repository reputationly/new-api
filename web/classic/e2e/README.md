# classic 前端测试

两层，解决的问题不同：

| 层                         | 命令           | 覆盖什么                                     |
| -------------------------- | -------------- | -------------------------------------------- |
| 组件测试（vitest + jsdom） | `bun run test` | 单个组件的行为：光标稳定性、序列化、价格计算 |
| 端到端（Playwright）       | `bun run e2e`  | 页面能不能打开、接口通不通、跳转到不到位     |

## 组件测试

```bash
bun run test            # 跑一遍
bun run test:watch      # 监听
bun run test:coverage   # 覆盖率
```

不需要后端，不需要任何环境变量。

`src/test/setup.js` 里补了四类 jsdom 缺口，**每一条都有具体原因，删任何一条都会让
某些用例以「产品有 bug」的样子失败**：

- **初始化 i18n** —— 不初始化时 `useTranslation()` 每次渲染返回新的 `t`，而 `t` 是
  表格 `columns` 的直接依赖，于是 columns 每次敲键都重建、输入框光标跳到末尾。
  这是环境造出来的假象，生产里 `src/index.jsx` 一启动就初始化了。
- **localStorage** —— Node 22 自带一个实验性的同名全局，未加 `--localstorage-file`
  时它是 `undefined` 且会盖掉 jsdom 那份；`helpers/api.js` 在模块顶层就读它。
- **canvas getContext** —— jsdom 恒返回 `null`，而 Semi 依赖的 lottie-web 在模块
  顶层就 `getContext('2d').fillStyle = ...`。
- **matchMedia / ResizeObserver / IntersectionObserver** —— Semi 的 Table、Select、
  响应式栅格挂载时直接调用。

## 端到端

需要一个**已经在跑的后端**和一组管理员账号：

```bash
E2E_ADMIN_USER=your_admin E2E_ADMIN_PASS=your_pass bun run e2e
```

没设这两个环境变量时用例整体跳过（不是失败），方便在没有后端的环境下跑其余检查。

其他可选变量：

| 变量           | 默认                    | 说明                                  |
| -------------- | ----------------------- | ------------------------------------- |
| `E2E_BACKEND`  | `http://localhost:3000` | 后端地址                              |
| `E2E_BASE_URL` | 自动拉起 `bun run dev`  | 已有前端时指过去，跳过启动 dev server |

```bash
bun run e2e:ui        # 交互式调试
bun run e2e:report    # 看上次的报告（失败时带 trace / 截图 / 录像）
```

**这些用例只读不写。** 它们不去真的建分组、保存配置——跑 E2E 的很可能就是本人的
开发库，一个手滑就把线上分组配置改了。要验「建分组 → 挂渠道 → 配折扣」这条完整
主线，目前仍需人工走一遍（见 `docs/group-management-redesign.md` §7.0）。

定位元素优先用 `data-testid`，不要用 `.semi-*` 类名或 placeholder 文本——
组件库的内部 DOM 不是公开契约，升级一次选择器就碎。
