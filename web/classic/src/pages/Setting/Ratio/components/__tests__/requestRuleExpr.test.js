import { expect, it } from 'vitest';
import {
  combineBillingExpr,
  splitBillingExprAndRequestRules,
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
