import React from 'react';
import { beforeEach, describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { getLogsColumns } from '../UsageLogsColumnDefs';

const detailCell = (currency, pricingDetail) => {
  const column = getLogsColumns({
    t: (s) => s,
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
      catalog: ['输入单价：$1.5 / 1M tokens', '图片输出单价：$30 / 1M tokens'],
    });
    expect(screen.getByText(/^分组倍率 0\.850*x$/)).toBeTruthy();
    expect(screen.getByText('输入单价：$1.5 / 1M tokens')).toBeTruthy();
    expect(screen.getByText('图片输出单价：$30 / 1M tokens')).toBeTruthy();
    expect(screen.queryByText(/¥204/)).toBeNull();
  });

  it('USD 目录价无快照时不退回人民币单价', () => {
    detailCell('USD', { currency: 'USD', rate: '', catalog: [] });
    expect(screen.getByText('缺少当次汇率快照，不回算')).toBeTruthy();
    expect(screen.queryByText(/¥204/)).toBeNull();
  });

  it('旧日志继续展示人民币单价', () => {
    const { container } = detailCell('CNY');
    expect(container.textContent).toContain('¥204');
  });
});
