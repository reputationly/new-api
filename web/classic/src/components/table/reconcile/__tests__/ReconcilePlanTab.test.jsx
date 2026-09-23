import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';

vi.mock('../../../../helpers', async () => {
  const actual = await vi.importActual('../../../../helpers');
  return {
    ...actual,
    API: {
      get: vi.fn().mockResolvedValue({ data: { success: false } }),
      post: vi.fn(),
    },
    showError: vi.fn(),
  };
});

import { API } from '../../../../helpers';
import ReconcilePage from '../../../../pages/Reconcile';

// 面板自己的测试全绿不代表对账页上能点到它：tab 没挂上的话这张报表就不存在。
describe('对账管理：套餐经营 tab', () => {
  it('切到套餐经营后请求履约率报表', async () => {
    render(<ReconcilePage />);
    fireEvent.click(screen.getByText('套餐经营'));
    await waitFor(() =>
      expect(
        API.get.mock.calls.some(([url]) =>
          String(url).startsWith('/api/reconcile/admin/plan/fulfillment'),
        ),
      ).toBe(true),
    );
  });
});
