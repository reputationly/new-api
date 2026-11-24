import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { API } from '../../../../helpers';
import { Modal } from '@douyinfe/semi-ui';
import ModelSourceFXCard from '../components/ModelSourceFXCard';
import ToolPriceSettings from '../ToolPriceSettings';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (s, values) => s.replace('{{date}}', values?.date),
  }),
}));
vi.mock('../../../../helpers', () => ({
  API: { get: vi.fn(), post: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

it('shows reference stats and a fetch failure and saves only the configured rate', async () => {
  const response = {
    from: 1792857600,
    through: 1795449599,
    quotes: [
      { date: '2026-11-20', rate: 6.6 },
      { date: '2026-11-23', rate: 6.8 },
    ],
    baseline: null,
    changes: [],
    last_change: null,
    current_rate: '7',
    status: { error: 'HTTP 503', last_success: 1795400000000 },
    today: '2026-11-23',
    latest_date: '2026-11-23',
  };
  API.get.mockResolvedValue({ data: { success: true, data: response } });
  API.post.mockImplementation(async () => {
    response.current_rate = '7.1';
    return { data: { success: true } };
  });

  render(<ModelSourceFXCard configuredRate='7' />);
  expect(await screen.findByText(/最新中间价：6.8000/)).toBeTruthy();
  expect(screen.getByText('均值：6.7000')).toBeTruthy();
  expect(screen.getByText(/CFETS 请求失败：HTTP 503/)).toBeTruthy();
  expect(screen.queryByText('最新中间价 11/23')).toBeNull();
  expect(screen.queryByRole('button', { name: /采用参考汇率/ })).toBeNull();

  fireEvent.change(screen.getByRole('textbox', { name: '全局模型源汇率' }), {
    target: { value: '7.1' },
  });
  fireEvent.click(screen.getByRole('button', { name: '保存全局汇率' }));
  await waitFor(() =>
    expect(API.post).toHaveBeenCalledWith('/api/model_pricing/options', {
      options: { ModelSourceRate: '7.1' },
    }),
  );
  expect(await screen.findByText('当前生效汇率：7.1 ¥/$')).toBeTruthy();
});

it('shows an older CFETS quote as a date note, not a warning', async () => {
  API.get.mockResolvedValue({
    data: {
      success: true,
      data: {
        from: 1758240000,
        through: 1759564799,
        today: '2025-10-04',
        latest_date: '2025-09-30',
        quotes: [{ date: '2025-09-30', rate: 7.1 }],
        changes: [],
        status: { error: '' },
      },
    },
  });
  render(<ModelSourceFXCard configuredRate='7' />);
  expect(await screen.findByText('最新中间价 09/30')).toBeTruthy();
  expect(screen.queryByText(/CFETS 请求失败|CFETS 数据未更新/)).toBeNull();
});

it('requires confirmation before saving a big change and publishes the saved rate', async () => {
  API.post.mockClear();
  API.get.mockResolvedValue({
    data: {
      success: true,
      data: { current_rate: '7', quotes: [], changes: [] },
    },
  });
  const confirm = vi.spyOn(Modal, 'confirm').mockImplementation(() => {});
  const onRateChange = vi.fn();
  try {
    render(
      <ModelSourceFXCard configuredRate='7' onRateChange={onRateChange} />,
    );
    await waitFor(() => expect(onRateChange).toHaveBeenCalledWith('7'));
    API.post.mockImplementation(async () => {
      API.get.mockResolvedValue({
        data: {
          success: true,
          data: { current_rate: '8', quotes: [], changes: [] },
        },
      });
      return { data: { success: true } };
    });
    const input = screen.getByRole('textbox', { name: '全局模型源汇率' });
    fireEvent.change(input, { target: { value: '8' } });
    fireEvent.click(screen.getByRole('button', { name: '保存全局汇率' }));
    expect(confirm).toHaveBeenCalledTimes(1);
    expect(API.post).not.toHaveBeenCalled();
    expect(input.disabled).toBe(true);
    await confirm.mock.calls[0][0].onOk();
    expect(API.post).toHaveBeenCalledTimes(1);
    expect(onRateChange).toHaveBeenCalledWith('8');
  } finally {
    confirm.mockRestore();
  }
});

it('keeps valid custom tool prices visible and blocks saves when currency JSON is invalid', () => {
  render(
    <ToolPriceSettings
      options={{
        'tool_price_setting.prices': '{"custom-search":23}',
        'tool_price_setting.currencies': '{broken',
      }}
    />,
  );
  expect(screen.getByDisplayValue('custom-search')).toBeTruthy();
  expect(
    screen.getByText('已保存的工具价格或币种配置无效，不能覆盖保存。'),
  ).toBeTruthy();
  expect(screen.getByRole('button', { name: '保存' }).disabled).toBe(true);
});
