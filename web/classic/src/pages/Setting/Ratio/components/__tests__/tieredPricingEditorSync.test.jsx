import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import TieredPricingEditor from '../TieredPricingEditor';

const t = (key) => key;

describe('TieredPricingEditor', () => {
  it('does not publish defaults or revert an external rate adjustment', () => {
    const onExprChange = vi.fn();
    const props = { t, onExprChange, onRequestRuleExprChange: vi.fn() };
    const original = 'tier("base", 10.2 * p + c * 6.8)';
    const adjusted = 'tier("base", p * 10.5 + c * 7)';
    const { rerender } = render(
      <TieredPricingEditor
        {...props}
        model={{ name: 'm', billingExpr: original }}
      />,
    );
    expect(screen.getByDisplayValue(original)).toBeTruthy();
    rerender(
      <TieredPricingEditor
        {...props}
        model={{ name: 'm', billingExpr: adjusted }}
      />,
    );
    expect(screen.queryByDisplayValue(original)).toBeNull();
    expect(onExprChange).not.toHaveBeenCalled();
  });
});
