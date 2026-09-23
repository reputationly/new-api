import { test, expect } from '@playwright/test';

/**
 * 资金对账 + 套餐与算力点的界面端到端用例。方案见
 * docs/e2e-test-plan-fund-subscription.md §3 F20、§4.4、§4.5。
 *
 * 依赖 scripts/e2e/run_e2e.py 跑完之后的隔离环境（:3300，全新 SQLite，账号与数据由
 * 脚本建好）。会写一次「对比表展示模型」配置，所以**只在显式设置 E2E_FUND_SUB=1 时运行**，
 * 免得有人对着自己的开发库跑。
 *
 *   E2E_FUND_SUB=1 E2E_BASE_URL=http://localhost:3300 bunx playwright test e2e/fund-subscription.spec.js
 */

const SHOTS = '/tmp/e2e/screenshots';
const enabled = process.env.E2E_FUND_SUB === '1';

test.skip(!enabled, '未设置 E2E_FUND_SUB=1，跳过资金 / 套餐端到端界面用例');

const PASSWORDS = { root: 'rootpass123' };
const passwordOf = (u) => PASSWORDS[u] || 'pass12345';

// 走接口登录再注入 localStorage：登录页不是这里要验的东西（同 e2e/fixtures.js 的做法）
async function loginAs(page, username) {
  const res = await page.context().request.post('/api/user/login', {
    data: { username, password: passwordOf(username) },
  });
  const body = await res.json();
  expect(body.success, `登录 ${username} 失败：${body.message}`).toBeTruthy();
  await page.goto('/');
  await page.evaluate((user) => {
    localStorage.setItem('user', JSON.stringify(user));
  }, body.data);
  return body.data;
}

// 截图前等过渡动画与数据加载结束：否则拍到的是骨架屏 / 半透明的加载态，断言虽然过了，
// 截图却不能当证据。不用 networkidle：页面有轮询（通知等），永远等不到空闲。
async function shot(page, name) {
  await page.waitForTimeout(1500);
  await page.screenshot({ path: `${SHOTS}/${name}.png`, fullPage: true });
}

// 页面上出现 React 错误边界 / 运行时异常就算失败——详情页与卡片视图曾因一个未定义
// 变量整页崩溃，构建与单测都没拦住
function watchErrors(page) {
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message));
  return errors;
}

test.describe.configure({ mode: 'serial' });

test('F20 对账管理：收入对账', async ({ page }) => {
  const errors = watchErrors(page);
  await loginAs(page, 'root');
  await page.goto('/console/reconcile');
  await expect(page.getByText('真实入账').first()).toBeVisible();
  await shot(page, 'F20-revenue');
  expect(errors).toEqual([]);
});

test('S30 对账管理：套餐经营', async ({ page }) => {
  const errors = watchErrors(page);
  await loginAs(page, 'root');
  await page.goto('/console/reconcile');
  await page.getByText('套餐经营').click();
  await expect(page.getByText('套餐经营总览')).toBeVisible();
  await expect(
    page.locator('.semi-table-tbody').getByText('专业版'),
  ).toBeVisible();
  await shot(page, 'S30-fulfillment');
  expect(errors).toEqual([]);
});

test('S20 模型广场：卡片视图（默认）、表格视图、详情页', async ({ page }) => {
  const errors = watchErrors(page);
  await loginAs(page, 'alice');
  await page.goto('/pricing');
  // 默认是卡片视图：套餐名角标 + 套餐内价
  await expect(page.getByText('e2e-image').first()).toBeVisible();
  await expect(page.getByText('专业版').first()).toBeVisible();
  await expect(page.getByText(/套餐内/).first()).toBeVisible();
  await shot(page, 'S20-card');

  await page.getByRole('button', { name: '表格视图' }).click();
  await expect(page.getByText('3 次/月').first()).toBeVisible();
  await expect(page.getByText('限特定渠道').first()).toBeVisible();
  await shot(page, 'S20-table');

  // 桌面端「仅看套餐可用」开关可见且生效：打开后套餐外的 e2e-outside 消失
  const toggle = page.getByText('仅看套餐可用');
  await expect(toggle).toBeVisible();
  await toggle.locator('xpath=..').getByRole('switch').click();
  await expect(page.getByText('e2e-outside')).toHaveCount(0);
  await expect(page.getByText('e2e-chat').first()).toBeVisible();
  await shot(page, 'S20-filter');

  // 详情页（曾经整页崩溃）
  await page.getByText('e2e-image').first().click();
  await expect(
    page
      .locator('.semi-sidesheet')
      .getByText(/套餐内/)
      .first(),
  ).toBeVisible();
  await shot(page, 'S20-detail');
  expect(errors).toEqual([]);
});

