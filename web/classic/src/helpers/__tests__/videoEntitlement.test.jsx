import React from 'react';
import { describe, it, expect, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { calculateModelPrice, formatVideoMatrixSummary } from '../utils';
import VideoMatrixBreakdown from '../../components/table/model-pricing/modal/components/VideoMatrixBreakdown';

// 视频矩阵模型的套餐内价。
//
// calculateModelPrice 对视频矩阵提前返回，此前在算套餐内价之前就短路了：被套餐覆盖的
// 视频模型只显示原价，而扣费侧照样把矩阵算出的 quota 乘折扣换成算力点。

const t = (s) => s;

beforeEach(() => {
  // 1 美元 = 500000 quota，1 算力点 = 100 quota → 1 美元 = 5000 点
  localStorage.setItem('quota_per_unit', '500000');
  localStorage.setItem('quota_display_type', 'USD');
});

const covered = (over = {}) => ({
  plan_title: '专业版',
  consume_points: true,
  discount: 1,
  ...over,
});

const perCall = {
  model_name: 'vid',
  quota_type: 1,
  model_price: 1,
  enable_groups: ['vip'],
  video_pricing: {
    mode: 'per_call',
    per_call: { '720p': { '5s': 0.2, '10s': 0.4 } },
  },
};

const perSecond = {
  model_name: 'vid-s',
  quota_type: 1,
  model_price: 1,
  enable_groups: ['default'],
  video_pricing: { mode: 'per_second', per_second: { '720p': 0.05 } },
};

const priceOf = (record, group, ratio, coverage, qpcp = 100) =>
  calculateModelPrice({
    record,
    selectedGroup: group,
    groupRatio: { [group]: ratio },
    groupModelRatio: {},
    displayPrice: (v) => String(v),
    quotaDisplayType: 'USD',
    entitlementCoverage: coverage,
    quotaPerComputePoint: qpcp,
  });

describe('calculateModelPrice：视频矩阵的套餐内区间', () => {
  // 分组 0.5 折：0.1 / 0.2 美元 → 500 / 1000 点；与货币区间同口径（折后价）
  it('按分组折后价逐格换算，取区间', () => {
    const d = priceOf(perCall, 'vip', 0.5, covered());
    expect(d.isVideoMatrix).toBe(true);
    expect(d.entitlementRange).toEqual({ lo: 500, hi: 1000 });
  });

  it('权益折扣作用在消耗侧', () => {
    const d = priceOf(perCall, 'vip', 0.5, covered({ discount: 0.5 }));
    expect(d.entitlementRange).toEqual({ lo: 250, hi: 500 });
  });

  // 按次一次调用结算一次：不足 1 点按 1 点烧，显示也要 ceil
  it('按次向上取整', () => {
    const d = priceOf(perCall, 'vip', 0.5, covered(), 300);
    // 0.1 × 500000 / 300 = 166.67 → 167；0.2 → 333.33 → 334
    expect(d.entitlementRange).toEqual({ lo: 167, hi: 334 });
  });

  it('按秒不取整（结算时才按时长取整）', () => {
    const d = priceOf(perSecond, 'default', 1, covered(), 300);
    expect(d.entitlementRange.lo).toBeCloseTo(83.333, 2);
  });

  it('无覆盖 / 不消耗算力点时没有区间', () => {
    expect(priceOf(perCall, 'vip', 0.5, null).entitlementRange).toBeNull();
    expect(
      priceOf(perCall, 'vip', 0.5, covered({ consume_points: false }))
        .entitlementRange,
    ).toBeNull();
  });
});

describe('formatVideoMatrixSummary：列表摘要', () => {
  it('被覆盖时追加套餐内区间', () => {
    render(
      <div>
        {formatVideoMatrixSummary(priceOf(perCall, 'vip', 0.5, covered()), t)}
      </div>,
    );
    expect(screen.getByText(/套餐内/).textContent).toContain('500 ~ 1,000');
  });

  it('按秒保留两位小数', () => {
    render(
      <div>
        {formatVideoMatrixSummary(
          priceOf(perSecond, 'default', 1, covered(), 300),
          t,
        )}
      </div>,
    );
    expect(screen.getByText(/套餐内/).textContent).toContain('83.33');
  });

  it('未覆盖时一个字都不多出', () => {
    render(
      <div>
        {formatVideoMatrixSummary(priceOf(perCall, 'vip', 0.5, null), t)}
      </div>,
    );
    expect(screen.queryByText(/套餐内/)).toBeNull();
  });
});

describe('VideoMatrixBreakdown：详情逐格价目', () => {
  // 口径与本表货币价一致（基础单价，不乘分组倍率）：0.2 / 0.4 美元 → 1000 / 2000 点
  it('每格价格下追加该格的套餐内点数', () => {
    render(
      <VideoMatrixBreakdown
        videoPricing={perCall.video_pricing}
        t={t}
        entitlementCoverage={covered()}
        quotaPerComputePoint={100}
      />,
    );
    expect(screen.getByText(/^1,000 算力点$/)).toBeTruthy();
    expect(screen.getByText(/^2,000 算力点$/)).toBeTruthy();
  });

  it('未覆盖时不出点数', () => {
    render(<VideoMatrixBreakdown videoPricing={perCall.video_pricing} t={t} />);
    expect(screen.queryByText(/算力点/)).toBeNull();
  });
});
