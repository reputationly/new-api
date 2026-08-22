import { test as base, expect } from '@playwright/test';

const ADMIN_USER = process.env.E2E_ADMIN_USER;
const ADMIN_PASS = process.env.E2E_ADMIN_PASS;

export const hasCredentials = Boolean(ADMIN_USER && ADMIN_PASS);

/**
 * 以管理员身份登录并注入会话。
 *
 * 走接口登录而不是填表单：登录页本身不是这些用例要验的东西，走 UI 只会让
 * 每个用例都因为验证码/OAuth 开关之类的无关变更而挂掉。
 *
 * 登录态同时需要 cookie（后端 session）与 localStorage 里的 user
 * （前端 AdminRoute 据此判断是否放行），两者缺一进不去后台。
 */
export const test = base.extend({
  adminPage: async ({ page, context, baseURL }, use) => {
    const res = await context.request.post('/api/user/login', {
      data: { username: ADMIN_USER, password: ADMIN_PASS },
    });
    const body = await res.json();
    if (!body?.success) {
      throw new Error(
        `管理员登录失败：${body?.message || res.status()}。` +
          `请确认 E2E_ADMIN_USER / E2E_ADMIN_PASS 与后端账号一致。`,
      );
    }
    if (body.data?.role < 10) {
      throw new Error('该账号不是管理员，分组管理页会被 AdminRoute 拦下');
    }

    await page.goto('/');
    await page.evaluate((user) => {
      localStorage.setItem('user', JSON.stringify(user));
    }, body.data);

    await use(page);
  },
});

export { expect };

/** 没配账号时整体跳过，而不是让一堆连接错误淹没输出 */
test.beforeEach(() => {
  test.skip(
    !hasCredentials,
    '未设置 E2E_ADMIN_USER / E2E_ADMIN_PASS，跳过端到端用例',
  );
});
