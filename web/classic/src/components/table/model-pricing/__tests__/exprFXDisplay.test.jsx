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

// 混合写法：只有带 `* fx` 的那一项是美元价，其它项必须保持原样。
describe('fx 按项生效（混合写法）', () => {
  it('只有 p 用 fx 时，输出价不被乘', () => {
    const tiers = parseTiersFromExpr(
      'v2:tier("base", p * 1.5 * fx + c * 7.5)',
      6.8,
    );
    expect(tiers[0].inputPrice).toBeCloseTo(10.2, 6);
    expect(tiers[0].outputPrice).toBeCloseTo(7.5, 6);
  });

  it('档位标签里出现 fx 不算引用', () => {
    expect(exprUsesFX('v2:tier("fx-档", p * 10.2)')).toBe(false);
    const tiers = parseTiersFromExpr('v2:tier("fx-档", p * 10.2)', 6.8);
    expect(tiers[0].inputPrice).toBeCloseTo(10.2, 6);
  });

  it('引用 fx 但汇率缺失时不显示猜测的单价', () => {
    const tiers = parseTiersFromExpr(
      'v2:tier("base", p * 1.5 * fx + c * 7.5)',
      0,
    );
    expect(tiers[0]).toMatchObject({ label: 'base', indivisible: true });
    expect(tiers[0]).not.toHaveProperty('inputPrice');
  });

  it('括号和重复变量按完整数值计算', () => {
    expect(
      parseTiersFromExpr(
        'v2:tier("base", p * (1.5 * fx) + c * 7.5 - 0.2 * p)',
        6.8,
      )[0],
    ).toMatchObject({ inputPrice: 10, outputPrice: 7.5 });
  });

  it('非线性、固定加价和档位外倍率不展示拆分单价', () => {
    for (const expression of [
      'tier("base", p * c)',
      'tier("base", p * 2 + 1)',
      'tier("base", p * 2) * 3',
    ]) {
      expect(parseTiersFromExpr(expression)[0]).toMatchObject({
        indivisible: true,
      });
    }
  });

  it('用量日志仍能展示带独立请求倍率的分档目录价', () => {
    const tiers = parseTiersFromExpr(
      'v2:(len <= 2000 ? tier("short", p * 2) : tier("long", p * 3)) * (header("mode") == "fast" ? 2 : 1)',
    );
    expect(tiers.map((tier) => [tier.label, tier.inputPrice])).toEqual([
      ['short', 2],
      ['long', 3],
    ]);
  });
});
