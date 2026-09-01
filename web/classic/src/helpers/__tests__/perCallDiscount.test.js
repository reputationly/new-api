import { describe, it, expect } from 'vitest';
import { calculateModelPrice, getModelPriceItems } from '../utils';

/**
 * 按次计费的折前价。
 *
 * 按量计费那条分支一直有 originalInputPrice / originalCompletionPrice，按次那条
 * 没有——而自建的媒体类模型（z-image、ltx2.5、indextts-2.5 等）几乎全是按次，
 * 正好也是折扣力度最大的一批（5 折）。结果是打折最狠的模型反而看不到原价。
 */

const t = (s) => s;
const displayPrice = (usd) => `$${Number(usd).toFixed(6)}`;

const perCall = (ratio) =>
  calculateModelPrice({
    record: {
      model_name: 'z-image',
      quota_type: 1,
      model_price: 0.04,
      enable_groups: ['default'],
    },
    selectedGroup: 'default',
    groupRatio: { default: 1 },
    // 后端算好的终值（分组倍率 × 模型折扣），见 controller/pricing.go
    groupModelRatio: { default: { 'z-image': ratio } },
    tokenUnit: 'M',
    displayPrice,
  });

describe('按次计费的折前价', () => {
  it('打折时同时给出折后价和折前价', () => {
    const p = perCall(0.5);
    expect(p.price).toBe('$0.02');
    expect(p.originalPrice).toBe('$0.04');
  });

  // 不打折还划一道线，用户会以为自己错过了什么
  it('无折扣时不给折前价', () => {
    expect(perCall(1).originalPrice).toBeNull();
  });

  // 倍率 > 1 是涨价，划线原价会显示成「原价更便宜」，比不显示更糟
  it('倍率大于 1 时不给折前价', () => {
    expect(perCall(1.5).originalPrice).toBeNull();
  });

  // 三个渲染点（PricingTableColumns / ModelPricingTable / formatPriceInfo）
  // 都只认 item.originalValue，helper 不透传的话改了也白改
  it('折前价透传到 items 的 originalValue', () => {
    const fixed = getModelPriceItems(perCall(0.5), t, 'USD').find(
      (i) => i.key === 'fixed',
    );
    expect(fixed.originalValue).toBe('$0.04');
  });

  it('无折扣时 originalValue 为空，渲染端的 && 判断会跳过划线', () => {
    const fixed = getModelPriceItems(perCall(1), t, 'USD').find(
      (i) => i.key === 'fixed',
    );
    expect(fixed.originalValue).toBeFalsy();
  });
});