test('S20 模型广场：无套餐用户看不到任何套餐元素', async ({ page }) => {
  const errors = watchErrors(page);
  await loginAs(page, 'carol');
  await page.goto('/pricing');
  await expect(page.getByText('e2e-image').first()).toBeVisible();
  await expect(page.getByText(/套餐内/)).toHaveCount(0);
  await expect(page.getByText('仅看套餐可用')).toHaveCount(0);
  expect(errors).toEqual([]);
});

test('S21-S23 购买页：我的订阅、购买卡片、对比表', async ({ page }) => {
  const errors = watchErrors(page);
  await loginAs(page, 'alice');
  await page.goto('/console/topup');
  // 我的订阅：余量进度条与低余量提示（e2e-image 3/3 次已用尽）
  await expect(page.getByText('算力点').first()).toBeVisible();
  await expect(
    page.getByText(/e2e-image 剩余 0 次，用尽后将按账户余额计费/),
  ).toBeVisible();
  // 购买卡片：每期算力点与「超出按余额计费」，新式套餐不写「总额度：不限」
  await expect(page.getByText('每期算力点: 1,000').first()).toBeVisible();
  await expect(
    page.getByText('超出套餐的部分按账户余额计费').first(),
  ).toBeVisible();
  // 对比表
  await expect(page.getByText('套餐对比')).toBeVisible();
  await expect(page.getByText('≈ 10 次').first()).toBeVisible();
  await shot(page, 'S21-topup');
  expect(errors).toEqual([]);
});

test('S24 使用日志：套餐标签、超额标签、计费来源筛选', async ({ page }) => {
  const errors = watchErrors(page);
  await loginAs(page, 'alice');
  await page.goto('/console/log');
  await expect(page.getByText('专业版 150点').first()).toBeVisible();
  await expect(page.getByText('超额').first()).toBeVisible();
  await shot(page, 'S24-logs');
  expect(errors).toEqual([]);
});

test('S26 管理端：套餐列表与展示模型弹窗', async ({ page }) => {
  const errors = watchErrors(page);
  await loginAs(page, 'root');
  await page.goto('/console/subscription');
  const row = page.getByRole('row', { name: /专业版/ });
  await expect(row.getByText('无')).toBeVisible();
  await page.getByRole('button', { name: '对比表展示模型' }).click();
  await expect(page.getByText('e2e-image').first()).toBeVisible();
  await shot(page, 'S26-admin');
  expect(errors).toEqual([]);
});

test('S25 mobile：我的套餐卡片、日志标记、模型角标', async ({ page }) => {
  const errors = watchErrors(page);
  await page.setViewportSize({ width: 390, height: 844 });
  await loginAs(page, 'alice');
  await page.goto('/m/profile');
  await expect(page.getByText('我的套餐')).toBeVisible();
  await expect(
    page.getByText('超出套餐的部分按账户余额计费').first(),
  ).toBeVisible();
  await shot(page, 'S25-m-profile');
  await page.goto('/m/logs');
  await expect(page.getByText('专业版 150点').first()).toBeVisible();
  await shot(page, 'S25-m-logs');
  await page.goto('/m/models');
  await expect(page.getByText('专业版').first()).toBeVisible();
  await shot(page, 'S25-m-models');
  expect(errors).toEqual([]);
});

