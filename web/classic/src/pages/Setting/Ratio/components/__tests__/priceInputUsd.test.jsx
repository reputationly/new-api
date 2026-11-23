import { describe, it, expect, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import PriceInput from '../PriceInput';

// 美元录入模式是纯展示层换算：输入框按 $显示/输入，回调出去的值必须是人民币，
// 否则序列化（ModelRatio = ¥/百万 tokens）会直接被除错一个汇率。
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

  it('usdRate=6.8 时显示的是美元价，并标出落库的人民币值', () => {
    render(
      <PriceInput
        label='输入价格'
        value='10.2'
        onChange={() => {}}
        usdRate={6.8}
      />,
    );
    // ¥10.2 ÷ 6.8 = $1.50
    expect(screen.getByDisplayValue('1.50')).toBeTruthy();
    expect(screen.getByText('$/1M tokens')).toBeTruthy();
    expect(screen.getByText('= ¥10.20/1M tokens')).toBeTruthy();
  });

  it('键入美元价后回调的是人民币（含浮点收敛）', () => {
    const onChange = vi.fn();
    render(
      <PriceInput
        label='输出价格'
        value=''
        onChange={onChange}
        usdRate={6.8}
      />,
    );
    fireEvent.change(screen.getByRole('textbox'), {
      target: { value: '1.5' },
    });
    // 1.5 × 6.8 = 10.200000000000001，必须收敛成 10.2
    expect(onChange).toHaveBeenLastCalledWith('10.2');
  });

  it('按次价的后缀也跟着换成 $/次', () => {
    render(
      <PriceInput
        label='固定价格'
        value='204'
        onChange={() => {}}
        suffix='¥/次'
        usdRate={6.8}
      />,
    );
    expect(screen.getByDisplayValue('30.00')).toBeTruthy();
    expect(screen.getByText('$/次')).toBeTruthy();
    expect(screen.getByText('= ¥204.00/次')).toBeTruthy();
  });

  it('usdRate 非法时退回人民币模式', () => {
    render(
      <PriceInput
        label='输入价格'
        value='10'
        onChange={() => {}}
        usdRate={0}
      />,
    );
    expect(screen.getByDisplayValue('10.00')).toBeTruthy();
    expect(screen.getByText('¥/1M tokens')).toBeTruthy();
  });
});
