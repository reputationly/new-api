import { test, expect } from './fixtures';

/**
 * 分组管理页主线，对应 docs/group-management-redesign.md §7.0。
 *
 * 这些用例回答的是组件测试回答不了的问题：页面能不能打开、四个接口通不通、
 * Tab 之间跳转到不到位。它们**只读不写**——不去真的建分组保存配置，
 * 因为跑 E2E 的很可能就是本人的开发库，一个手滑就把线上分组配置改了。
 */

test.describe('分组管理页', () => {
  test.beforeEach(async ({ adminPage }) => {
    await adminPage.goto('/console/group');
  });

  test('页面打开且五个 Tab 都在', async ({ adminPage }) => {
    await expect(
      adminPage.getByRole('heading', { name: '分组管理' }),
    ).toBeVisible();

    for (const tab of [
      '分组',
      '模型折扣',
      '自动分组',
      '跨分组规则',
      '充值 · 限流 · 积分',
    ]) {
      await expect(adminPage.getByRole('tab', { name: tab })).toBeVisible();
    }
  });

  test('分组表加载出健康状态与覆盖数', async ({ adminPage }) => {
    // /api/group/overview 有没有真的返回并渲染
    const table = adminPage.locator('table').first();
    await expect(table).toBeVisible();
    await expect(
      adminPage.getByRole('columnheader', { name: '状态' }),
    ).toBeVisible();
    await expect(
      adminPage.getByRole('columnheader', { name: '渠道/模型' }),
    ).toBeVisible();

    // 至少有一行分组，且状态列不是空的（渲染成了 Tag 而不是崩在 undefined 上）
    const firstStatus = adminPage
      .locator('tbody tr')
      .first()
      .locator('td')
      .nth(1);
    await expect(firstStatus).not.toBeEmpty();
  });

  test('模型折扣 Tab 能选分组并拉到该分组的模型', async ({ adminPage }) => {
    const modelsCall = adminPage.waitForResponse(
      (r) => r.url().includes('/api/group/models') && r.status() === 200,
    );
    await adminPage.getByRole('tab', { name: '模型折扣' }).click();
    const res = await modelsCall;
    expect((await res.json()).success).toBe(true);

    await expect(adminPage.getByText('配置哪个分组')).toBeVisible();
  });

  test('倍率试算器返回完整解析链', async ({ adminPage }) => {
    await expect(adminPage.getByText('倍率试算器')).toBeVisible();

    // 用 data-testid 定位而不是 .semi-* 类名或 placeholder 文本：
    // 组件库的内部 DOM 不是公开契约，一升级选择器就碎
    await adminPage.getByTestId('sim-using-group').click();
    const firstOption = adminPage
      .locator('[role="listbox"] [role="option"]')
      .first();
    await expect(firstOption).toBeVisible();
    await firstOption.click();

    const resolveCall = adminPage.waitForResponse(
      (r) => r.url().includes('/api/group/resolve') && r.status() === 200,
    );
    await adminPage.getByTestId('sim-run').click();
    const body = await (await resolveCall).json();

    expect(body.success).toBe(true);
    // 解析链的四个关键字段必须都在，前端据此逐层展示
    expect(body.data).toHaveProperty('final');
    expect(body.data).toHaveProperty('group_ratio');
    expect(body.data).toHaveProperty('base');
    expect(body.data).toHaveProperty('rule_match');

    await expect(adminPage.getByTestId('sim-result')).toBeVisible();
    await expect(adminPage.getByText('最终倍率')).toBeVisible();
  });

  test('旧入口留下了指向分组管理的跳转', async ({ adminPage }) => {
    await adminPage.goto('/console/setting?tab=ratio');
    await expect(
      adminPage.getByRole('button', { name: '前往分组管理' }).first(),
    ).toBeVisible();
  });
});
