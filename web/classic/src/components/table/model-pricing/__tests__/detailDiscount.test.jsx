import React from 'react';
import { describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import ModelDetailSideSheet from '../modal/ModelDetailSideSheet';

describe('模型价格详情', () => {
  it('隐藏分组倍率，保留分档基础价、折扣和折后价格摘要', () => {
    render(
      <ModelDetailSideSheet
        visible
        modelData={{
          model_name: 'discounted',
          enable_groups: ['default'],
          billing_mode: 'tiered_expr',
          billing_expr: 'v2:tier("base", p * 5 * fx)',
          pricing_currency: 'USD',
          source_rate: 6.8,
        }}
        groupRatio={{ default: 1 }}
        groupModelRatio={{ default: { discounted: 0.5 } }}
        usableGroup={{ default: 'default' }}
        vendorsMap={{}}
        endpointMap={{}}
        t={(key) => key}
      />,
    );

    expect(screen.queryByText('分组倍率')).toBeNull();
    expect(screen.getByText('折扣')).toBeTruthy();
    expect(screen.getByText('5折')).toBeTruthy();
    expect(screen.getByText('$5.00')).toBeTruthy();
    expect(screen.getByText('输入价格 $2.50 / 1M Tokens')).toBeTruthy();
  });
});
