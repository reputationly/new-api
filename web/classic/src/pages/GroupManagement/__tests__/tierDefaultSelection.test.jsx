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
