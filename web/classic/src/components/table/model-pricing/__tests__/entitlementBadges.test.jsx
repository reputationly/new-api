import React from 'react';
import { describe, it, expect, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

import { getPricingTableColumns } from '../view/table/PricingTableColumns';

// 角标在**表格列定义里**的接线。
//
// 纯函数 buildEntitlementBadges 全绿不代表页面上真能看到：列定义忘了调它、
// 或者页面层忘了把 entitlementConfig 透传下来，测试照样全绿而角标一个不出。
// 这个坑本会话已经踩过两次（reserveFunding 的分支、tryEntitlement 的调用），
// 所以这里专门守接线。

const t = (s) => s;

const baseRecord = {
  model_name: 'gpt-test',
  quota_type: 0,
  model_ratio: 2.5,
  model_price: 0,
  enable_groups: ['default'],
};

const coverage = (over = {}) => ({
  entitlement_id: 1,
  plan_id: 1,
  plan_title: '专业版',
  consume_points: true,
  discount: 1,
  limit_count: 0,
  used_count: 0,
  reset_period: 'never',
  channel_limited: false,
  ...over,
});

// getPricingTableColumns 内部调了 useIsMobile()，是个 Hook——必须在组件渲染中调用，
// 直接在测试里调会拿到 null dispatcher。
const Harness = ({ entitlementConfig }) => {
  const columns = getPricingTableColumns({
    t,
    selectedGroup: 'default',
    groupRatio: { default: 1 },
    groupModelRatio: {},
    groupTimeRatio: {},
    copyText: () => {},
    currency: 'USD',
    siteDisplayType: 'USD',
    displayPrice: (v) => String(v),
    pointsConfig: { enabled: false },
    entitlementConfig,
  });
  const col = columns.find((c) => c.dataIndex === 'model_name');
  return <div>{col.render('gpt-test', baseRecord, 0)}</div>;
};

const renderModelNameCell = (entitlementConfig) =>
  render(<Harness entitlementConfig={entitlementConfig} />);

beforeEach(() => {
  localStorage.setItem('quota_per_compute_point', '100');
});

describe('模型广场的套餐权益角标', () => {
  // 未登录 / 无套餐：展示必须与加这个功能之前完全一致
  it('无覆盖数据时一个角标都不出', () => {
    renderModelNameCell({ coverage: null, quotaPerComputePoint: 100 });
    expect(screen.queryByText('专业版')).toBeNull();
  });

  it('该模型未被覆盖时不出角标', () => {
    renderModelNameCell({
      coverage: { 'other-model': coverage() },
      quotaPerComputePoint: 100,
    });
    expect(screen.queryByText('专业版')).toBeNull();
  });

  it('命中时显示套餐名', () => {
    renderModelNameCell({
      coverage: { 'gpt-test': coverage() },
      quotaPerComputePoint: 100,
    });
    expect(screen.getByText('专业版')).toBeTruthy();
  });

  it('限次权益显示次数与周期', () => {
    renderModelNameCell({
      coverage: {
        'gpt-test': coverage({ limit_count: 500, reset_period: 'monthly' }),
      },
      quotaPerComputePoint: 100,
    });
    expect(screen.getByText('500 次/月')).toBeTruthy();
  });

  it('不消耗算力点的权益显示「免费」而非次数', () => {
    renderModelNameCell({
      coverage: {
        'gpt-test': coverage({
          consume_points: false,
          limit_count: 500,
          reset_period: 'monthly',
        }),
      },
      quotaPerComputePoint: 100,
    });
    expect(screen.getByText('不消耗算力点')).toBeTruthy();
    expect(screen.queryByText('500 次/月')).toBeNull();
  });

  // 限渠道的覆盖是有条件的：不标的话用户会以为这个价必然拿得到
  it('限渠道要标出来', () => {
    renderModelNameCell({
      coverage: { 'gpt-test': coverage({ channel_limited: true }) },
      quotaPerComputePoint: 100,
    });
    expect(screen.getByText('限特定渠道')).toBeTruthy();
  });

  // entitlementConfig 整个缺失时不能崩——页面在数据到达前会先渲染一次
  it('entitlementConfig 未就绪时不报错', () => {
    expect(() => renderModelNameCell(undefined)).not.toThrow();
  });
});
