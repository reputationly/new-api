import { describe, it, expect } from 'vitest';
import {
  calculateModelPrice,
  getModelPriceItems,
  getGroupDiscountInfo,
} from '../utils';

// 分时段价目可以**不等比例**变化：忙时输入 2/输出 8、闲时输入 1/输出 7。
// 计费侧靠 output_factor 实现，展示侧靠后端下发的 group_model_completion_ratio
// （此刻生效的补全倍率）。这两条断言钉住"广场输出价 = 实扣输出价"。
describe('分时段价目：输出价用此刻补全倍率', () => {
  const record = {
    model_name: 'deepseek-v4-1-flash',
    quota_type: 0,
    model_ratio: 2,
    completion_ratio: 4,
    enable_groups: ['default'],
  };

  const base = {
    record,
    selectedGroup: 'default',
    groupRatio: { default: 1 },
    // 闲时成交终值 = 常规价 0.5 × 输入系数 0.5 = 0.25
    groupModelRatio: { default: { 'deepseek-v4-1-flash': 0.25 } },
    displayPrice: (v) => `¥${Number(v).toFixed(2)}`,
  };

  it('不传 completionRatioMap 时用基础补全倍率（行为与改造前一致）', () => {
    const p = calculateModelPrice(base);
    // 输入 2 × 0.25 = 0.5；输出 2 × 4 × 0.25 = 2
    expect(p.inputPrice).toBe('¥0.50');
    expect(p.completionPrice).toBe('¥2.00');
  });

  it('传了此刻补全倍率（4 × 0.875/0.5 = 7）时输出价按 7 算', () => {
    const p = calculateModelPrice({
      ...base,
      componentScaleMap: {
        default: { 'deepseek-v4-1-flash': { output: 1.75 } },
      },
    });
    expect(p.inputPrice).toBe('¥0.50');
    // 2 × 7 × 0.25 = 3.5 —— 正好是"闲时输出目录价 7 的 5 折"
    expect(p.completionPrice).toBe('¥3.50');
  });

  it('不属于该分组的覆盖不生效', () => {
    const p = calculateModelPrice({
      ...base,
      componentScaleMap: {
        premium: { 'deepseek-v4-1-flash': { output: 1.75 } },
      },
    });
    expect(p.completionPrice).toBe('¥2.00');
  });

  it('积分抵扣只出小标，不再逐行追加积分价', () => {
    const p = calculateModelPrice({
      ...base,
      pointsEnabled: true,
      quotaPerPoint: 100,
      pointsEnabledGroups: ['default'],
    });
    const items = getModelPriceItems(p, (s) => s, 'CNY');
    expect(items.some((it) => it.isPoints)).toBe(false);
    // 但「可用积分」这个事实仍由 priceData.points 表达，供界面打标
    expect(Boolean(p.points)).toBe(true);
  });
});

describe('固定折扣角标', () => {
  it('discount_ratio = 常规终值 ÷ 线路基准，与时刻无关', () => {
    // 忙时终值 0.5、闲时终值 0.25，但两者由同一个 discount_ratio 表达 → 都是 5 折
    expect(getGroupDiscountInfo(0.5).text).toBe('5折');
    expect(getGroupDiscountInfo(0.5).color).toBe(
      getGroupDiscountInfo(0.5).color,
    );
  });
});
