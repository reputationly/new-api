import React from 'react';
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';

vi.mock('../../../helpers', async () => {
  const actual = await vi.importActual('../../../helpers');
  return {
    ...actual,
    API: { get: vi.fn() },
  };
});

import { API } from '../../../helpers';
import PlanComparisonTable from '../PlanComparisonTable';

// 对比表的**接线**：行构造函数全绿不代表购买页上真能看到——组件忘了请求、
// 或者拿错了响应字段，测试照样全绿而表一行不出。

const t = (s) => s;
const plans = [{ plan: { id: 1, title: '专业版' } }];

beforeEach(() => {
  localStorage.setItem('quota_per_compute_point', '100');
  localStorage.setItem('quota_per_unit', '500000');
  API.get.mockReset();
});

describe('PlanComparisonTable', () => {
  it('拿到数据后按套餐名出列、按换算值出格', async () => {
    API.get.mockResolvedValue({
      data: {
        success: true,
        data: {
          models: [{ model_name: 'img-hd', quota_type: 1, model_price: 0.04 }],
          plans: [
            {
              plan_id: 1,
              compute_points_per_period: 50000 * 100,
              coverage: {
                'img-hd': {
                  consume_points: true,
                  discount: 1,
                  channel_limited: false,
                },
              },
              limits: [],
            },
          ],
        },
      },
    });
    render(<PlanComparisonTable plans={plans} t={t} />);
    expect(API.get).toHaveBeenCalledWith('/api/subscription/plans/comparison');
    await waitFor(() => expect(screen.getByText('≈ 250 次')).toBeTruthy());
    expect(screen.getByText('专业版')).toBeTruthy();
    expect(screen.getByText('50,000')).toBeTruthy();
  });

  // 没有算力点套餐时后端回 null：老式套餐的购买页必须与之前完全一致
  it('后端返回 null 时整个组件不渲染', async () => {
    API.get.mockResolvedValue({ data: { success: true, data: null } });
    const { container } = render(<PlanComparisonTable plans={plans} t={t} />);
    await waitFor(() => expect(API.get).toHaveBeenCalled());
    expect(container.textContent).toBe('');
  });

  // 对比表是锦上添花，请求失败不能把购买区一起带崩
  it('请求失败时静默不出表', async () => {
    API.get.mockRejectedValue(new Error('boom'));
    const { container } = render(<PlanComparisonTable plans={plans} t={t} />);
    await waitFor(() => expect(API.get).toHaveBeenCalled());
    expect(container.textContent).toBe('');
  });
});
