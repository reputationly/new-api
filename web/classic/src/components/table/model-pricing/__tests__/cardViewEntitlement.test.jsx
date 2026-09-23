import React from 'react';
import { describe, it, expect, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

import PricingCardView from '../view/card/PricingCardView';

// 卡片视图的真实渲染。
//
// 这里曾经写成 record.model_name，而卡片视图里的变量叫 model——构建能过、纯函数测试
// 全绿，但卡片模式下整个模型广场一渲染就抛 ReferenceError，而且与登没登录、有没有
// 套餐无关（参数在调用前就要求值）。所以这里第一条守的是「无套餐时能正常渲染」。

const t = (s) => s;

const model = {
  model_name: 'img-test',
  quota_type: 1,
  model_price: 0.04,
  enable_groups: ['default'],
};

const renderCards = (entitlementConfig) =>
  render(
    <PricingCardView
      filteredModels={[model]}
      loading={false}
      pageSize={20}
      currentPage={1}
      setPageSize={() => {}}
      setCurrentPage={() => {}}
      selectedGroup='default'
      groupRatio={{ default: 1 }}
      groupModelRatio={{}}
      groupTimeRatio={{}}
      copyText={() => {}}
      currency='USD'
      siteDisplayType='USD'
      displayPrice={(v) => String(v)}
      t={t}
      openModelDetail={() => {}}
      pointsConfig={{ enabled: false }}
      entitlementConfig={entitlementConfig}
    />,
  );

beforeEach(() => {
  localStorage.setItem('quota_per_unit', '500000');
  localStorage.setItem('quota_display_type', 'USD');
});

describe('卡片视图', () => {
  it('无套餐时正常渲染', () => {
    renderCards({ coverage: null, quotaPerComputePoint: 100 });
    expect(screen.getByText('img-test')).toBeTruthy();
  });

  it('被覆盖时出套餐角标与套餐内价', () => {
    renderCards({
      coverage: {
        'img-test': { plan_title: '专业版', consume_points: true, discount: 1 },
      },
      quotaPerComputePoint: 100,
    });
    expect(screen.getByText('专业版')).toBeTruthy();
    // 0.04 美元 × 500000 / 100 = 200 点
    expect(screen.getByText(/200 算力点/)).toBeTruthy();
  });
});
