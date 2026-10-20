import React from 'react';
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';

import { getLogsColumns } from '../UsageLogsColumnDefs';

// 使用日志的 IP 列只给管理员。普通用户的列集合里不能存在这一列——
// 表格和列设置弹窗都直接消费 getLogsColumns 的返回值，所以只要这里没有，
// 两处就都不会出现，也没法通过改 localStorage 恢复出来。

const t = (s) => s;
const COLUMN_KEYS = { COST: 'cost', IP: 'ip' };

const buildColumns = (isAdminUser) =>
  getLogsColumns({ t, COLUMN_KEYS, isAdminUser });

const findIpColumn = (isAdminUser) =>
  buildColumns(isAdminUser).find((column) => column.key === COLUMN_KEYS.IP);

describe('使用日志 IP 列的可见性', () => {
  it('普通用户拿不到 IP 列', () => {
    expect(findIpColumn(false)).toBeUndefined();
  });

  it('管理员能拿到 IP 列', () => {
    expect(findIpColumn(true)).toBeDefined();
  });
});

describe('使用日志 IP 列的渲染', () => {
  const renderIp = (text, record = { type: 6 }) => {
    const column = findIpColumn(true);
    return render(<div>{column.render(text, record, 0)}</div>);
  };

  it('有值时显示 IP（退款 / 任务结算日志同样适用）', () => {
    const { container } = renderIp('1.2.3.4');
    expect(container.textContent).toBe('1.2.3.4');
  });

  it('空值不渲染任何内容', () => {
    const { container } = renderIp('');
    expect(container.textContent).toBe('');
  });
});
