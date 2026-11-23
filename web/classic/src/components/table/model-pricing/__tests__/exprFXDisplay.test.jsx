import { describe, it, expect } from 'vitest';
import {
  exprFXMultiplier,
  exprUsesFX,
  parseTiersFromExpr,
} from '../../../../helpers/render';

// 表达式引用 fx 时，系数是美元目录价：展示必须乘该模型汇率，才与实扣一致。
describe('表达式 fx 展示口径', () => {
  const usdExpr =
    'v2:tier("base", p * 1.5 * fx + c * 7.5 * fx + img_o * 30 * fx)';

  it('识别 fx', () => {
    expect(exprUsesFX(usdExpr)).toBe(true);
    expect(exprUsesFX('v2:tier("base", p * 10.2)')).toBe(false);
  });

  it('乘汇率后等于人民币单价', () => {
    expect(exprFXMultiplier(usdExpr, 6.8)).toBe(6.8);
    expect(exprFXMultiplier('v2:tier("base", p * 10.2)', 6.8)).toBe(1);
  });

  it('引用 fx 但汇率缺失时返回 0（宁可显示成错的，也不显示"像对的"）', () => {
    expect(exprFXMultiplier(usdExpr, 0)).toBe(0);
    expect(exprFXMultiplier(usdExpr, undefined)).toBe(0);
  });

  it('parseTiersFromExpr 按汇率展开系数', () => {
    const tiers = parseTiersFromExpr(usdExpr, 6.8);
    expect(tiers).toHaveLength(1);
    expect(tiers[0].inputPrice).toBeCloseTo(10.2, 6);
    expect(tiers[0].outputPrice).toBeCloseTo(51, 6);
    expect(tiers[0].imageOutputPrice).toBeCloseTo(204, 6);
  });

  it('未引用 fx 的老表达式乘 1（系数本来就是人民币）', () => {
    const tiers = parseTiersFromExpr('v2:tier("base", p * 10.2)', 6.8);
    expect(tiers[0].inputPrice).toBeCloseTo(10.2, 6);
  });
});
