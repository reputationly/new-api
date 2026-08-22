import { defineConfig, devices } from '@playwright/test';

/**
 * 端到端测试。
 *
 * 组件测试（vitest + jsdom）能覆盖单个组件的行为，但覆盖不了「页面真的能打开、
 * 接口真的通、跳转真的到位」——分组管理页的主线（建分组 → 看健康状态 → 配折扣 →
 * 试算）跨了四个接口和五个 Tab，只有真浏览器能验。
 *
 * 依赖一个**已经在跑的后端**（默认 http://localhost:3000）与一组管理员账号。
 * 没配 E2E_ADMIN_USER / E2E_ADMIN_PASS 时用例会整体跳过，而不是红着——
 * 让没有后端的人 `bun run test` 不至于被一堆连接错误淹没。
 */
const BACKEND = process.env.E2E_BACKEND || 'http://localhost:3000';

export default defineConfig({
  testDir: './e2e',
  // 这些用例会真的写配置，并行跑会互相踩
  workers: 1,
  fullyParallel: false,
  timeout: 30_000,
  expect: { timeout: 10_000 },
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL: process.env.E2E_BASE_URL || 'http://localhost:5173',
    trace: 'retain-on-failure',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
  },
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],
  // 复用已经起着的 dev server；没起就自己拉一个。
  // dev server 的 /api 代理指向 BACKEND（见 vite.config.js）
  webServer: process.env.E2E_BASE_URL
    ? undefined
    : {
        command: 'bun run dev',
        url: 'http://localhost:5173',
        reuseExistingServer: true,
        timeout: 60_000,
        env: { ...process.env, E2E_BACKEND: BACKEND },
      },
});
