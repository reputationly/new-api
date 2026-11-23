import { describe, it, expect } from 'vitest';
import { toV2BillingExpr } from '../hooks/useModelPricingEditorState';

// 存储形态必须幂等：正文、带 v2:、带 v1: 都要收敛成「恰好一个 v2:」。
// 编辑框只收正文，但预设模板与文档里的表达式都带前缀，直接拼会变成 v2:v2:… 编译失败。
describe('toV2BillingExpr', () => {
  it('正文补 v2: 前缀', () => {
    expect(
      toV2BillingExpr('tier("base", p * 10.2 + c * 51 + img_o * 204)'),
    ).toBe('v2:tier("base", p * 10.2 + c * 51 + img_o * 204)');
  });

  it('已经带 v2: 时不再重复加（幂等）', () => {
    const expr = 'v2:tier("base", p * 10.2)';
    expect(toV2BillingExpr(expr)).toBe(expr);
  });

  it('历史 v1: 前缀会被换成 v2:', () => {
    expect(toV2BillingExpr('v1:tier("base", p * 2)')).toBe(
      'v2:tier("base", p * 2)',
    );
  });

  it('空表达式不产生野前缀', () => {
    expect(toV2BillingExpr('')).toBe('v2:');
    expect(toV2BillingExpr(null)).toBe('v2:');
  });
});
