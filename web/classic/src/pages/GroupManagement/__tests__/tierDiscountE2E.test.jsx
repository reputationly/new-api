import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

import GroupManagement from '../index';
import { API } from '../../../helpers';

/**
 * 「档位折扣」的端到端：进页面 → 看到已配的档 → 增删规则 → 保存 →
 * 断言真正 PUT 出去的 payload。
 *
 * 拆开测各自都绿、串起来处处是坑——一天之内撞上四个：默认不选中档位、下拉框
 * 「暂无数据」、新增行落到第二页、空白 tag 让模型对所有人隐藏。四个都发生在
 * 「上一步的输出交给下一步」的接缝上，没有一个在函数内部。
 *
 * 所以断言一律落在最终的 PUT payload 上：那是唯一能证明「运营在界面上做的事真的
 * 存进去了」的东西。界面显示对了但存错了，是这一天里代价最高的一类缺陷。
 *
 * 覆盖不到的：换档位。Semi Select 的下拉选项在 jsdom 里不响应合成点击事件
 * （userEvent / fireEvent / pointerEventsCheck 关闭都试过），换档只能靠手工验。
 * 下拉框「有没有选项可选」由 tierDefaultSelection.test.jsx 守着。
 */

vi.mock('../../../helpers', async () => {
  const actual = await vi.importActual('../../../helpers');
  return {
    ...actual,
    API: { get: vi.fn(), post: vi.fn(), put: vi.fn() },
    showError: vi.fn(),
    showSuccess: vi.fn(),
    showWarning: vi.fn(),
  };
});

const GROUP_RATIO = JSON.stringify({ default: 1, geostar: 0.9, premium: 1 });

/**
 * 两个档，验证编辑其中一个不会波及另一个。
 *
 * batch2026q3 刻意配到 12 条**超过一页**（每页 10 条）——现网那个档要排除 15 个
 * 零毛利模型，本来就是这个量级。数据量不足一页时，「新行追加到末尾」和「插到最前」
 * 表现完全一样，这条路径就白测了。
 */
const TIER_CONFIG = JSON.stringify({
  batch2026q3: {
    '*': { mode: 'multiply', value: 0.9 },
    'GLM-5.3': { mode: 'multiply', value: 1 },
    'GLM-5': { mode: 'multiply', value: 1 },
    'GLM-5-Turbo': { mode: 'multiply', value: 1 },
    'DeepSeek-R1': { mode: 'multiply', value: 1 },
    'Kimi-K2.6': { mode: 'multiply', value: 1 },
    'MiniMax-M2.7': { mode: 'multiply', value: 1 },
    'MiniMax-M3': { mode: 'multiply', value: 1 },
    'Qwen3.7-Max': { mode: 'multiply', value: 1 },
    'Qwen3.7-Plus': { mode: 'multiply', value: 1 },
    'Qwen3.8-Flash': { mode: 'multiply', value: 1 },
    'Qwen3.8-Max': { mode: 'multiply', value: 1 },
  },
  vip2025: {
    '*': { mode: 'multiply', value: 0.8 },
  },
});

function mockAPIs(tierConfig = TIER_CONFIG) {
  API.get.mockImplementation((url) => {
    if (url.startsWith('/api/option/')) {
      return Promise.resolve({
        data: {
          success: true,
          data: [
            { key: 'GroupRatio', value: GROUP_RATIO },
            { key: 'UserGroupModelRatio', value: tierConfig },
          ],
        },
      });
    }
    if (url.startsWith('/api/group/overview')) {
      return Promise.resolve({
        data: { success: true, data: { groups: [], unconfigured: [] } },
      });
    }
    if (url.startsWith('/api/group/models')) {
      return Promise.resolve({
        data: {
          success: true,
          data: ['GLM-5.3', 'GLM-5.2', 'Kimi-K3', 'MiniMax-M3'],
        },
      });
    }
    return Promise.resolve({ data: { success: true, data: [] } });
  });
  API.put.mockResolvedValue({ data: { success: true } });
}

/**
 * 页面上同时存在「模型折扣」和「档位折扣」两套一模一样的控件——Semi Tabs 把未激活的
 * TabPane 也渲染进 DOM。全局查询会拿到前者，测的就不是这个功能了。
 *
 * 这不是洁癖：本文件的前身连着两版变异测试都是绿的，就因为断言查到了另一个 Tab。
 */
function tierPane() {
  return screen.getByText('配置哪个用户档').closest('.semi-tabs-pane');
}

