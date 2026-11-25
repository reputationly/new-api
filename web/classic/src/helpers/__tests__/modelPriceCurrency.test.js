import { describe, expect, it } from 'vitest';
import { calculateModelPrice } from '../utils';

describe('model plaza currency', () => {
  const calculate = (record) =>
    calculateModelPrice({
      record,
      selectedGroup: 'default',
      groupRatio: { default: 0.5 },
    });

  it('shows per-call and token prices in the currency of the model', () => {
    const usd = { pricing_currency: 'USD', source_rate: 6.8 };
    const cny = { pricing_currency: 'CNY' };
    expect(calculate({ ...usd, quota_type: 1, model_price: 34 }).price).toBe(
      '$2.50',
    );
    expect(calculate({ ...cny, quota_type: 1, model_price: 34 }).price).toBe(
      '¥17.00',
    );
    expect(
      calculate({ ...usd, quota_type: 0, model_ratio: 34, completion_ratio: 1 })
        .inputPrice,
    ).toBe('$2.50');
  });

  it('does not display a zero price for a USD model without FX', () => {
    expect(
      calculate({ pricing_currency: 'USD', quota_type: 1, model_price: 0 }),
    ).toEqual({ priceUnavailable: true });
  });
});
