import { describe, it, expect } from 'vitest';
import { getGroupDiscountInfo, DISCOUNT_HEX } from '../discount';

/**
 * 折扣标签的展示口径。两端共用这一份，所以它错了就是两个端一起错。
 *
 * 入参是后端算好的 Final（分组倍率 × 模型折扣），与计费、日志同源——这里只负责
 * 把那个数翻译成人看的字，不做任何解析。
 */

describe('getGroupDiscountInfo', () => {
  it('整数折扣不拖小数点', () => {
    expect(getGroupDiscountInfo(0.5).text).toBe('5折');
    expect(getGroupDiscountInfo(0.9).text).toBe('9折');
  });

  it('非整数折扣保留一位', () => {
    expect(getGroupDiscountInfo(0.85).text).toBe('8.5折');
    expect(getGroupDiscountInfo(0.65).text).toBe('6.5折');
    expect(getGroupDiscountInfo(0.75).text).toBe('7.5折');
  });

  // 浮点误差：0.7 * 1000 在 IEEE754 下是 699.9999...，不做 round 会得到「6.9折」
  it('浮点误差不会把 7 折显示成 6.9 折', () => {
    expect(getGroupDiscountInfo(0.7).text).toBe('7折');
    expect(getGroupDiscountInfo(0.3).text).toBe('3折');
    expect(getGroupDiscountInfo(0.29).text).toBe('2.9折');
  });

  // 没打折就不该有标签。标个「10折」是纯噪音，而模型广场大部分模型都不打折
  it('倍率 >= 1 不显示标签', () => {
    expect(getGroupDiscountInfo(1)).toBeNull();
    expect(getGroupDiscountInfo(1.5)).toBeNull();
  });

  it('倍率异常时不显示，而不是显示错的', () => {
    expect(getGroupDiscountInfo(undefined)).toBeNull();
    expect(getGroupDiscountInfo(null)).toBeNull();
    expect(getGroupDiscountInfo(NaN)).toBeNull();
    expect(getGroupDiscountInfo('abc')).toBeNull();
    expect(getGroupDiscountInfo(-0.5)).toBeNull();
  });

  // free 分组倍率就是 0，显示「0折」没人看得懂
  it('倍率 0 显示为免费', () => {
    expect(getGroupDiscountInfo(0).text).toBe('免费');
  });

  it('按力度分三档配色', () => {
    expect(getGroupDiscountInfo(0.5).color).toBe('red');
    expect(getGroupDiscountInfo(0.7).color).toBe('red');
    expect(getGroupDiscountInfo(0.71).color).toBe('orange');
    expect(getGroupDiscountInfo(0.9).color).toBe('orange');
    expect(getGroupDiscountInfo(0.95).color).toBe('amber');
    expect(getGroupDiscountInfo(0.99).color).toBe('amber');
  });

  // 手机端没有 Semi 的 color token，靠这张表对齐两端颜色
  it('每个档位都有对应的手机端色值', () => {
    ['red', 'orange', 'amber'].forEach((c) => {
      expect(DISCOUNT_HEX[c]).toBeDefined();
      expect(DISCOUNT_HEX[c].bg).toMatch(/^#[0-9a-f]{6}$/i);
      expect(DISCOUNT_HEX[c].fg).toMatch(/^#[0-9a-f]{6}$/i);
    });
  });
});
