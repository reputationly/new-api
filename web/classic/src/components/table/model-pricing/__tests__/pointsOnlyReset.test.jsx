import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

vi.mock('../filter/PricingGroups', () => ({ default: () => null }));
vi.mock('../filter/PricingQuotaTypes', () => ({ default: () => null }));
vi.mock('../filter/PricingEndpointTypes', () => ({ default: () => null }));
vi.mock('../filter/PricingVendors', () => ({ default: () => null }));
vi.mock('../filter/PricingTags', () => ({ default: () => null }));
vi.mock('../../../../hooks/model-pricing/usePricingFilterCounts', () => ({
  usePricingFilterCounts: () => ({}),
}));
// 弹窗里的筛选内容不是本用例的被测对象（重置逻辑在 PricingFilterModal 自己身上），
// 整块换掉，免得为了渲染它去补一堆无关的 props。
vi.mock('../modal/components/FilterModalContent', () => ({
  default: () => null,
}));

import PricingSidebar from '../layout/PricingSidebar';
import PricingFilterModal from '../modal/PricingFilterModal';

// 「重置」必须把「仅看可积分抵扣」一起清掉，**两端都要**。
//
// 桌面端走侧栏、移动端走筛选弹窗，两处各自调用 resetPricingFilters 并手工列出要重置的
// setter —— 漏一个不会报错，只会让用户点完重置发现列表还是被筛过的。这条曾经真漏过：
// 删旧开关时把 setShowRatio 那行删掉了，却没换成新的 setFilterPointsOnly。

describe('重置要清掉「仅看可积分抵扣」', () => {
  it('桌面端侧栏', () => {
    const setFilterPointsOnly = vi.fn();
    render(
      <PricingSidebar
        setFilterPointsOnly={setFilterPointsOnly}
        models={[]}
        allModels={[]}
        t={(s) => s}
      />,
    );
    screen.getByText('重置').closest('button').click();
    expect(setFilterPointsOnly).toHaveBeenCalledWith(false);
  });

  it('移动端筛选弹窗', () => {
    const setFilterPointsOnly = vi.fn();
    render(
      <PricingFilterModal
        visible
        onClose={() => {}}
        sidebarProps={{ setFilterPointsOnly }}
        t={(s) => s}
      />,
    );
    screen.getByText('重置').closest('button').click();
    expect(setFilterPointsOnly).toHaveBeenCalledWith(false);
  });
});

describe('resetPricingFilters 本身', () => {
  it('把 filterPointsOnly 重置回默认的关闭态', async () => {
    const { resetPricingFilters } = await import('../../../../helpers/utils');
    const setFilterPointsOnly = vi.fn();
    resetPricingFilters({ setFilterPointsOnly });
    expect(setFilterPointsOnly).toHaveBeenCalledWith(false);
  });

  it('调用方漏传某个 setter 时不炸（全是可选链）', async () => {
    const { resetPricingFilters } = await import('../../../../helpers/utils');
    expect(() => resetPricingFilters({})).not.toThrow();
  });
});
