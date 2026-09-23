import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';

import PricingTopSection from '../layout/header/PricingTopSection';

// 桌面端「仅看套餐可用」开关的接线。
//
// 桌面链路比 mobile 多两层：PricingTopSection → PricingVendorIntroWithSkeleton →
// PricingVendorIntro → SearchActions。中间那层只转发了积分的三个 prop，套餐这三个被
// 丢掉，开关在桌面端永远不出现——而筛选逻辑、重置逻辑的测试照样全绿。

const t = (s) => s;

// 其余 prop 必须是**稳定引用**：真实页面里它们是 state，重渲染时不变。每次都传新建的
// []/{} 会让 useCallback 因为别的依赖变了而重算，依赖数组漏没漏项就测不出来。
const EMPTY = [];
const SIDEBAR = {};
const baseProps = {
  isMobile: false,
  loading: false,
  filterVendor: 'all',
  models: EMPTY,
  filteredModels: EMPTY,
  selectedRowKeys: EMPTY,
  searchValue: '',
  sidebarProps: SIDEBAR,
  t,
};

const renderDesktop = (props) =>
  render(<PricingTopSection {...baseProps} {...props} />);

const withCoverage = { coverage: { m: { plan_title: 'P' } } };

describe('桌面端「仅看套餐可用」开关', () => {
  it('有覆盖数据时出现，点击回写筛选状态', () => {
    const setFilterEntitlementOnly = vi.fn();
    renderDesktop({
      entitlementConfig: withCoverage,
      filterEntitlementOnly: false,
      setFilterEntitlementOnly,
    });
    expect(screen.getByText('仅看套餐可用')).toBeTruthy();
    const label = screen.getByText('仅看套餐可用');
    fireEvent.click(label.parentElement.querySelector('[role="switch"]'));
    expect(setFilterEntitlementOnly).toHaveBeenCalledWith(
      true,
      expect.anything(),
    );
  });

  it('无覆盖数据时不出现', () => {
    renderDesktop({ entitlementConfig: { coverage: null } });
    expect(screen.queryByText('仅看套餐可用')).toBeNull();
  });

  // 覆盖数据是异步到达的：首次渲染时为 null。renderSearchActions 包在 useCallback 里，
  // 依赖数组漏了 entitlementConfig 的话，数据到了开关也不会出现。
  it('覆盖数据后到达时开关随之出现', () => {
    const { rerender } = renderDesktop({
      entitlementConfig: { coverage: null },
    });
    expect(screen.queryByText('仅看套餐可用')).toBeNull();
    rerender(
      <PricingTopSection {...baseProps} entitlementConfig={withCoverage} />,
    );
    expect(screen.getByText('仅看套餐可用')).toBeTruthy();
  });
});
