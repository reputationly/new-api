import { describe, it, expect, beforeEach } from 'vitest';
import { buildWarningThresholdPresets } from '../NotificationSettings';

/**
 * F1：提醒阈值预设不再写死 quota 整数（100000 → "0.2$" 这种），而是按人民币面额
 * ¥1 / ¥10 / ¥100 现算：quota = 人民币 × quota_per_yuan，标签跟随当前展示币种。
 *
 * 写死的问题在换算率一变就暴露：quota_per_yuan 一变，同一个整数就对应另一个人民币
 * 金额，而标签还印着旧值。
 */

beforeEach(() => {
  localStorage.clear();
});

describe('buildWarningThresholdPresets', () => {
  it('1 quota = ¥0.00000001：¥1 / ¥10 / ¥100 三档', () => {
    localStorage.setItem('quota_per_yuan', '100000000');
    localStorage.setItem('quota_display_type', 'CNY');

    const presets = buildWarningThresholdPresets();
    expect(presets.map((p) => p.value)).toEqual([
      100000000, 1000000000, 10000000000,
    ]);
    expect(presets.map((p) => p.label)).toEqual(['¥1.00', '¥10.00', '¥100.00']);
  });

  it('细化前的旧单位（1 quota = ¥0.000001）同样按面额还原', () => {
    localStorage.setItem('quota_per_yuan', '1000000');
    localStorage.setItem('quota_display_type', 'CNY');

    const presets = buildWarningThresholdPresets();
    expect(presets.map((p) => p.value)).toEqual([1000000, 10000000, 100000000]);
    expect(presets.map((p) => p.label)).toEqual(['¥1.00', '¥10.00', '¥100.00']);
  });

  it('USD 展示时标签用美元符号，不再是 "1$" 这种混排', () => {
    localStorage.setItem('quota_per_yuan', '100000000');
    localStorage.setItem('quota_display_type', 'USD');
    localStorage.setItem('status', JSON.stringify({ usd_exchange_rate: 7.3 }));

    const presets = buildWarningThresholdPresets();
    expect(presets.map((p) => p.label)).toEqual(['$0.14', '$1.37', '$13.70']);
  });

  it('读不到 quota_per_yuan 时按 10⁸ 兜底', () => {
    localStorage.setItem('quota_display_type', 'CNY');

    const presets = buildWarningThresholdPresets();
    expect(presets.map((p) => p.value)).toEqual([
      100000000, 1000000000, 10000000000,
    ]);
  });
});
