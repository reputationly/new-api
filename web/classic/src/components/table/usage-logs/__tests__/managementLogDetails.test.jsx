import { it, expect, vi } from 'vitest';
import { render, renderHook, screen, waitFor } from '@testing-library/react';
import { useLogsData } from '../../../../hooks/usage-logs/useUsageLogsData';

vi.mock('react-i18next', async () => ({
  ...(await vi.importActual('react-i18next')),
  useTranslation: () => ({ t: (key) => key }),
}));

vi.mock('../../../../helpers', async () => ({
  ...(await vi.importActual('../../../../helpers')),
  isAdmin: () => true,
  API: {
    get: vi.fn(async (url) => ({
      data: {
        success: true,
        data: {
          page: 1,
          page_size: 10,
          total: 2,
          items: url.startsWith('/api/log/')
            ? [
                {
                  id: 1,
                  type: 3,
                  created_at: 1,
                  other: JSON.stringify({
                    admin_info: {
                      admin_username: 'admin',
                      changes: [
                        {
                          key: 'ModelRatio',
                          path: ['model-a'],
                          before: 0,
                          before_exists: true,
                          after: 2,
                          after_exists: true,
                        },
                      ],
                    },
                  }),
                },
                { id: 2, type: 2, created_at: 1, other: '{}' },
              ]
            : [],
        },
      },
    })),
  },
}));

it('管理日志显示配置差异，但不显示请求转换或计费模式', async () => {
  const { result } = renderHook(() => useLogsData());
  await waitFor(() => expect(result.current.logs).toHaveLength(2));

  const auditRows = result.current.expandData[1];
  const billingRows = result.current.expandData[2];
  expect(auditRows.map(({ key }) => key)).not.toContain('请求转换');
  expect(auditRows.map(({ key }) => key)).not.toContain('计费模式');
  expect(billingRows.map(({ key }) => key)).toEqual(
    expect.arrayContaining(['请求转换', '计费模式']),
  );

  render(auditRows.find(({ key }) => key === '配置变更').value);
  expect(
    screen.getAllByRole('columnheader').map((cell) => cell.textContent),
  ).toEqual(['配置项', '修改前', '修改后']);
  expect(screen.getAllByRole('cell').map((cell) => cell.textContent)).toEqual([
    'ModelRatio / model-a',
    '0',
    '2',
  ]);
});
