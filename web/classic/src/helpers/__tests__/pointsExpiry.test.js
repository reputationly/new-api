import { describe, it, expect } from 'vitest';

import {
  formatPointsExpiryDate,
  formatPointsExpiryFullDate,
  pickExpiringHighlights,
} from '../pointsExpiry';

/**
 * 到期日必须按**北京时间**展示（v2.5 §10.2）。
 *
 * 到期时刻固定是北京时间 23:59:59：用浏览器时区格式化时，东京（UTC+9）会把
 * 「12-20 23:59:59 CST」显示成 12-21 —— 用户看到的过期日比实际晚一天；反过来
 * 发放日在西半球浏览器上又会早一天。服务端所有判定都按北京时间，展示必须一致。
 */

// 北京时间某天某时刻对应的 unix 秒（+08:00 固定偏移，无夏令时）
const bj = (y, m, d, hh, mm, ss) =>
  Date.UTC(y, m - 1, d, hh - 8, mm, ss) / 1000;

describe('formatPointsExpiryDate', () => {
  it('按北京时间取日期，而不是 UTC 日期', () => {
    // 北京时间 2026-12-20 00:30 = UTC 2026-12-19 16:30
    // 用 UTC 格式化会得到 12-19，这里必须是 12-20
    expect(formatPointsExpiryDate(bj(2026, 12, 20, 0, 30, 0))).toBe('12-20');
  });

  it('北京时间 23:59:59 仍是当天（东九区浏览器不会跳到次日）', () => {
    const ts = bj(2026, 12, 20, 23, 59, 59);
    expect(formatPointsExpiryDate(ts)).toBe('12-20');
    expect(formatPointsExpiryFullDate(ts)).toBe('2026-12-20');
  });

  it('0 表示永不过期，返回空串', () => {
    expect(formatPointsExpiryDate(0)).toBe('');
    expect(formatPointsExpiryDate(null)).toBe('');
    expect(formatPointsExpiryFullDate(0)).toBe('');
  });

  it('完整日期含年份，跨年正确', () => {
    expect(formatPointsExpiryFullDate(bj(2027, 1, 1, 23, 59, 59))).toBe(
      '2027-01-01',
    );
  });
});

/**
 * Q2（二期第 1 轮验收）：Tag 的数量与日期必须同源。
 *
 * 反例就是验收单里那个：12-20 到期 10、01-15 到期 500。旧实现用「30 天合计 510」
 * 配「最早日期 12-20」，读出来是「这 510 积分都会在 12-20 过期」。
 */
describe('pickExpiringHighlights', () => {
  const summary = {
    expiring: { expires_at: bj(2026, 12, 20, 23, 59, 59), quota: 510 },
    nextExpiries: [
      { expires_at: bj(2027, 1, 15, 23, 59, 59), quota: 500 },
      { expires_at: bj(2026, 12, 20, 23, 59, 59), quota: 10 },
    ],
  };

  it('Tag 取最近一个到期日与当天到期的量，不是 30 天合计', () => {
    const { nearest } = pickExpiringHighlights(summary);
    expect(nearest.quota).toBe(10);
    expect(formatPointsExpiryDate(nearest.expires_at)).toBe('12-20');
  });

  it('Tooltip 按到期时间升序逐行列出，最多 3 行', () => {
    const { lines } = pickExpiringHighlights(summary);
    expect(lines.map((e) => formatPointsExpiryDate(e.expires_at))).toEqual([
      '12-20',
      '01-15',
    ]);

    const four = {
      expiring: { quota: 0 },
      nextExpiries: [1, 2, 3, 4].map((d) => ({
        expires_at: bj(2026, 12, d, 23, 59, 59),
        quota: d,
      })),
    };
    expect(pickExpiringHighlights(four).lines).toHaveLength(3);
  });

  it('30 天合计仍然单独给出（Tooltip 里显示）', () => {
    expect(pickExpiringHighlights(summary).total30d).toBe(510);
  });

  it('空输入不炸', () => {
    expect(pickExpiringHighlights(null).nearest).toBeNull();
    expect(pickExpiringHighlights({}).lines).toEqual([]);
    expect(pickExpiringHighlights({ nextExpiries: [] }).total30d).toBe(0);
  });
});
