import React from 'react';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import {
  normalizeUSDLogTimeRatios,
  renderLogPricingFX,
  renderModelPrice,
} from '../render';

const usdLog = {
  price_unit: 'cny',
  pricing_currency: 'USD',
  model_source_rate: 6.8,
};

describe('使用日志的当次汇率', () => {
  it('shows the frozen formula and uses only the expression FX snapshot', () => {
    const expression = 'v2:tier("base", p * 5 * fx)';
    const log = {
      ...usdLog,
      billing_mode: 'tiered_expr',
      expr_b64: btoa(expression),
    };
    const { rerender } = render(renderLogPricingFX({ ...log, expr_fx: 6.9 }));
    expect(screen.getByText(`计费表达式：${expression}`)).toBeTruthy();
    expect(screen.getByText('本次 fx = 6.9（1 USD = 6.9 CNY）')).toBeTruthy();
    rerender(renderLogPricingFX(log));
    expect(screen.queryByText(/本次 fx/)).toBeNull();
    rerender(renderLogPricingFX({ ...log, pricing_currency: 'CNY' }));
    expect(screen.queryByText(/计费表达式/)).toBeNull();
  });

  it('uses the original USD and converted CNY unit prices for token and call charges', () => {
    const log = {
      ...usdLog,
      model_ratio: 34,
      completion_ratio: 2,
      model_price: -1,
    };
    const { rerender } = render(renderLogPricingFX(log));
    expect(screen.getByText('本次换算：1 USD = 6.8 CNY')).toBeTruthy();
    expect(screen.getByText('输入单价：$5 → ¥34 / 1M tokens')).toBeTruthy();
    expect(screen.getByText('输出单价：$10 → ¥68 / 1M tokens')).toBeTruthy();
    rerender(renderLogPricingFX({ ...log, model_price: 1.36 }));
    expect(screen.getByText('按次单价：$0.2 → ¥1.36 / 次')).toBeTruthy();
    expect(screen.queryByText(/输入单价/)).toBeNull();
    rerender(renderLogPricingFX({ ...log, model_source_rate: undefined }));
    expect(screen.queryByText(/本次换算|输入单价/)).toBeNull();
  });

  it('shows matching window prices when the input and output window factors differ', () => {
    localStorage.setItem('quota_display_type', 'CNY');
    const log = {
      ...usdLog,
      model_ratio: 34,
      model_price: -1,
      completion_ratio: 1,
      group_ratio: 0.5,
      user_group_ratio: -1,
      time_factors: { input: 0.5, output: 0.25 },
      prompt_tokens: 1000,
      completion_tokens: 500,
    };
    render(
      <>
        {renderModelPrice(normalizeUSDLogTimeRatios(log))}
        {renderLogPricingFX(log)}
      </>,
    );
    expect(
      screen.getByText('时段输入单价：$2.5 → ¥17 / 1M tokens'),
    ).toBeTruthy();
    expect(
      screen.getByText('时段输出单价：$2.5 → ¥17 / 1M tokens'),
    ).toBeTruthy();
    expect(screen.getByText('输出价格：¥17 / 1M tokens')).toBeTruthy();
    expect(screen.getByText(/= ¥0\.0255/)).toBeTruthy();
  });

  it('uses the tool rate only for tool fields explicitly marked USD', () => {
    const log = {
      ...usdLog,
      pricing_currency: 'CNY',
      tool_source_rate: 7,
      web_search_price: 42,
      file_search_price: 5,
    };
    const { rerender } = render(renderLogPricingFX(log));
    expect(screen.queryByText(/本次换算|单价/)).toBeNull();
    rerender(
      renderLogPricingFX({
        ...log,
        usd_tool_price_fields: ['web_search_price'],
      }),
    );
    expect(screen.getByText('工具本次换算：1 USD = 7 CNY')).toBeTruthy();
    expect(screen.getByText('Web搜索单价：$6 → ¥42 / 1K 次')).toBeTruthy();
    expect(screen.queryByText(/文件搜索单价/)).toBeNull();
  });
});