/** 模型名那列的 Input 是表里唯一没有 placeholder 的（备注列有「为什么是这个价」） */
function patternInputs() {
  return within(tierPane())
    .getAllByRole('textbox')
    .filter((el) => !el.placeholder);
}

async function openTierTab(user) {
  render(
    <MemoryRouter>
      <GroupManagement />
    </MemoryRouter>,
  );
  await waitFor(() => expect(API.get).toHaveBeenCalled());
  await user.click(await screen.findByText('档位折扣'));
  await screen.findByText('batch2026q3');
}

/** 删除按钮包在 Popconfirm 里，点按钮只弹确认框，要再点「确定」才真的删 */
async function deleteFirstRule(user) {
  const trash = Array.from(tierPane().querySelectorAll('button')).filter((b) =>
    b.className.includes('danger'),
  );
  if (!trash.length) return false;
  await user.click(trash[0]);
  const ok = await screen.findByText('确定');
  await user.click(ok);
  return true;
}

function savedTierConfig() {
  const call = API.put.mock.calls.find(
    (c) => c[1]?.key === 'UserGroupModelRatio',
  );
  expect(call, '保存请求里没有 UserGroupModelRatio').toBeTruthy();
  return JSON.parse(call[1].value);
}

describe('档位折扣 · 端到端', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockAPIs();
  });

  it('加一条规则并保存：新规则进去了，老规则和别的档都没被动', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    await openTierTab(user);

    // ① 自动选中已配档位，规则渲染出来
    await waitFor(() => {
      expect(
        within(tierPane()).getByDisplayValue('GLM-5.3'),
      ).toBeInTheDocument();
    });

    // ② 加一条规则。新行必须落在当前页——追加到末尾时会掉到第二页而看不见
    await user.click(within(tierPane()).getByText('自定义/通配规则'));
    const empty = patternInputs().filter((el) => el.value === '');
    expect(empty.length, '新增的空规则行没有出现在当前页').toBe(1);

    await user.type(empty[0], 'Kimi-K3');
    await user.click(screen.getByText('保存'));

    // ③ 落到 payload 上验
    await waitFor(() => expect(API.put).toHaveBeenCalled());
    const saved = savedTierConfig();

    expect(saved.batch2026q3).toHaveProperty('Kimi-K3');
    expect(saved.batch2026q3['*'].value, '兜底规则不能丢').toBe(0.9);
    expect(saved.batch2026q3['GLM-5.3'].value, '既有例外不能丢').toBe(1);
    expect(saved.vip2025, '编辑一个档不能把另一个档冲掉').toEqual({
      '*': { mode: 'multiply', value: 0.8 },
    });
  });

  it('改倍率并保存：存进去的是改后的值', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    await openTierTab(user);
    await waitFor(() => {
      expect(
        within(tierPane()).getByDisplayValue('GLM-5.3'),
      ).toBeInTheDocument();
    });

    // 把兜底折扣从 0.9 改成 0.85
    const numberInputs = within(tierPane())
      .getAllByRole('spinbutton')
      .filter((el) => el.value === '0.9');
    expect(numberInputs.length, '找不到值为 0.9 的倍率输入框').toBe(1);
    await user.clear(numberInputs[0]);
    await user.type(numberInputs[0], '0.85');

    await user.click(screen.getByText('保存'));
    await waitFor(() => expect(API.put).toHaveBeenCalled());

    expect(savedTierConfig().batch2026q3['*'].value).toBe(0.85);
  });

  it('删光某档的规则后保存，该档整体消失而不是留个空壳', async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    await openTierTab(user);
    await waitFor(() => {
      expect(
        within(tierPane()).getByDisplayValue('GLM-5.3'),
      ).toBeInTheDocument();
    });

    for (let i = 0; i < 20; i += 1) {
      // eslint-disable-next-line no-await-in-loop
      if (!(await deleteFirstRule(user))) break;
    }
    expect(patternInputs().length, '规则没被删干净').toBe(0);

    await user.click(screen.getByText('保存'));
    await waitFor(() => expect(API.put).toHaveBeenCalled());

    const saved = savedTierConfig();
    expect(
      saved.batch2026q3,
      '规则删光后该档应整体移除；留一个空对象会让它一直出现在下拉里，且下次打开又被自动选中',
    ).toBeUndefined();
    expect(saved.vip2025, '删一个档不能顺手删掉另一个').toBeTruthy();
  });
});
