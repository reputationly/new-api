import { describe, expect, it } from 'vitest';
import { formatEta, formatQueueHint } from '../queueHint';

// t 直通：这里测的是拼装与取舍，不是翻译。
const t = (s) => s;

describe('formatQueueHint', () => {
  it('说不准时返回 null，让调用方退回笼统文案', () => {
    // 门面在派发中 / 无运行实例 / 老版本不带字段时都回 null。编一个位置出来
    // 比不说更糟：用户会拿它当承诺。
    expect(formatQueueHint(null, 530, t)).toBeNull();
    expect(formatQueueHint(undefined, 530, t)).toBeNull();
  });

  it('0 不是「说不准」，是「马上开始」', () => {
    // 这两者在后端是不同的值（0 vs null），在页面上也必须是不同的话。
    expect(formatQueueHint(0, 0, t)).toBe('即将开始…');
  });

  it('报位置和区间', () => {
    expect(formatQueueHint(2, 530, t)).toBe(
      '前面还有 2 个任务 · 预计约 9–14 分钟后开始',
    );
  });

  it('没有 ETA 时只报位置，不硬编时间', () => {
    // 位置是精确的，时间不是。缺时间就只说精确的那半边。
    expect(formatQueueHint(3, null, t)).toBe('前面还有 3 个任务');
  });
});

describe('formatEta', () => {
  it('不足一分钟不报区间', () => {
    // 「约 0–1 分钟」既难看又没信息量。
    expect(formatEta(40, t)).toBe('预计 1 分钟内开始');
  });

  it('上界至少比下界大一分钟', () => {
    // 300s → lo=5，ceil(450/60)=8；但短时段四舍五入会把区间压平，
    // 比如 70s → lo=1, ceil(105/60)=2 —— 这里守的是不出现「约 5–5 分钟」。
    const s = formatEta(300, t);
    const [lo, hi] = s
      .match(/(\d+)–(\d+)/)
      .slice(1)
      .map(Number);
    expect(hi).toBeGreaterThan(lo);
  });

  it('非法输入不产出文案', () => {
    expect(formatEta(0, t)).toBeNull();
    expect(formatEta(null, t)).toBeNull();
    expect(formatEta(NaN, t)).toBeNull();
  });
});
