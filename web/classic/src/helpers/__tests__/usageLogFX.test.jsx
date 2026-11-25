import React from 'react';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { renderLoggedToolFX } from '../render';

describe('使用日志的当次汇率', () => {
  it('uses the tool rate only for tool fields explicitly marked USD', () => {
    const log = {
      price_unit: 'cny',
      pricing_currency: 'CNY',
      model_source_rate: 6.8,
      tool_source_rate: 7,
      web_search_price: 42,
      file_search_price: 5,
    };
    const { rerender } = render(renderLoggedToolFX(log));
    expect(screen.queryByText(/本次换算|单价/)).toBeNull();
    rerender(
      renderLoggedToolFX({
        ...log,
        usd_tool_price_fields: ['web_search_price'],
      }),
    );
    expect(screen.getByText('工具本次换算：1 USD = 7 CNY')).toBeTruthy();
    expect(screen.getByText('Web搜索单价：$6 → ¥42 / 1K 次')).toBeTruthy();
    expect(screen.queryByText(/文件搜索单价/)).toBeNull();
  });
});
