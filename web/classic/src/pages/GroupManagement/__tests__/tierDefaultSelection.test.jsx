import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';

import GroupManagement from '../index';
import { API } from '../../../helpers';

/**
 * 「档位折扣」的默认选中项。
 *
 * 缺这个 effect 时的症状极具迷惑性：配好、保存成功、离开页面再回来，下拉框归零、
 * 规则表空白——和「压根没保存上」在视觉上完全一样。实际配置一直躺在 option 里，
 * 只是没人选中那个档。现网遇到过一次，第一反应是重配一遍，而重配一次就多一次
 * 把折扣填错的机会。
 *
 * 断言落在「下拉框显示了档名 + 规则表渲染出了该档的规则」两处，而不是只看下拉框：
 * 只看下拉框的话，把默认值设成 tierNames[0]（会选中 free 这种没配过的分组名）
 * 同样能让下拉框非空，但规则表依然是空的，bug 原样存在。
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

const USER_TIER_CONFIG = JSON.stringify({
  batch2026q3: {
    '*': { mode: 'multiply', value: 0.9 },
    'GLM-5.3': { mode: 'multiply', value: 1 },
  },
});

/**
 * GroupRatio 里刻意放了排序靠前、且**没有**档位折扣配置的分组名。
 * 若实现取的是 tierNames[0]，选中的会是 aaa-free 而不是 batch2026q3。
 */
const GROUP_RATIO = JSON.stringify({
  'aaa-free': 0,
  default: 1,
  premium: 1,
});

function mockAPIs({ userTierConfig = USER_TIER_CONFIG } = {}) {
  API.get.mockImplementation((url) => {
    if (url.startsWith('/api/option/')) {
      return Promise.resolve({
        data: {
          success: true,
          data: [
            { key: 'GroupRatio', value: GROUP_RATIO },
            { key: 'UserGroupModelRatio', value: userTierConfig },
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
        data: { success: true, data: ['GLM-5.3', 'GLM-5.2'] },
      });
    }
    return Promise.resolve({ data: { success: true, data: [] } });
  });
}

async function openTierTab() {
  render(
    <MemoryRouter>
      <GroupManagement />
    </MemoryRouter>,
  );
  await waitFor(() => expect(API.get).toHaveBeenCalled());
  await userEvent.click(await screen.findByText('档位折扣'));
}

describe('档位折扣的默认选中', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('打开页面即选中已配置的档，并渲染出它的规则', async () => {
    mockAPIs();
    await openTierTab();

    expect(await screen.findByText('batch2026q3')).toBeInTheDocument();

    // 关键断言：规则确实渲染出来了。
    // 只看下拉框非空是不够的——取 tierNames[0] 会选中 aaa-free，下拉框同样非空，
    // 但规则表是空的，bug 原样存在。GLM-5.3 只存在于 batch2026q3 的配置里。
    await waitFor(() => {
      expect(screen.getByDisplayValue('GLM-5.3')).toBeInTheDocument();
    });
  });

  it('一个档都没配时保持空白，不硬塞一个分组名进去', async () => {
    mockAPIs({ userTierConfig: '{}' });
    await openTierTab();

    // 这句提示只在编辑器的 group 为空时出现。若实现退化成「总是选 tierNames[0]」，
    // 会选中 aaa-free，提示消失 —— 这条断言就是在守住那个退化。
    expect(
      await screen.findByText('请先选择或输入一个用户档'),
    ).toBeInTheDocument();
  });
});

/**
 * 下拉框展开后必须能看到选项。
 *
 * Semi Select 在 optionList 从空变非空后不更新内部选项，展开永远是「暂无数据」。
 * 这里的时序恰好命中：首次渲染时 inputs 还没加载，tierNames 是空数组，等
 * /api/option/ 回来选项才有值，而那时 Select 已经挂载完了。
 *
 * 后果是运营根本换不了档位——只能靠 allowCreate 把档名重新敲一遍，而敲错一个字
 * 就会创建出一个空档，看起来又像「配置丢了」。
 */
describe('档位下拉的选项', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('数据加载完成后展开，能看到所有档位', async () => {
    mockAPIs();
    await openTierTab();
    await screen.findByText('batch2026q3');

    // 必须精确定位到「档位折扣」那个 Select。页面上同时存在「模型折扣」的分组
    // 下拉（Semi Tabs 会把未激活的 TabPane 也渲染进 DOM），
    // document.querySelector('.semi-select') 拿到的是前者——测的就不是这个功能。
    const tierSelect = screen
      .getByText('配置哪个用户档')
      .closest('.semi-col')
      .querySelector('.semi-select');
    expect(tierSelect).toBeTruthy();
    await userEvent.click(tierSelect);

    // 必须限定在下拉面板内断言。`aaa-free` 在「分组」Tab 的表格里也有，
    // 用 screen.getAllByText 查全文档时，下拉框空不空都能找到它——
    // 去掉 key 的变异下这条依然会绿，是一条测不出任何东西的断言。
    await waitFor(() => {
      const panel = document.querySelector('.semi-select-option-list');
      expect(panel).toBeTruthy();
      expect(panel.textContent).toContain('aaa-free');
      expect(panel.textContent).not.toContain('暂无数据');
    });
  });
});
