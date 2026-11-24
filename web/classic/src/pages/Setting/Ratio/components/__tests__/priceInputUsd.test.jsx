import { describe, it, expect, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import PriceInput from '../PriceInput';

describe('PriceInput 美元录入模式', () => {
  it('默认仍是人民币：后缀 ¥/1M tokens，回调原样透传', () => {
    const onChange = vi.fn();
    render(<PriceInput label='输入价格' value='10.95' onChange={onChange} />);
    expect(screen.getByDisplayValue('10.95')).toBeTruthy();
    expect(screen.getByText('¥/1M tokens')).toBeTruthy();

    fireEvent.change(screen.getByDisplayValue('10.95'), {
      target: { value: '12' },
    });
    expect(onChange).toHaveBeenLastCalledWith('12');
  });

  it('USD 模型显示存储的美元原价', () => {
    render(
      <PriceInput
        label='输入价格'
        value='1.5'
        onChange={() => {}}
        sourceCurrency='USD'
      />,
    );
    expect(screen.getByDisplayValue('1.50')).toBeTruthy();
    expect(screen.getByText('$/1M tokens')).toBeTruthy();
  });

  it('键入美元价后回调美元原价', () => {
    const onChange = vi.fn();
    render(
      <PriceInput
        label='输出价格'
        value=''
        onChange={onChange}
        sourceCurrency='USD'
      />,
    );
    fireEvent.change(screen.getByRole('textbox'), {
      target: { value: '1.5' },
    });
    expect(onChange).toHaveBeenLastCalledWith('1.5');
  });

  it('按次价的后缀也跟着换成 $/次', () => {
    render(
      <PriceInput
        label='固定价格'
        value='30'
        onChange={() => {}}
        suffix='¥/次'
        sourceCurrency='USD'
      />,
    );
    expect(screen.getByDisplayValue('30.00')).toBeTruthy();
    expect(screen.getByText('$/次')).toBeTruthy();
  });

  it('显式 CNY 币种显示人民币价格', () => {
    render(
      <PriceInput
        label='输入价格'
        value='10'
        onChange={() => {}}
        sourceCurrency='CNY'
      />,
    );
    expect(screen.getByDisplayValue('10.00')).toBeTruthy();
    expect(screen.getByText('¥/1M tokens')).toBeTruthy();
  });
});

describe('PriceInput 美元录入：极低单价不被精度截断', () => {
  it('提交时保留美元原价精度', () => {
    const onChange = vi.fn();
    render(
      <PriceInput
        label='输入价格'
        value=''
        onChange={onChange}
        sourceCurrency='USD'
      />,
    );
    fireEvent.change(screen.getByRole('textbox'), {
      target: { value: '0.00000002' },
    });
    expect(onChange).toHaveBeenLastCalledWith('0.00000002');
  });

  it('显示侧也不被圆成 0.00', () => {
    render(
      <PriceInput
        label='输入价格'
        value='0.00000002'
        onChange={() => {}}
        sourceCurrency='USD'
      />,
    );
    expect(screen.getByDisplayValue('0.00000002')).toBeTruthy();
  });
});
