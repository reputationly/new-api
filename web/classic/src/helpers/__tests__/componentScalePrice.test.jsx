import { describe, it, expect } from 'vitest';
import { calculateModelPrice } from '../utils';

// 时段价可以只改某一个分量。这条锁死「缓存价按自己的系数折算」：
// 常规 输入 2、cache_ratio 0.02（¥0.04）；闲时 输入 1、缓存 ¥0.01
// → cache 折算系数 = f_cache/f_input = 0.25/0.5 = 0.5（后端下发）
// → 缓存价 = 输入价(¥1) × 0.02 × 0.5 = ¥0.01
describe('分量价目：缓存按自己的系数折算', () => {
  const record = {
    model_name: 'm',
    quota_type: 0,
    model_ratio: 2,
    completion_ratio: 4,
    // 用 0.5 而不是 0.02：价格显示保留 2 位小数，0.02 档的差异会被取整吃掉
    cache_ratio: 0.5,
    enable_groups: ['default'],
  };
  const base = {
    record,
    selectedGroup: 'default',
    groupRatio: { default: 1 },
    // 闲时终值 = 常规 0.5 × 输入系数 0.5 = 0.25
    groupModelRatio: { default: { m: 0.25 } },
    displayPrice: (v) => `¥${Number(v).toFixed(2)}`,
  };

  it('不传分量系数：缓存价按整单倍率走', () => {
    const p = calculateModelPrice(base);
    // 2 × 0.5 × 0.25 = 0.25
    expect(p.cachePrice).toBe('¥0.25');
  });

  it('传了 cache 折算系数 0.5：缓存价按 0.5 收缩', () => {
    const plain = calculateModelPrice(base);
    const scaled = calculateModelPrice({
      ...base,
      componentScaleMap: { default: { m: { cache: 0.5 } } },
    });
    const num = (s) => Number(String(s).replace(/[^0-9.]/g, ''));
    expect(num(plain.cachePrice)).toBeGreaterThan(0);
    // 缓存价 = 输入价 × cache_ratio × 系数 → 0.5 倍（显示保留 2 位，认到 1 位小数）
    expect(num(scaled.cachePrice) / num(plain.cachePrice)).toBeCloseTo(0.5, 1);
  });
});
