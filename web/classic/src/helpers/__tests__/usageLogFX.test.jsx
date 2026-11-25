import React from 'react';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { renderLoggedToolFX, renderUsageLogCatalog } from '../render';

describe('使用日志的当次汇率', () => {
  it('在原有价格列表下显示汇率与参考提示，不显示算式', () => {
    const { container } = render(
      renderUsageLogCatalog(
        {
          catalog: [
            '命中档位：标准',
            '图片输出价格：$30.00 / 1M tokens（≈ ¥204.00）',
          ],
          rate: '6.8',
          tool_rate: '7',
        },
        (key, { rate }) => key.replace('{{rate}}', rate),
      ),
    );
    expect(
      Array.from(container.querySelectorAll('p'), (p) => p.textContent),
    ).toEqual([
      '命中档位：标准',
      '图片输出价格：$30.00 / 1M tokens（≈ ¥204.00）',
      '换算汇率：1 USD = 6.8 CNY',
      '工具换算汇率：1 USD = 7 CNY',
      '仅供参考，以实际扣费为准',
    ]);
  });

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
