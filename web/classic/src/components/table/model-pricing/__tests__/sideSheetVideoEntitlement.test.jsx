import React from 'react';
import { describe, it, expect, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

import ModelDetailSideSheet from '../modal/ModelDetailSideSheet';

// 详情页 → 逐格价目表的接线。VideoMatrixBreakdown 自己的测试全绿不代表详情页里
// 真能看到：详情页忘了把 coverage 传下去，逐格点数就一个都不出。

const t = (s) => s;

const videoModel = {
  model_name: 'vid',
  quota_type: 1,
  model_price: 1,
  enable_groups: ['default'],
  video_pricing: {
    mode: 'per_call',
    per_call: { '720p': { '5s': 0.2 } },
  },
};

const renderSheet = (entitlementConfig) =>
  render(
    <ModelDetailSideSheet
      visible
      onClose={() => {}}
      modelData={videoModel}
      groupRatio={{ default: 1 }}
      groupModelRatio={{}}
      groupTimeRatio={{}}
      usableGroup={{ default: 'default' }}
      currency='USD'
      siteDisplayType='USD'
      displayPrice={(v) => String(v)}
      vendorsMap={{}}
      endpointMap={{}}
      autoGroups={[]}
      pointsConfig={{ enabled: false }}
      entitlementConfig={entitlementConfig}
      t={t}
    />,
  );

beforeEach(() => {
  localStorage.setItem('quota_per_unit', '500000');
  localStorage.setItem('quota_display_type', 'USD');
});

describe('详情页视频逐格价目的套餐内点数', () => {
  it('被覆盖时逐格出点数', () => {
    renderSheet({
      coverage: {
        vid: { plan_title: '专业版', consume_points: true, discount: 1 },
      },
      quotaPerComputePoint: 100,
    });
    // 0.2 美元 × 500000 / 100 = 1000 点（基础单价口径）
    expect(screen.getAllByText(/^1,000 算力点$/).length).toBeGreaterThan(0);
  });

  it('未覆盖时不出点数', () => {
    renderSheet({ coverage: null, quotaPerComputePoint: 100 });
    expect(screen.queryByText(/^[\d,.]+ 算力点$/)).toBeNull();
  });
});