test('C5 管理端：资金操作弹窗调整授信额度', async ({ page }) => {
  const errors = watchErrors(page);
  const admin = await loginAs(page, 'root');
  await page.goto('/console/user');
  const row = page.getByRole('row', { name: /gina/ });
  await row.getByRole('button', { name: '编辑' }).click();
  await page.getByRole('button', { name: '入账 / 赠送 / 授信 / 回款' }).click();
  const modal = page.locator('.semi-modal').filter({ hasText: '资金操作' });
  // 当前信用：C1 之后欠款 10000 quota（¥0.15）
  await expect(modal.getByText('信用').first()).toBeVisible();
  await shot(page, 'C5-fund-modal');

  await modal.getByText('调整授信额度').click();
  await modal.getByLabel('授信上限（元）').fill('200');
  await modal.getByLabel('授信协议号').fill('C-ui-1');
  await modal.getByRole('button', { name: '确认' }).click();
  await expect(page.getByText(/成功/).first()).toBeVisible();

  // 以接口为准核对：¥200 ÷ 7.3 × 500000 ≈ 13698630 quota
  const res = await page.context().request.get('/api/user/?p=1&page_size=100', {
    headers: { 'New-Api-User': String(admin.id) },
  });
  const users = (await res.json()).data.items;
  const gina = users.find((u) => u.username === 'gina');
  expect(gina.credit_limit).toBeGreaterThan(13_600_000);
  expect(gina.credit_limit).toBeLessThan(13_800_000);
  await shot(page, 'C5-after');
  expect(errors).toEqual([]);
});

test('C7 用户侧授信展示：钱包页、超限提示、mobile、管理端', async ({
  page,
}) => {
  const errors = watchErrors(page);
  // 正常：信息条给出额度、已用、可用
  await loginAs(page, 'gina');
  await page.goto('/console/topup');
  await expect(page.getByText(/信用额度 .* · 已用 .* · 可用/)).toBeVisible();
  await shot(page, 'C7-gina-wallet');

  // 超限：警告条说明超出多少、新请求会被拒
  await loginAs(page, 'hank');
  await page.goto('/console/topup');
  await expect(page.getByText(/已超出 .*新的请求将被拒绝/)).toBeVisible();
  await shot(page, 'C7-hank-wallet');

  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/m/profile');
  await expect(page.getByText(/信用额度 .* · 已用 .*已超出/)).toBeVisible();
  await shot(page, 'C7-hank-mobile');
  await page.setViewportSize({ width: 1280, height: 720 });

  // 没开授信的用户什么都不出
  await loginAs(page, 'alice');
  await page.goto('/console/topup');
  await expect(page.getByText(/信用额度/)).toHaveCount(0);

  // 管理端弹窗：超限要标出来
  await loginAs(page, 'root');
  await page.goto('/console/user');
  await page
    .getByRole('row', { name: /hank/ })
    .getByRole('button', { name: '编辑' })
    .click();
  await page.getByRole('button', { name: '入账 / 赠送 / 授信 / 回款' }).click();
  await expect(page.getByText(/已超出 ¥.*新请求会被拒绝/)).toBeVisible();
  await shot(page, 'C7-admin-over');
  expect(errors).toEqual([]);
});

test('P1 普通管理员：用户管理可见，资金操作可用', async ({ page }) => {
  const errors = watchErrors(page);
  await loginAs(page, 'mgr');
  await page.goto('/console/user');
  await expect(page).not.toHaveURL(/forbidden/);
  const row = page.getByRole('row', { name: /dave/ });
  await row.getByRole('button', { name: '编辑' }).click();
  await page.getByRole('button', { name: '入账 / 赠送 / 授信 / 回款' }).click();
  await expect(
    page.locator('.semi-modal').filter({ hasText: '资金操作' }),
  ).toBeVisible();
  await shot(page, 'P1-admin-fund');
  expect(errors).toEqual([]);
});

test('P3 套餐价格按实付人民币展示，不乘汇率', async ({ page }) => {
  const errors = watchErrors(page);
  // 专业版 price_amount = 29.9（元）。乘 7.3 会显示成 218.27
  await loginAs(page, 'alice');
  await page.goto('/console/topup');
  await expect(page.getByText('29.90').first()).toBeVisible();
  await expect(page.getByText(/218\.27/)).toHaveCount(0);
  await shot(page, 'P3-topup-price');

  await loginAs(page, 'root');
  await page.goto('/console/subscription');
  await expect(page.getByText('¥29.90').first()).toBeVisible();
  await expect(page.getByText(/218\.27/)).toHaveCount(0);
  await shot(page, 'P3-admin-price');
  expect(errors).toEqual([]);
});
