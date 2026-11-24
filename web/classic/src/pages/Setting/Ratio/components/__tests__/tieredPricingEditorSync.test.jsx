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

  it('estimates using the live global rate and refuses to show a zero estimate without it', () => {
    localStorage.setItem('quota_display_type', 'CNY');
    localStorage.setItem('quota_per_yuan', '1000000000');
    const model = { name: 'm', billingExpr: 'tier("美元", p * fx)' };
    const props = {
      model,
      t,
      onExprChange: vi.fn(),
      onRequestRuleExprChange: vi.fn(),
    };
    const { rerender } = render(<TieredPricingEditor {...props} />);
    expect(screen.getByText('未设置全局模型源汇率，不可估算')).toBeTruthy();
    rerender(<TieredPricingEditor {...props} sourceRate='6.8' />);
    const first = screen.getByText(/^预计费用：/).textContent;
    expect(first).toContain('¥1.36');
    rerender(<TieredPricingEditor {...props} sourceRate='7' />);
    expect(screen.getByText(/^预计费用：/).textContent).not.toEqual(first);
    expect(props.onExprChange).not.toHaveBeenCalled();
  });
});
