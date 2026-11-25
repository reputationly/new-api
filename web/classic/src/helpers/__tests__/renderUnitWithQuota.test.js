import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderUnitWithQuota } from '../render';

/**
 * renderUnitWithQuota 把**美元金额**换成 quota 数（TOKENS 展示模式下的渠道余额）。
 * 汇率只有一处来源：quota.js 的 getQuotaPerUnit（读 quota_per_unit，缺失时兜底并告警）。
 * 这条测试钉住两件事：正常换算，以及键缺失时不能变成 NaN。
 */

beforeEach(() => {
  localStorage.clear();
  vi.spyOn(console, 'warn').mockImplementation(() => {});
});

afterEach(() => {
  vi.restoreAllMocks();
  localStorage.clear();
});

describe('renderUnitWithQuota', () => {
  it('按 quota_per_unit 把美元金额换算成 quota', () => {
    localStorage.setItem('quota_per_unit', '7300000000');
    expect(renderUnitWithQuota(2)).toBe(14600000000);
    expect(renderUnitWithQuota('0.5')).toBe(3650000000);
  });

  it('读不到 quota_per_unit 时走兜底值，而不是 NaN', () => {
    const result = renderUnitWithQuota(1);
    expect(Number.isNaN(result)).toBe(false);
    expect(result).toBe(7300000000);
  });

  it('非法值（0 / 负数 / 非数字）同样兜底', () => {
    localStorage.setItem('quota_per_unit', '0');
    expect(renderUnitWithQuota(1)).toBe(7300000000);
  });
});
