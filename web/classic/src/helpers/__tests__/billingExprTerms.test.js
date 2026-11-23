import { describe, it, expect } from 'vitest';
import {
  exprUsesFX,
  extractTermCoefficients,
  scaleTermCoefficients,
  splitTopLevelTerms,
  termUsesFX,
} from '../billingExprTerms';

// 复验报告点出的四类写法：无空格、数字在前、链式乘法、标签里的 fx。
describe('billingExprTerms', () => {
  it('无空格也能判断 fx 归属', () => {
    const [t1, t2] = splitTopLevelTerms('p*1.5*fx+c*7.5');
    expect(t1).toBe('p*1.5*fx');
    expect(t2).toBe('+c*7.5');
    expect(termUsesFX(t1)).toBe(true);
    expect(termUsesFX(t2)).toBe(false);
  });

  it('数字在前的系数也能取到', () => {
    expect(extractTermCoefficients('1.5 * p')).toEqual([
      { varName: 'p', value: 1.5 },
    ]);
  });

  it('链式乘法只缩放一次', () => {
    expect(scaleTermCoefficients('1.5 * p * 2', 2)).toBe('p * 3 * 2');
  });

  it('档位标签里的 fx 不算引用', () => {
    expect(exprUsesFX('tier("fx-档", p * 10.2)')).toBe(false);
  });

  it('括号内的加减号不切分', () => {
    expect(
      splitTopLevelTerms('tier("a", p * 1 + c * 2) : tier("b", p * 3)'),
    ).toHaveLength(1);
  });

  it('指数写法不当作分隔符', () => {
    expect(splitTopLevelTerms('p * 1e-7 + c * 2')).toHaveLength(2);
  });
});
