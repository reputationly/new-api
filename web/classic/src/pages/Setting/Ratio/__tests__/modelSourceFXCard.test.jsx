import React from 'react';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { API } from '../../../../helpers';
import ModelSourceFXCard from '../components/ModelSourceFXCard';

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
  expect(screen.queryByText(/休市期间不发布/)).toBeNull();
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

it('shows an older CFETS quote as a closure note, not a warning', async () => {
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
  expect(
    await screen.findByText('最新中间价为 09/30（休市期间不发布）'),
  ).toBeTruthy();
  expect(screen.queryByText(/CFETS 请求失败|CFETS 数据未更新/)).toBeNull();
});
