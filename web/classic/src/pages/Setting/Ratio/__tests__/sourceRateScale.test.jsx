import { describe, it, expect } from 'vitest';
import {
  exprReferencesFX,
  scaleBillingExpr,
  scaleNumericString,
} from '../hooks/useModelPricingEditorState';

// 改汇率时的按比例缩放：运营只改汇率一个数字，系数由机器重算。
describe('scaleBillingExpr（改汇率）', () => {
  it('系数整体乘以比例（6.8 → 7.0）', () => {
    const before =
      'len <= 272000 ? tier("≤272K", p * 1.36 + c * 8.16 + cr * 0.136 + cc * 1.7 + cc1h * 3.4) : tier(">272K", p * 2.72 + c * 12.24 + cr * 0.272 + cc * 3.4 + cc1h * 6.8)';
    const got = scaleBillingExpr(before, 7 / 6.8);
    expect(got).toContain('p * 1.4 + c * 8.4');
    expect(got).toContain('cr * 0.14');
    expect(got).toContain('cc * 1.75');
    expect(got).toContain('p * 2.8 + c * 12.6');
    // 档位标签与条件一个字都不能动
    expect(got).toContain('tier("≤272K"');
    expect(got).toContain('tier(">272K"');
    expect(got).toContain('len <= 272000');
  });

  it('6.8 → 6.6（降价方向）', () => {
    const got = scaleBillingExpr('p * 1.36 + img_o * 204', 6.6 / 6.8);
    expect(got).toBe('p * 1.32 + img_o * 198');
  });

  it('空表达式原样返回', () => {
    expect(scaleBillingExpr('', 1.1)).toBe('');
    expect(scaleBillingExpr(undefined, 1.1)).toBeUndefined();
  });
});

describe('scaleNumericString（价格字段）', () => {
  it('按比例缩放并保持存储精度', () => {
    expect(scaleNumericString('8.16', 7 / 6.8)).toBe('8.4');
    expect(scaleNumericString('0.136', 7 / 6.8)).toBe('0.14');
  });

  it('空值与非数字原样返回', () => {
    expect(scaleNumericString('', 2)).toBe('');
    expect(scaleNumericString('.', 2)).toBe('.');
  });
});

// 引用 fx 的表达式：系数是美元价，汇率由 fx 承担 → 改汇率时**不得**缩放文本。
describe('exprReferencesFX', () => {
  it('识别 fx 引用', () => {
    expect(exprReferencesFX('tier("base", p * 1.5 * fx + c * 7.5 * fx)')).toBe(
      true,
    );
    expect(exprReferencesFX('tier("base", p * 1.36 + c * 8.16)')).toBe(false);
    expect(exprReferencesFX('')).toBe(false);
    expect(exprReferencesFX(null)).toBe(false);
  });

  it('fx 只作为完整标识符才算（不能误判 fxx / affx）', () => {
    expect(exprReferencesFX('tier("base", p * fxx)')).toBe(false);
    expect(exprReferencesFX('tier("base", p * affx)')).toBe(false);
  });

  it('档位标签里的 fx 不阻止换算', () => {
    expect(exprReferencesFX('tier("fx", p * 10.2)')).toBe(false);
  });
});

// 系数写在前面（`1.5 * p`）同样要缩放；比较里的数字不能碰。
describe('scaleBillingExpr 反向系数', () => {
  it('数字 * 变量 形式也缩放（统一归一成 变量 * 数字）', () => {
    expect(scaleBillingExpr('1.36 * p + 8.16 * c', 7 / 6.8)).toBe(
      'p * 1.4 + c * 8.4',
    );
  });

  it('链式乘法只缩放一次（1.5 * p * 2 不会被两套规则各乘一遍）', () => {
    const got = scaleBillingExpr('1.5 * p * 2', 2);
    expect(got).toBe('p * 3 * 2');
  });

  it('不动档位条件里的数字', () => {
    const got = scaleBillingExpr(
      'len <= 272000 ? tier("≤272K", 1.36 * p) : tier(">272K", 2.72 * p)',
      2,
    );
    expect(got).toContain('len <= 272000');
    expect(got).toContain('p * 5.44');
  });
});
