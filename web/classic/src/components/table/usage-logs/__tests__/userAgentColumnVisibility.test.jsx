import React from 'react';
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';

import { getLogsColumns } from '../UsageLogsColumnDefs';

// 「客户端」列（User-Agent）与 IP 一样只给管理员，并紧跟在 IP 后面。

const t = (s) => s;
const COLUMN_KEYS = { COST: 'cost', IP: 'ip', USER_AGENT: 'user_agent' };

const buildColumns = (isAdminUser) =>
  getLogsColumns({ t, COLUMN_KEYS, isAdminUser });

describe('使用日志客户端列', () => {
  it('普通用户拿不到客户端列', () => {
    expect(
      buildColumns(false).find((c) => c.key === COLUMN_KEYS.USER_AGENT),
    ).toBeUndefined();
  });

  it('管理员的客户端列紧跟 IP 列', () => {
    const keys = buildColumns(true).map((c) => c.key);
    expect(keys.indexOf(COLUMN_KEYS.USER_AGENT)).toBe(
      keys.indexOf(COLUMN_KEYS.IP) + 1,
    );
  });

  it('有值时显示原始 User-Agent，空值不渲染', () => {
    const column = buildColumns(true).find(
      (c) => c.key === COLUMN_KEYS.USER_AGENT,
    );
    const ua = 'WorkBuddy/5.11.3 WorkBuddy/5.11.3 CLI/2.176.0';
    const { container } = render(<div>{column.render(ua, {}, 0)}</div>);
    expect(container.textContent).toBe(ua);
    const empty = render(<div>{column.render('', {}, 0)}</div>);
    expect(empty.container.textContent).toBe('');
  });
});
