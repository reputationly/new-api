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

it('只提取整个 v2 表达式的乘数，忽略容器和档名中的运算符', () => {
  const base =
    'v2:{base: [len > 10 ? tier("base)?:*", p * 2) : p + 1]}["base"][0]';
  const factor = '(header("mode") == "fast" ? 2 : 1)';
  const expected = {
    billingExpr: base,
    requestRuleExpr: factor,
  };
  expect(splitBillingExprAndRequestRules(base + ' * ' + factor)).toEqual(expected);
  const combined = combineBillingExpr(base, factor);
  expect(combined).toBe('v2:(' + base.slice(3) + ') * ' + factor);
  expect(splitBillingExprAndRequestRules(combined)).toEqual(expected);
});

it.each(['+', '-'])('保留含顶层 %s 的原式，不把分项乘数提取为整价规则', (operator) => {
  const expr =
    'v2:{normal: 4}["normal"] ' +
    operator +
    ' tier("base", p * 2) * (header("mode") == "fast" ? 2 : 1)';
  expect(splitBillingExprAndRequestRules(expr)).toEqual({
    billingExpr: expr,
    requestRuleExpr: '',
  });
});
