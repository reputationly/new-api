import React from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';

vi.mock('../../../../helpers', () => ({
  timestamp2string: vi.fn(),
  renderGroup: vi.fn(),
  renderQuota: () => '¥1.00',
  getChannelIcon: vi.fn(),
  showSuccess: vi.fn(),
  showError: vi.fn(),
  showInfo: vi.fn(),
}));

import { getChannelsColumns } from '../ChannelsColumnDefs';

describe('channel balance currency', () => {
  const column = getChannelsColumns({
    t: (text) => text,
    COLUMN_KEYS: { BALANCE: 'balance' },
  }).find(({ key }) => key === 'balance');

  it.each([
    [20, '$12.34'],
    [25, '$12.34'],
    [43, '¥12.34'],
    [40, '¥12.34'],
    [10, '12.34'],
  ])(
    'formats provider type %s without converting its balance',
    (type, expected) => {
      const html = renderToStaticMarkup(
        column.render(null, { type, balance: 12.34, used_quota: 1 }),
      );
      expect(html).toContain('>¥1.00<');
      expect(html).toContain(`>${expected}<`);
    },
  );
});
