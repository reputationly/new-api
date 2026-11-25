import React from 'react';
import { beforeEach, describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { getLogsColumns } from '../UsageLogsColumnDefs';

const detailCell = (currency, pricingDetail) => {
  const column = getLogsColumns({
    t: (s, values) => (values ? s.replace('{{rate}}', values.rate) : s),
    COLUMN_KEYS: { DETAILS: 'details' },
  }).find((col) => col.key === 'details');
  return render(
    <div>
      {column.render('', {
        type: 2,
        other: JSON.stringify({
          billing_mode: 'tiered_expr',
          pricing_currency: currency,
          price_unit: 'cny',
          group_ratio: 0.85,
          user_group_ratio: 0.9,
          expr_b64: btoa('v2:tier("standard", p*1.5*fx + img_o*30*fx)'),
          expr_fx: 6.8,
          matched_tier: 'standard',
        }),
        pricing_detail: pricingDetail,
      })}
    </div>,
  );
};

describe('使用日志详情列的目录价', () => {
  beforeEach(() => {
    localStorage.setItem('quota_display_type', 'CNY');
  });

  it('USD 日志使用与展开详情及导出相同的目录价', () => {
    detailCell('USD', {
      currency: 'USD',
      rate: '6.8',
      catalog: [
        '输入价格：$1.50 / 1M tokens（≈ ¥10.20）',
        '图片输出价格：$30.00 / 1M tokens（≈ ¥204.00）',
      ],
    });
    expect(screen.getByText(/^分组倍率 0\.850*x$/)).toBeTruthy();
    expect(screen.getByText('输入价格：$1.50 / 1M tokens')).toBeTruthy();
    expect(screen.getByText('图片输出价格：$30.00 / 1M tokens')).toBeTruthy();
    expect(screen.getByText('换算汇率：1 USD = 6.8 CNY')).toBeTruthy();
    expect(screen.queryByText(/¥204/)).toBeNull();
  });

  it('USD 目录价无快照时不退回人民币单价', () => {
    detailCell('USD', {
      currency: 'USD',
      rate: '',
      catalog: ['图片输出价格：$30.00 / 1M tokens', '未记录当次换算汇率'],
    });
    expect(screen.getByText('图片输出价格：$30.00 / 1M tokens')).toBeTruthy();
    expect(screen.getByText('未记录当次换算汇率')).toBeTruthy();
    expect(screen.queryByText(/¥204/)).toBeNull();
  });

  it('旧日志继续展示人民币单价', () => {
    const { container } = detailCell('CNY');
    expect(container.textContent).toContain('¥204');
  });
});
