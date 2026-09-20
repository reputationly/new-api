import { describe, it, expect } from 'vitest';
import {
  formatWindowDays,
  formatWindowRange,
  crossesMidnight,
} from '../discount';

// 这几个函数被 classic 模型广场和 web/mobile **同时**使用（mobile 通过 vite 别名
// 直接 import 本文件）。改错了两端会一起错，或者更糟——只错一端，于是同一个模型
// 在手机和电脑上显示不同的折扣。

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
