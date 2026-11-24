import React from 'react';
import { fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, it, expect, vi } from 'vitest';
import {
  exprFXMultiplier,
  exprUsesFX,
  parseTiersFromExpr,
  renderTieredModelPriceSimple,
} from '../../../../helpers/render';
import '../../../../i18n/i18n';
import { formatDynamicPriceSummary } from '../../../../helpers/utils';
import TieredPricingEditor from '../../../../pages/Setting/Ratio/components/TieredPricingEditor';
import DynamicPricingBreakdown from '../modal/components/DynamicPricingBreakdown';

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
      'p * 2',
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

describe('显式零价', () => {
  const t = (s) => s;

  beforeEach(() => {
    localStorage.setItem('quota_display_type', 'CNY');
    localStorage.setItem('quota_per_yuan', '1000000000');
  });

  it('区分零价输入与未定价的输出', () => {
    const expr = 'v2:tier("free", p * 0)';
    expect(parseTiersFromExpr(expr)[0]).toMatchObject({ inputPrice: 0 });
    expect(parseTiersFromExpr(expr)[0]).not.toHaveProperty('outputPrice');

    const { container } = render(formatDynamicPriceSummary(expr, t));
    expect(container.textContent).toContain('¥0.00');
    expect(container.textContent.match(/¥0\.00/g)).toHaveLength(1);
  });

  it('卡片与详情识别可视化编辑器生成的单档条件和零价兜底', () => {
    const onExprChange = vi.fn();
    const editor = render(
      <TieredPricingEditor
        model={{ name: 'm', billingExpr: 'tier("base", p * 2 + c * 0)' }}
        onExprChange={onExprChange}
        onRequestRuleExprChange={vi.fn()}
        t={t}
      />,
    );
    fireEvent.click(screen.getByText('添加条件'));
    expect(onExprChange).toHaveBeenCalledWith(
      'len < 200000 ? tier("base", p * 2 + c * 0) : p * 0 + c * 0',
    );
    const expr = `v2:${onExprChange.mock.lastCall[0]}`;
    editor.unmount();

    const card = render(formatDynamicPriceSummary(expr, t));
    expect(card.container.textContent).toContain('¥2.00');
    expect(card.container.textContent).toContain('¥0.00');
    expect(card.container.textContent).not.toContain('单价不可拆分');
    card.unmount();

    render(<DynamicPricingBreakdown billingExpr={expr} t={t} />);
    expect(screen.queryByText('单价不可拆分')).toBeNull();
    expect(screen.getByText('默认')).toBeTruthy();
    expect(screen.getByText('¥2.00')).toBeTruthy();
    expect(screen.getAllByText('¥0.00')).toHaveLength(3);
  });
});

describe('单引号档位标签', () => {
  const t = (s) => s;

  it('分档支持单引号及转义，标签内的标点不影响条件或请求规则', () => {
    const expr =
      String.raw`v2:(len < 1000 ? tier('it\'s "fx)"\\\u{6863}', p * 1.36) : tier("long", p * 2)) * (header("mode") == "fast" ? 2 : 1)`;
    expect(exprUsesFX(expr)).toBe(false);
    expect(parseTiersFromExpr(expr)).toMatchObject([
      { label: 'it\'s "fx)"\\档', inputPrice: 1.36 },
      { label: 'long', inputPrice: 2 },
    ]);
  });

  it('卡片、详情和用量日志显示同一档的单价', () => {
    localStorage.setItem('quota_display_type', 'CNY');
    const expr = String.raw`v2:tier('it\'s base', p * 1.36)`;
    const card = render(formatDynamicPriceSummary(expr, t));
    expect(card.container.textContent).toContain('¥1.36');
    expect(card.container.textContent).not.toContain('单价不可拆分');
    card.unmount();

    render(<DynamicPricingBreakdown billingExpr={expr} t={t} />);
    expect(screen.getByText("it's base")).toBeTruthy();
    expect(screen.getByText('¥1.36')).toBeTruthy();

    const segments = renderTieredModelPriceSimple({
      expr_b64: btoa(expr),
      matched_tier: "it's base",
      group_ratio: 1,
    });
    expect(segments.map(({ text }) => text).join(' ')).toContain('¥1.36');
  });
});
