import React from 'react';
import { describe, it, expect, beforeEach } from 'vitest';
import { render } from '@testing-library/react';
import { calculateModelPrice, formatDynamicPriceSummary } from '../utils';
import { getDisplayCurrency } from '../quota';
import { renderQuota } from '../render';

/**
 * F4：定价页的单价与后端样例一致。
 *
 * 定价配置（/api/pricing 的 model_ratio / model_price / 视频矩阵）现在**原样**就是
 * 人民币：model_ratio 是 ¥/百万 tokens，model_price 是 ¥/次。定价页只做「展示币种」
 * 一次折算（CNY → 1），不再有 × 2、不再有 × 汇率。
 *
 * 样例（设计文档 §2.3）：
 *   E1  deepseek 输入 ¥2/百万、输出 ×4、分组 0.5，prompt 199 / completion 4096
 *       → 16,583,000 quota = ¥0.016583
 *   E5  按次生图 ¥0.2/次、分组 1 → 200,000,000 quota = ¥0.2
 */

const QPY = 1000000000;

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem('quota_per_yuan', String(QPY));
  localStorage.setItem('quota_display_type', 'CNY');
});

// 与定价页 useModelPricingData 的 displayPrice 同一条链：人民币 × ratePerCny
const displayPrice = (cny, digits) => {
  const { symbol, ratePerCny } = getDisplayCurrency();
  return `${symbol}${(Number(cny) * ratePerCny).toFixed(digits)}`;
};

const priceOf = (record, groupRatio, over = {}) =>
  calculateModelPrice({
    record,
    selectedGroup: 'default',
    groupRatio: { default: groupRatio },
    groupModelRatio: {},
    displayPrice,
    quotaDisplayType: 'CNY',
    precision: 2,
    ...over,
  });

describe('定价页单价（E1 / E5）', () => {
  it('E1：ratio = ¥2/百万、分组 0.5 → 输入 ¥1.00、输出 ¥4.00', () => {
    const d = priceOf(
      {
        model_name: 'deepseek',
        quota_type: 0,
        model_ratio: 2,
        completion_ratio: 4,
        enable_groups: ['default'],
      },
      0.5,
    );
    expect(d.isPerToken).toBe(true);
    expect(d.inputPrice).toBe('¥1.00');
    expect(d.completionPrice).toBe('¥4.00');
  });

  it('E1：16,583,000 quota 就是 ¥0.016583（与后端样例同一个数）', () => {
    // (199 + 4096 × 4) × 2 × 1000 × 0.5 = 16,583,000
    const quota = (199 + 4096 * 4) * 2 * 1000 * 0.5;
    expect(quota).toBe(16583000);
    expect(renderQuota(quota, 9)).toBe('¥0.016583000');
  });

  it('E5：按次生图 ¥0.2/次、分组 1 → ¥0.20（200,000,000 quota）', () => {
    const d = priceOf(
      {
        model_name: 'img-x',
        quota_type: 1,
        model_price: 0.2,
        enable_groups: ['default'],
      },
      1,
    );
    expect(d.isPerToken).toBe(false);
    expect(d.price).toBe('¥0.20');
    expect(d.originalPrice).toBeNull();
    expect(renderQuota(0.2 * QPY, 9)).toBe('¥0.200000000');
    expect(0.2 * QPY).toBe(200000000);
  });

  it('折扣时同时给出折前价（少乘一个分组倍率）', () => {
    const d = priceOf(
      {
        model_name: 'img-x',
        quota_type: 1,
        model_price: 0.2,
        enable_groups: ['default'],
      },
      0.5,
    );
    expect(d.price).toBe('¥0.10');
    expect(d.originalPrice).toBe('¥0.20');
  });
});

describe('定价页单价：TOKENS 展示模式给出倍率而不是金额', () => {
  it('model_ratio 原样作为倍率显示', () => {
    const d = priceOf(
      {
        model_name: 'cheap',
        quota_type: 0,
        model_ratio: 0.01,
        completion_ratio: 2,
        enable_groups: ['default'],
      },
      1,
      { quotaDisplayType: 'TOKENS' },
    );
    expect(d.isTokensDisplay).toBe(true);
    expect(d.inputRatio).toBe(0.01);
    expect(d.completionRatio).toBe(2);
  });
});

// 定价页/卡片视图的动态计费摘要：v1 系数是历史美元口径，展示时先折成人民币，
// 否则同一个模型会显得比历史价格便宜 7.3 倍
describe('formatDynamicPriceSummary：v1 / v2 表达式的系数口径', () => {
  const t = (s) => s;
  const textOf = (node) =>
    render(React.createElement('div', null, node)).container.textContent;

  it('v1（无前缀）：系数 × 汇率后展示', () => {
    const text = textOf(
      formatDynamicPriceSummary('tier("base", p * 2 + c * 4)', t, 1),
    );
    expect(text).toContain('¥14.60'); // ¥2/1M × 7.3
    expect(text).toContain('¥29.20'); // ¥4/1M × 7.3
  });

  it('v1 前缀写法同样按美元口径处理', () => {
    const text = textOf(
      formatDynamicPriceSummary('v1:tier("base", p * 2 + c * 4)', t, 1),
    );
    expect(text).toContain('¥14.60');
  });

  it('v2：系数本身就是 ¥/百万 tokens，不再乘汇率', () => {
    const text = textOf(
      formatDynamicPriceSummary('v2:tier("base", p * 14.6 + c * 29.2)', t, 1),
    );
    expect(text).toContain('¥14.60');
    expect(text).toContain('¥29.20');
  });
});
