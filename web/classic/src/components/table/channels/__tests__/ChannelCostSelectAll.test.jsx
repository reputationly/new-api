import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import ChannelCostModal from '../modals/ChannelCostModal';
import { API } from '../../../../helpers';

/**
 * 成本录入的全选。
 *
 * 没有它时，渠道挂几十个模型就要逐个勾选 checkbox 才能用批量设值——而供应商每次
 * 调价、每次挂新模型都要重来一遍。现网并行那个渠道是 25 个模型。
 *
 * 关键行为是「全选作用于当前筛选结果」而不是全部行：只有这样才能
 * 「搜 Qwen → 全选 → 一键 0.7」，一次配完一族模型。
 */

vi.mock('../../../../helpers', async () => {
  const actual = await vi.importActual('../../../../helpers');
  return {
    ...actual,
    API: { get: vi.fn(), put: vi.fn() },
    showError: vi.fn(),
    showSuccess: vi.fn(),
  };
});

const MOUNTED = ['GLM-5.2', 'Qwen3.7-Max', 'Qwen3.8-Max', 'Kimi-K3'];

function mockCostAPI() {
  API.get.mockResolvedValue({
    data: { success: true, data: { mounted_models: MOUNTED, costs: [] } },
  });
}

async function openModal() {
  render(
    <ChannelCostModal
      visible
      channel={{ id: 9, name: '高级模型API' }}
      onClose={() => {}}
    />,
  );
  await waitFor(() => expect(API.get).toHaveBeenCalled());
  // 等表格真的渲染出行来，否则按钮计数还停在 (0)
  await screen.findByText('GLM-5.2');
}

/**
 * 「应用到选中 (N)」里的 N 是唯一能观察到选中数的地方，但它由
 * `{t('应用到选中')} ({selected.length})` 渲染成多个文本节点，
 * findByText 匹配不到整串，只能读按钮的 textContent。
 */
function applyButtonText() {
  const btn = screen
    .getAllByRole('button')
    .find((b) => b.textContent.includes('应用到选中'));
  return btn?.textContent ?? '';
}

describe('成本录入的全选', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockCostAPI();
  });

  it('全选按钮把当前全部行选中，再点一次取消', async () => {
    await openModal();

    const selectAll = await screen.findByText(`全选 (${MOUNTED.length})`);
    await userEvent.click(selectAll);

    await waitFor(() => {
      expect(applyButtonText()).toContain(`(${MOUNTED.length})`);
    });
    expect(await screen.findByText('取消全选')).toBeInTheDocument();

    await userEvent.click(screen.getByText('取消全选'));
    expect(
      await screen.findByText(`全选 (${MOUNTED.length})`),
    ).toBeInTheDocument();
  });

  it('搜索后全选只选中筛选结果，不波及被筛掉的行', async () => {
    await openModal();

    await userEvent.type(screen.getByPlaceholderText('搜索模型'), 'Qwen');

    // 按钮上的计数跟着筛选结果走 —— 若实现是「全选所有行」，这里会是 4
    const selectAll = await screen.findByText('全选 (2)');
    await userEvent.click(selectAll);

    // 清掉搜索后仍只选中那 2 个：全选写进的是模型名，不是「当前可见的下标」
    await userEvent.clear(screen.getByPlaceholderText('搜索模型'));
    await waitFor(() => {
      expect(screen.getByText('全选 (4)')).toBeInTheDocument();
    });
    expect(applyButtonText()).toContain('(2)');
  });
});
