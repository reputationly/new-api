import { describe, it, expect, beforeEach } from 'vitest';
import { buildWarningThresholdPresets } from '../NotificationSettings';

/**
 * F1：提醒阈值预设不再写死 quota 整数（100000 → "0.2$" 这种），而是按人民币面额
 * ¥1 / ¥10 / ¥100 现算：quota = 人民币 ÷ 汇率 × quota_per_unit，标签跟随当前展示币种。
 *
 * 写死的问题在换算率一变就暴露：QuotaPerUnit 500000 → 7300000 后，100000 这个数
 * 从 ¥0.2 变成 ¥0.0146，而标签还印着 "0.2$"。
 */

const rate = 7.3;

beforeEach(() => {
  localStorage.clear();
});

describe('buildWarningThresholdPresets', () => {
  it('500000：¥1 / ¥10 / ¥100 三档按旧换算率还原成原值量级', () => {
    localStorage.setItem('quota_per_unit', '500000');
    localStorage.setItem('quota_display_type', 'CNY');
    localStorage.setItem('status', JSON.stringify({ usd_exchange_rate: rate }));

    const presets = buildWarningThresholdPresets();
    expect(presets.map((p) => p.value)).toEqual([68493, 684932, 6849315]);
    expect(presets.map((p) => p.label)).toEqual(['¥1.00', '¥10.00', '¥100.00']);
  });

  it('7300000：同样的三档人民币金额换成 1 quota = ¥0.000001', () => {
    localStorage.setItem('quota_per_unit', '7300000');
    localStorage.setItem('quota_display_type', 'CNY');
    localStorage.setItem('status', JSON.stringify({ usd_exchange_rate: rate }));

    const presets = buildWarningThresholdPresets();
    expect(presets.map((p) => p.value)).toEqual([1000000, 10000000, 100000000]);
    expect(presets.map((p) => p.label)).toEqual(['¥1.00', '¥10.00', '¥100.00']);
  });

  it('USD 展示时标签用美元符号，不再是 "1$" 这种混排', () => {
    localStorage.setItem('quota_per_unit', '7300000');
    localStorage.setItem('quota_display_type', 'USD');

    const presets = buildWarningThresholdPresets();
    expect(presets.map((p) => p.label)).toEqual(['$0.14', '$1.37', '$13.70']);
  });

  it('status 里没有汇率时按 7.3 兜底', () => {
    localStorage.setItem('quota_per_unit', '7300000');
    localStorage.setItem('quota_display_type', 'CNY');

    const presets = buildWarningThresholdPresets();
    expect(presets.map((p) => p.value)).toEqual([1000000, 10000000, 100000000]);
  });
});
