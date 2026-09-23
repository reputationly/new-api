import React from 'react';
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';

vi.mock('../../../../helpers', async () => {
  const actual = await vi.importActual('../../../../helpers');
  return { ...actual, API: { get: vi.fn() }, showError: vi.fn() };
});

import { API } from '../../../../helpers';
import golden from './fixtures/planFulfillmentReport.json';
import PlanFulfillmentPanel, {
  formatRate,
  isLossRate,
} from '../PlanFulfillmentPanel';

// 套餐经营面板的接线：请求对了接口、按后端字段渲染。字段名与 service.PlanFulfillmentRow
// 的 json tag 一一对应——这里写错了就是一整列「¥0.00」，没有任何报错。

const row = (over = {}) => ({
  plan_id: 1,
  plan_title: '专业版',
  order_count: 3,
  revenue_fen: 29900,
  call_count: 120,
  value_fen: 50000,
  cost_fen: 12000,
  uncosted_call_count: 0,
  uncosted_value_fen: 0,
  overage_count: 0,
  overage_fen: 0,
  points_expired_granted: 0,
  points_expired_unused: 0,
  expired_unused_ratio: null,
  fulfillment_rate: 12000 / 29900,
  ...over,
});

beforeEach(() => {
  API.get.mockReset();
});

describe('formatRate / isLossRate', () => {
  it('没有收入时显示 —', () => {
    expect(formatRate(null)).toBe('—');
  });
  it('成本 ≥ 收入即亏损', () => {
    expect(isLossRate(1)).toBe(true);
    expect(isLossRate(0.99)).toBe(false);
    expect(isLossRate(null)).toBe(false);
  });
});

describe('PlanFulfillmentPanel', () => {
  it('按后端字段渲染每个套餐，亏损的标红', async () => {
    API.get.mockResolvedValue({
      data: {
        success: true,
        data: {
          rows: [
            row(),
            row({
              plan_id: 2,
              plan_title: '外采包',
              revenue_fen: 10000,
              cost_fen: 15000,
              fulfillment_rate: 1.5,
              uncosted_call_count: 7,
              uncosted_value_fen: 700,
            }),
          ],
          total: row({
            revenue_fen: 39900,
            cost_fen: 27000,
            fulfillment_rate: 27000 / 39900,
          }),
        },
      },
    });
    render(<PlanFulfillmentPanel />);
    expect(API.get.mock.calls[0][0]).toMatch(
      /^\/api\/reconcile\/admin\/plan\/fulfillment\?start=\d+&end=\d+$/,
    );
    await waitFor(() => expect(screen.getByText('外采包')).toBeTruthy());
    expect(screen.getByText('¥299.00')).toBeTruthy();
    expect(screen.getByText('40.1%')).toBeTruthy();
    expect(screen.getByText('150.0% 亏损')).toBeTruthy();
    expect(screen.getByText(/7 次 · ¥7.00/)).toBeTruthy();
    // 总览
    expect(screen.getByText('¥399.00')).toBeTruthy();
    expect(screen.getByText('67.7%')).toBeTruthy();
  });

  // 后端真实转换函数产出的报表（service 的 golden 测试守着它）：字段名对不上的话
  // 这里会出现一整列 ¥0.00
  it('能渲染后端产出的报表', async () => {
    API.get.mockResolvedValue({ data: { success: true, data: golden } });
    render(<PlanFulfillmentPanel />);
    await waitFor(() => expect(screen.getAllByText('¥299.00').length).toBe(2));
    expect(screen.getAllByText('¥3.65').length).toBe(2); // 外采成本：行 + 总览
    expect(screen.getByText(/7 次 · ¥0.73/)).toBeTruthy();
    expect(screen.getByText(/2 次 · ¥1.46/)).toBeTruthy();
    expect(screen.getAllByText('60.0%').length).toBeGreaterThan(0);
  });

  // 管理员开通的套餐没有收入：成本照列，履约率不能是 Infinity / NaN
  it('没有收入的套餐履约率显示 —', async () => {
    API.get.mockResolvedValue({
      data: {
        success: true,
        data: {
          rows: [
            row({ revenue_fen: 0, order_count: 0, fulfillment_rate: null }),
          ],
          total: row({ fulfillment_rate: null }),
        },
      },
    });
    const { container } = render(<PlanFulfillmentPanel />);
    await waitFor(() => expect(screen.getByText('专业版')).toBeTruthy());
    expect(container.textContent).not.toMatch(/Infinity|NaN/);
  });
});
