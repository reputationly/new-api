import { describe, it, expect, beforeEach } from 'vitest';
import {
  FALLBACK_QUOTA_PER_UNIT,
  FALLBACK_QUOTA_PER_YUAN,
  getDisplayCurrency,
  getQuotaPerYuan,
  quotaToDisplayAmount,
  displayAmountToQuota,
} from '../quota';
import {
  getQuotaWithUnit,
  renderNumber,
  renderQuota,
  renderQuotaTrimmed,
} from '../render';

/**
 * F1：换算基准改为人民币。
 *
 *   CNY    → 1（原样展示人民币）
 *   USD    → 1 / 汇率
 *   CUSTOM → 自定义汇率 / 汇率（自定义汇率原本是「1 美元 = X 自定义币」）
 *   TOKENS → 不折算（直接显示 quota 原值）
 *
 * 换算链是 `quota / quota_per_yuan × ratePerCny`：CNY 模式下**不再**乘汇率。
 */

const RATE = 7.3;
const QPY = FALLBACK_QUOTA_PER_YUAN;

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem('quota_per_yuan', String(QPY));
  localStorage.setItem('quota_per_unit', String(FALLBACK_QUOTA_PER_UNIT));
  localStorage.setItem('status', JSON.stringify({ usd_exchange_rate: RATE }));
});

describe('getDisplayCurrency 的四种展示模式', () => {
  it('CNY：ratePerCny = 1，符号 ¥', () => {
    localStorage.setItem('quota_display_type', 'CNY');
    expect(getDisplayCurrency()).toEqual({
      symbol: '¥',
      ratePerCny: 1,
      type: 'CNY',
    });
  });

  it('USD：ratePerCny = 1 / 汇率', () => {
    localStorage.setItem('quota_display_type', 'USD');
    const { symbol, ratePerCny, type } = getDisplayCurrency();
    expect(symbol).toBe('$');
    expect(type).toBe('USD');
    expect(ratePerCny).toBeCloseTo(1 / RATE, 12);
  });

  it('CUSTOM：ratePerCny = 自定义汇率 / 汇率', () => {
    localStorage.setItem('quota_display_type', 'CUSTOM');
    localStorage.setItem(
      'status',
      JSON.stringify({
        usd_exchange_rate: RATE,
        custom_currency_symbol: 'Ξ',
        custom_currency_exchange_rate: 3.65,
      }),
    );
    const { symbol, ratePerCny, type } = getDisplayCurrency();
    expect(symbol).toBe('Ξ');
    expect(type).toBe('CUSTOM');
    // 1 美元 = 3.65 Ξ → 1 元 = 0.5 Ξ
    expect(ratePerCny).toBeCloseTo(0.5, 12);
  });

  it('TOKENS：不折算（ratePerCny 只作占位）', () => {
    localStorage.setItem('quota_display_type', 'TOKENS');
    const { type, ratePerCny } = getDisplayCurrency();
    expect(type).toBe('TOKENS');
    expect(ratePerCny).toBe(1 / RATE); // 金额不折算，调用方自行绕开
  });

  it('读不到 quota_per_yuan 时按 10⁹ 兜底', () => {
    localStorage.removeItem('quota_per_yuan');
    expect(getQuotaPerYuan()).toBe(QPY);
    expect(getQuotaPerYuan()).toBe(FALLBACK_QUOTA_PER_YUAN);
  });
});

describe('renderQuota：quota / quota_per_yuan × ratePerCny', () => {
  it('CNY：10⁹ quota = ¥1，1 quota = ¥0.000000001（9 位）', () => {
    localStorage.setItem('quota_display_type', 'CNY');
    expect(renderQuota(QPY, 9)).toBe('¥1.000000000');
    expect(renderQuota(1, 9)).toBe('¥0.000000001');
    expect(renderQuota(16583000, 9)).toBe('¥0.016583000');
  });

  it('USD：按汇率折回美元', () => {
    localStorage.setItem('quota_display_type', 'USD');
    expect(renderQuota(QPY, 9)).toBe('$0.136986301');
    // 余额这类大额仍是 2 位
    expect(renderQuota(QPY * 10, 2)).toBe('$1.37');
  });

  it('CUSTOM：自定义币种按 ratePerCny 折算', () => {
    localStorage.setItem('quota_display_type', 'CUSTOM');
    localStorage.setItem(
      'status',
      JSON.stringify({
        usd_exchange_rate: RATE,
        custom_currency_symbol: 'Ξ',
        custom_currency_exchange_rate: 3.65,
      }),
    );
    expect(renderQuota(QPY, 2)).toBe('Ξ0.50');
  });

  it('TOKENS：原样显示 quota', () => {
    localStorage.setItem('quota_display_type', 'TOKENS');
    expect(renderQuota(1234567, 2)).toBe('1.2M');
  });

  // G4：改汇率不再影响人民币金额与余额
  it('CNY 下改汇率，人民币金额一分不变', () => {
    localStorage.setItem('quota_display_type', 'CNY');
    const before = renderQuota(16583000, 9);
    localStorage.setItem('status', JSON.stringify({ usd_exchange_rate: 6.9 }));
    expect(renderQuota(16583000, 9)).toBe(before);
  });
});

describe('renderQuotaTrimmed：去掉金额末尾的 0', () => {
  it('CNY：紧凑显示且保留最多 9 位精度', () => {
    localStorage.setItem('quota_display_type', 'CNY');
    expect(renderQuotaTrimmed(1490)).toBe('¥0.00000149');
    expect(renderQuotaTrimmed(1)).toBe('¥0.000000001');
    expect(renderQuotaTrimmed(1000000000)).toBe('¥1');
    expect(renderQuotaTrimmed(0)).toBe('¥0');
  });

  it('TOKENS：沿用 renderNumber 显示 quota', () => {
    localStorage.setItem('quota_display_type', 'TOKENS');
    expect(renderQuotaTrimmed(1490)).toBe(renderNumber(1490));
  });
});

describe('getQuotaWithUnit / quotaToDisplayAmount：同一条链', () => {
  it('CNY：金额 = quota / quota_per_yuan', () => {
    localStorage.setItem('quota_display_type', 'CNY');
    expect(getQuotaWithUnit(QPY, 2)).toBe('1.00');
    expect(quotaToDisplayAmount(QPY)).toBe(1);
    expect(quotaToDisplayAmount(16583000)).toBeCloseTo(0.016583, 9);
  });

  it('USD：金额 = quota / quota_per_yuan / 汇率', () => {
    localStorage.setItem('quota_display_type', 'USD');
    expect(quotaToDisplayAmount(QPY)).toBeCloseTo(1 / RATE, 12);
  });

  it('反向：金额 → quota 与正向互逆', () => {
    localStorage.setItem('quota_display_type', 'CNY');
    expect(displayAmountToQuota(1)).toBe(QPY);
    expect(displayAmountToQuota(quotaToDisplayAmount(16583000))).toBe(16583000);
    localStorage.setItem('quota_display_type', 'USD');
    expect(displayAmountToQuota(quotaToDisplayAmount(QPY))).toBe(QPY);
  });
});
