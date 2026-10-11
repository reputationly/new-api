import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';

/**
 * F1/F6：换算率的 fallback 只在读不到配置时生效，且只在 console 里告警一次。
 *
 * 这几个值正常都由 /api/status 在页面渲染前写入 localStorage；一旦它们与后端不一致，
 * 全站金额都会按错误的口径展示——所以 fallback 必须集中在一处（helpers/quota.js），
 * 而且取值要和后端出厂默认一致：1 美元 = 730000000 quota、1 元 = 100000000 quota、
 * 1 积分 = 1000000 quota。
 */

let warnSpy;

beforeEach(() => {
  localStorage.clear();
  warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => {});
});

afterEach(() => {
  warnSpy.mockRestore();
  localStorage.clear();
});

// 告警用模块级 Set 去重，必须拿全新的模块实例才能验证「只告警一次」
const freshQuota = async () => {
  vi.resetModules();
  return import('../quota');
};

describe('getQuotaPerUnit 的 fallback', () => {
  it('localStorage = 730000000 时直接使用，不告警', async () => {
    localStorage.setItem('quota_per_unit', '730000000');
    const { getQuotaPerUnit } = await freshQuota();
    expect(getQuotaPerUnit()).toBe(730000000);
    expect(warnSpy).not.toHaveBeenCalled();
  });

  it('读不到时 fallback 到 730000000，且只告警一次', async () => {
    const { getQuotaPerUnit } = await freshQuota();
    expect(getQuotaPerUnit()).toBe(730000000);
    expect(getQuotaPerUnit()).toBe(730000000);
    expect(getQuotaPerUnit()).toBe(730000000);
    expect(warnSpy).toHaveBeenCalledTimes(1);
    expect(String(warnSpy.mock.calls[0][0])).toContain('quota_per_unit');
  });

  it('非法值（0 / 负数 / 非数字）同样走 fallback', async () => {
    localStorage.setItem('quota_per_unit', '0');
    const { getQuotaPerUnit } = await freshQuota();
    expect(getQuotaPerUnit()).toBe(730000000);
    expect(warnSpy).toHaveBeenCalledTimes(1);

    localStorage.setItem('quota_per_unit', 'abc');
    const again = await freshQuota();
    expect(again.getQuotaPerUnit()).toBe(730000000);
    expect(warnSpy).toHaveBeenCalledTimes(2);
  });

  it('fallback 常量与实现返回值一致', async () => {
    const { getQuotaPerUnit, FALLBACK_QUOTA_PER_UNIT } = await freshQuota();
    expect(FALLBACK_QUOTA_PER_UNIT).toBe(730000000);
    expect(getQuotaPerUnit()).toBe(FALLBACK_QUOTA_PER_UNIT);
  });
});

describe('getQuotaPerYuan 的 fallback', () => {
  it('localStorage 有值时直接使用，不告警', async () => {
    localStorage.setItem('quota_per_yuan', '100000000');
    const { getQuotaPerYuan } = await freshQuota();
    expect(getQuotaPerYuan()).toBe(100000000);
    expect(warnSpy).not.toHaveBeenCalled();
  });

  it('读不到时 fallback 到 100000000，且只告警一次', async () => {
    const { getQuotaPerYuan, FALLBACK_QUOTA_PER_YUAN } = await freshQuota();
    expect(getQuotaPerYuan()).toBe(100000000);
    expect(getQuotaPerYuan()).toBe(100000000);
    expect(FALLBACK_QUOTA_PER_YUAN).toBe(100000000);
    expect(warnSpy).toHaveBeenCalledTimes(1);
    expect(String(warnSpy.mock.calls[0][0])).toContain('quota_per_yuan');
  });
});

describe('getQuotaPerPoint / getQuotaPerComputePoint 的 fallback', () => {
  it('有配置时用配置值，不告警', async () => {
    localStorage.setItem('quota_per_point', '10000');
    localStorage.setItem('quota_per_compute_point', '10000');
    const { getQuotaPerPoint, getQuotaPerComputePoint } = await freshQuota();
    expect(getQuotaPerPoint()).toBe(10000);
    expect(getQuotaPerComputePoint()).toBe(10000);
    expect(warnSpy).not.toHaveBeenCalled();
  });

  it('读不到时各自 fallback 到 1000000，且各自只告警一次', async () => {
    const { getQuotaPerPoint, getQuotaPerComputePoint } = await freshQuota();
    expect(getQuotaPerPoint()).toBe(1000000);
    expect(getQuotaPerPoint()).toBe(1000000);
    expect(getQuotaPerComputePoint()).toBe(1000000);
    expect(getQuotaPerComputePoint()).toBe(1000000);
    expect(warnSpy).toHaveBeenCalledTimes(2);
    expect(String(warnSpy.mock.calls[0][0])).toContain('quota_per_point');
    expect(String(warnSpy.mock.calls[1][0])).toContain(
      'quota_per_compute_point',
    );
  });
});
