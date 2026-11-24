import { expect, it } from 'vitest';
import {
  combineBillingExpr,
  splitBillingExprAndRequestRules,
  tryParseRequestRuleExpr,
} from '../requestRuleExpr';

it('不会把三元表达式分支上的倍率当成全局请求规则', () => {
  const base = "len < 1000 ? tier('short)', p * 2) : tier('long)', p * 3)";
  const factor = '(header("mode") == "fast" ? 2 : 1)';
  const expr = base + ' * ' + factor;
  const split = splitBillingExprAndRequestRules(expr);
  expect(split).toEqual({ billingExpr: expr, requestRuleExpr: '' });
  expect(combineBillingExpr(split.billingExpr, split.requestRuleExpr)).toBe(expr);

  const combined = '(' + base + ') * ' + factor;
  expect(splitBillingExprAndRequestRules(combined)).toEqual({
    billingExpr: base,
    requestRuleExpr: factor,
  });
});

it('可视化规则不能解析 expr-lang 转义时保留原式', () => {
  const factor = String.raw`(has(header("mode"), "\x2f") ? 2 : 1)`;
  const expr = 'tier("base)", p * 2) * ' + factor;
  expect(tryParseRequestRuleExpr(factor)).toBeNull();
  const split = splitBillingExprAndRequestRules(expr);
  expect(split).toEqual({ billingExpr: expr, requestRuleExpr: '' });
  expect(combineBillingExpr(split.billingExpr, split.requestRuleExpr)).toBe(expr);
});
