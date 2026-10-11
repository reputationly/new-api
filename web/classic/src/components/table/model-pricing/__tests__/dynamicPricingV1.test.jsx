import React from 'react';
import { describe, it, expect, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import DynamicPricingBreakdown from '../modal/components/DynamicPricingBreakdown';

/**
 * 定价详情弹窗的逐档价目。
 *
 * v1 表达式（无前缀 / `v1:`）的系数是历史美元口径，后端已拒绝结算，前端不改写存量
 * 表达式——这里按汇率折成人民币展示，只保证数字与历史价格一致；v2 的系数本身就是
 * ¥/百万 tokens。漏了这一层，同一个模型在定价页会显得比实际收费便宜 7.3 倍。
 */

const t = (s) => s;

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem('quota_per_yuan', '100000000');
  localStorage.setItem('quota_display_type', 'CNY');
  localStorage.setItem('status', JSON.stringify({ usd_exchange_rate: 7.3 }));
});

describe('DynamicPricingBreakdown：v1 / v2 的系数口径', () => {
  it('v1（无前缀）：$2/1M 的旧表达式显示成 ¥14.60', () => {
    render(
      <DynamicPricingBreakdown billingExpr={'tier("base", p * 2)'} t={t} />,
    );
    expect(screen.getByText('¥14.60')).toBeTruthy();
  });

  it('v2：¥14.6/1M 的新表达式显示成 ¥14.60', () => {
    render(
      <DynamicPricingBreakdown
        billingExpr={'v2:tier("base", p * 14.6)'}
        t={t}
      />,
    );
    expect(screen.getByText('¥14.60')).toBeTruthy();
  });

  it('v1 与 v2 表达同一个价格时，两处展示一致', () => {
    const v1 = render(
      <DynamicPricingBreakdown billingExpr={'tier("base", p * 2)'} t={t} />,
    );
    const v1Text = v1.container.textContent;
    v1.unmount();
    const v2 = render(
      <DynamicPricingBreakdown
        billingExpr={'v2:tier("base", p * 14.6)'}
        t={t}
      />,
    );
    const v2Text = v2.container.textContent;
    expect(v1Text).toContain('¥14.60');
    expect(v2Text).toContain('¥14.60');
    expect(v1Text).not.toContain('¥2.00');
  });
});
