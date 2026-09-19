import { describe, it, expect } from 'vitest';
import {
  getTimeDiscountInfo,
  formatWindowDays,
  formatWindowRange,
  formatTimeUntil,
  crossesMidnight,
  formatWindowLine,
} from '../discount';

// 这几个函数被 classic 模型广场和 web/mobile **同时**使用（mobile 通过 vite 别名
// 直接 import 本文件）。改错了两端会一起错，或者更糟——只错一端，于是同一个模型
// 在手机和电脑上显示不同的折扣。

describe('getTimeDiscountInfo', () => {
  // 后端下发的 best_ratio / windows[].ratio 都是**最终倍率**，与列表价同口径。
  // 时段折扣取代模型折扣而非叠乘，所以这里只翻译文案，不做任何乘法。
  const windows = [
    {
      label: '深夜档',
      start: '00:00',
      end: '08:00',
      days: [],
      value: 0.56,
      ratio: 0.56,
      active: false,
    },
  ];

  it('未配置时不渲染任何时段 UI', () => {
    expect(getTimeDiscountInfo(null)).toBeNull();
    expect(getTimeDiscountInfo(undefined)).toBeNull();
    expect(getTimeDiscountInfo({})).toBeNull();
    expect(getTimeDiscountInfo({ windows: [] })).toBeNull();
  });

  it('空闲时段进行中：文案带档位名', () => {
    const info = getTimeDiscountInfo({
      active: true,
      best_ratio: 0.56,
      label: '深夜档',
      until: '2026-09-19T08:00:00+08:00',
      windows: [{ ...windows[0], active: true }],
    });
    expect(info.active).toBe(true);
    expect(info.text).toBe('深夜档进行中');
    expect(info.until).toBe('2026-09-19T08:00:00+08:00');
  });

  it('高峰时段显示绝对折扣，不是需要再乘一次的相对值', () => {
    const info = getTimeDiscountInfo({
      active: false,
      best_ratio: 0.56,
      until: '2026-09-20T00:00:00+08:00',
      windows,
    });
    expect(info.active).toBe(false);
    // 「空闲时段 5.6折」可以和常规的「8折」直接比较；相对说法「再7折」要求用户心算
    expect(info.text).toBe('空闲时段 5.6折');
    expect(info.text).not.toContain('再');
  });

  it('全天各档都不算折扣时不出角标', () => {
    expect(
      getTimeDiscountInfo({
        active: false,
        best_ratio: 1,
        windows: [{ ...windows[0], value: 1, ratio: 1 }],
      }),
    ).toBeNull();
    // 分组基础倍率 >1 时，时段终值也可能 >1——那不是折扣，不该标
    expect(
      getTimeDiscountInfo({
        active: false,
        best_ratio: 1.05,
        windows: [{ ...windows[0], ratio: 1.05 }],
      }),
    ).toBeNull();
  });

  it('best_ratio 缺失或非法时不炸', () => {
    expect(getTimeDiscountInfo({ active: false, windows })).toBeNull();
    expect(
      getTimeDiscountInfo({ active: false, best_ratio: 'x', windows }),
    ).toBeNull();
  });
});

describe('formatWindowDays', () => {
  it('把预设还原成管理端配置时选的那个词', () => {
    expect(formatWindowDays([])).toBe('每天');
    expect(formatWindowDays([1, 2, 3, 4, 5])).toBe('工作日');
    expect(formatWindowDays([0, 6])).toBe('周末');
    expect(formatWindowDays([0, 1, 2, 3, 4, 5, 6])).toBe('每天');
  });

  it('非预设组合逐日列出', () => {
    expect(formatWindowDays([1, 3])).toBe('周一、周三');
  });

  it('顺序与重复不影响结果', () => {
    expect(formatWindowDays([5, 1, 3, 2, 4, 1])).toBe('工作日');
  });
});

describe('formatWindowRange / crossesMidnight', () => {
  it('跨午夜要显式标出来', () => {
    expect(formatWindowRange({ start: '22:00', end: '06:00' })).toBe(
      '22:00-次日06:00',
    );
    expect(crossesMidnight({ start: '22:00', end: '06:00' })).toBe(true);
  });

  it('不跨午夜就是普通区间', () => {
    expect(formatWindowRange({ start: '00:00', end: '08:00' })).toBe(
      '00:00-08:00',
    );
    expect(crossesMidnight({ start: '00:00', end: '08:00' })).toBe(false);
  });

  // 按分钟数比较而不是字符串：'9:00' > '18:00' 在字符串下为真（'9' > '1'），
  // 会把一个普通白天时段渲染成跨午夜。后端 parseClock 已收严为必须补零，
  // 这条钉住的是这个共用纯函数本身不依赖那个上游不变量。
  it('小时未补零时不能误判成跨午夜', () => {
    expect(crossesMidnight({ start: '9:00', end: '18:00' })).toBe(false);
    expect(formatWindowRange({ start: '9:00', end: '18:00' })).toBe(
      '9:00-18:00',
    );
    expect(crossesMidnight({ start: '9:00', end: '6:00' })).toBe(true);
  });

  it('钟点缺失或畸形时按不跨午夜处理，不抛异常', () => {
    expect(crossesMidnight(null)).toBe(false);
    expect(crossesMidnight({ start: 'abc', end: '08:00' })).toBe(false);
    expect(formatWindowRange(null)).toBe('');
  });
});

describe('formatWindowLine', () => {
  // 角标读 best_ratio、tooltip 读每档的 ratio，两者必须同口径。用配置系数
  // （w.value）的话，分组基础倍率不为 1 或该用户有档位折扣时就会分叉——
  // tooltip 说「×0.56」而紧挨着的角标说「5.0折」。
  it('折扣取最终倍率，不是配置系数', () => {
    const line = formatWindowLine({
      days: [],
      start: '00:00',
      end: '08:00',
      value: 0.56,
      ratio: 0.504,
    });
    expect(line).toBe('每天 00:00-08:00 5.0折');
    expect(line).not.toContain('0.56');
  });

  it('倍率不构成折扣时退回裸倍率，不显示误导性的「折」', () => {
    expect(
      formatWindowLine({
        days: [1, 2, 3, 4, 5],
        start: '09:00',
        end: '18:00',
        ratio: 1.2,
      }),
    ).toBe('工作日 09:00-18:00 1.2x');
  });
});

describe('formatTimeUntil', () => {
  // 这条是防时区 bug 的：后端给的时刻带的是**时段模板自己的**时区偏移，
  // 一旦过 Date 就会被浏览器时区重新渲染，用户手机设成 UTC 时「至 08:00」
  // 会显示成「至 00:00」。所以必须是纯字符串截取。
  it('不受运行环境时区影响', () => {
    expect(formatTimeUntil('2026-09-19T08:00:00+08:00')).toBe('08:00');
    expect(formatTimeUntil('2026-09-19T08:00:00Z')).toBe('08:00');
  });

  it('非法输入返回空串而不是抛异常', () => {
    expect(formatTimeUntil(null)).toBe('');
    expect(formatTimeUntil('')).toBe('');
    expect(formatTimeUntil('not-a-time')).toBe('');
  });
});
