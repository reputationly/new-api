import React from 'react';
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';

vi.mock('../../../helpers', async () => {
  const actual = await vi.importActual('../../../helpers');
  return { ...actual, API: { get: vi.fn() }, showError: vi.fn() };
});

import { API } from '../../../helpers';
import { UserContext } from '../../../context/User';
import { StatusContext } from '../../../context/Status';
import { useModelPricingData } from '../useModelPricingData';

// 「仅看套餐可用」开着的时候覆盖数据变成 null（套餐到期后刷新）：开关此时是隐藏的，
// 筛选若继续生效就会留下一个空列表且无处可关。

const pricingResponse = (coverage) => ({
  data: {
    success: true,
    data: [
      {
        model_name: 'covered',
        quota_type: 0,
        model_ratio: 1,
        enable_groups: ['default'],
      },
      {
        model_name: 'other',
        quota_type: 0,
        model_ratio: 1,
        enable_groups: ['default'],
      },
    ],
    group_ratio: { default: 1 },
    usable_group: { default: 'default' },
    entitlement_coverage: coverage,
    quota_per_compute_point: 100,
  },
});

const wrapper = ({ children }) => (
  <StatusContext.Provider value={[{}, () => {}]}>
    <UserContext.Provider value={[{}, () => {}]}>
      {children}
    </UserContext.Provider>
  </StatusContext.Provider>
);

const names = (result) =>
  result.current.filteredModels.map((m) => m.model_name).sort();

beforeEach(() => {
  API.get.mockReset();
});

describe('仅看套餐可用', () => {
  it('有覆盖数据时只留被覆盖的模型', async () => {
    API.get.mockResolvedValue(
      pricingResponse({ covered: { plan_title: 'P' } }),
    );
    const { result } = renderHook(() => useModelPricingData(), { wrapper });
    await waitFor(() => expect(result.current.loading).toBe(false));
    act(() => result.current.setFilterEntitlementOnly(true));
    expect(names(result)).toEqual(['covered']);
  });

  it('覆盖数据变成 null 后筛选不生效，列表恢复全部', async () => {
    API.get.mockResolvedValue(
      pricingResponse({ covered: { plan_title: 'P' } }),
    );
    const { result } = renderHook(() => useModelPricingData(), { wrapper });
    await waitFor(() => expect(result.current.loading).toBe(false));
    act(() => result.current.setFilterEntitlementOnly(true));

    API.get.mockResolvedValue(pricingResponse(null));
    await act(async () => {
      await result.current.refresh();
    });
    expect(result.current.entitlementConfig.coverage).toBeNull();
    expect(names(result)).toEqual(['covered', 'other']);
  });
});
