import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import {
  validateDashboardRange,
  getGranularityForRange,
  getPresetRange,
  formatRangeSummary,
  parseDashboardTimestamp,
} from '../dashboard';
import { timestamp2string } from '../utils';

// 看板时间条件的口径：预设决定「查哪一段」，粒度只决定「横轴怎么分桶」，
// 校验则保证弹窗与后端两个上限一致（90 天）。这三件事在 UI 上很容易被混为一谈，
// 所以把它们钉在测试里。

const NOW = new Date('2026-01-15T10:30:00');

describe('看板时间范围', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('空时间被拒绝，而不是被解析成 0 去扫全表', () => {
    expect(validateDashboardRange('', '2026-01-15 10:30:00')).toBe(
      '请选择起始时间和结束时间',
    );
    expect(validateDashboardRange('2026-01-15 10:30:00', '')).toBe(
      '请选择起始时间和结束时间',
    );
  });

  it('结束不晚于起始被拒绝', () => {
    expect(
      validateDashboardRange('2026-01-15 10:00:00', '2026-01-15 10:00:00'),
    ).toBe('结束时间必须晚于起始时间');
  });

  it('90 天放行，超过 1 秒拒绝（与后端上限一致）', () => {
    const end = '2026-01-15 10:30:00';
    const exactly90 = timestamp2string(NOW.getTime() / 1000 - 90 * 86400);
    const over90 = timestamp2string(NOW.getTime() / 1000 - 90 * 86400 - 1);
    expect(validateDashboardRange(exactly90, end)).toBe('');
    expect(validateDashboardRange(over90, end)).toBe('时间跨度不能超过 90 天');
  });

  it('按跨度给粒度，避免「30 天按小时」刷出 720 个点', () => {
    expect(getGranularityForRange(0, 3600)).toBe('hour');
    expect(getGranularityForRange(0, 48 * 3600)).toBe('hour');
    expect(getGranularityForRange(0, 49 * 3600)).toBe('day');
    expect(getGranularityForRange(0, 31 * 86400)).toBe('day');
    expect(getGranularityForRange(0, 32 * 86400)).toBe('week');
  });

  it('上个月按日历月算，且跨年时取到 12 月', () => {
    const range = getPresetRange('lastMonth');
    expect(range.start_timestamp).toBe('2025-12-01 00:00:00');
    expect(range.end_timestamp).toBe('2025-12-31 23:59:59');
    expect(range.granularity).toBe('day');
    // 31 天的月份必须能通过校验：这正是后端把上限从「1 个月」放宽到 90 天的原因
    expect(
      validateDashboardRange(range.start_timestamp, range.end_timestamp),
    ).toBe('');
  });

  it('昨天是完整的一天，不是「最近 24 小时」', () => {
    const range = getPresetRange('yesterday');
    expect(range.start_timestamp).toBe('2026-01-14 00:00:00');
    expect(range.end_timestamp).toBe('2026-01-14 23:59:59');
  });

  it('兼容 Safari：非标准时间串也能解析', () => {
    // 直接把毫秒当秒用的错误会让区间变成 1970 年，最终返回全表数据
    expect(parseDashboardTimestamp('2026-01-15 10:30:00')).toBe(
      NOW.getTime() / 1000,
    );
  });

  it('范围摘要展示天数和粒度', () => {
    const summary = formatRangeSummary(
      '2026-01-09 10:30:00',
      '2026-01-15 10:30:00',
      'day',
    );
    expect(summary).toContain('6 天');
    expect(summary).toContain('01-09 10:30');
    expect(summary).toContain('按天');
  });
});
