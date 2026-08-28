import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import GroupTable, { serializeGroupTable } from '../components/GroupTable';
import { API } from '../../../helpers';

vi.mock('../../../helpers', async () => {
  const actual = await vi.importActual('../../../helpers');
  return { ...actual, API: { get: vi.fn(), post: vi.fn(), put: vi.fn() } };
});

beforeEach(() => {
  API.get.mockResolvedValue({ data: { success: true, data: [] } });
});

/**
 * 分组停用态的序列化（设计 §10.8）。
 *
 * GroupEnabled 存的是**被停用**的分组名，未列出即启用。反过来存「启用列表」的话，
 * 新建分组不写进去就会被判成停用——默认值必须是「全部启用」。
 */

describe('serializeGroupTable 的 GroupEnabled', () => {
  it('只把 enabled=false 的分组写进停用列表', () => {
    const out = serializeGroupTable([
      {
        name: 'default',
        ratio: 1,
        description: '',
        selectable: true,
        enabled: true,
      },
      {
        name: 'free',
        ratio: 0,
        description: '',
        selectable: false,
        enabled: false,
      },
    ]);

    expect(JSON.parse(out.GroupEnabled)).toEqual(['free']);
  });

  it('缺 enabled 字段的老数据按启用处理', () => {
    // 本次改造之前保存的行没有 enabled 字段；把 undefined 当成停用会让
    // 升级后第一次保存就把所有分组关掉
    const out = serializeGroupTable([
      { name: 'default', ratio: 1, description: '', selectable: true },
    ]);

    expect(JSON.parse(out.GroupEnabled)).toEqual([]);
  });

  it('全部启用时是空数组而不是 undefined', () => {
    const out = serializeGroupTable([
      {
        name: 'default',
        ratio: 1,
        description: '',
        selectable: true,
        enabled: true,
      },
    ]);

    expect(out.GroupEnabled).toBe('[]');
  });

  it('不影响其余三个 option 的产出', () => {
    const out = serializeGroupTable([
      {
        name: 'free',
        ratio: 0,
        description: '体验',
        selectable: true,
        enabled: false,
      },
    ]);

    expect(JSON.parse(out.GroupRatio)).toEqual({ free: 0 });
    expect(JSON.parse(out.UserUsableGroups)).toEqual({ free: '体验' });
    expect(JSON.parse(out.GroupDescription)).toEqual({ free: '体验' });
  });
});

/**
 * 接线测试：serializeGroupTable 的产出必须被上层接住。
 *
 * 纯函数测试（上面那组）只能证明「算对了」。GroupEnabled 第一版的缺陷正是
 * 算对了但没人接——index.jsx 的 handleGroupTableChange 少解构了这个 key，
 * 表现是勾选框能动、保存提示成功、刷新回滚，全程不报错。
 */
describe('GroupTable 的 onChange 契约', () => {
  it('切换启用勾选时，onChange 必须带上 GroupEnabled', async () => {
    const seen = [];
    render(
      <GroupTable
        groupRatio={JSON.stringify({ free: 0 })}
        userUsableGroups='{}'
        groupDescription='{}'
        groupEnabled='[]'
        onChange={(v) => seen.push(v)}
      />,
    );

    const user = userEvent.setup();
    const checkboxes = document.querySelectorAll('input[type="checkbox"]');
    expect(checkboxes.length).toBeGreaterThan(0);
    await user.click(checkboxes[0]);

    await waitFor(() => expect(seen.length).toBeGreaterThan(0));
    const last = seen[seen.length - 1];

    // 四个 key 一个都不能少：上层是整体解构后写进 inputs 的，
    // 少一个就等于那个字段永远不会被保存
    expect(Object.keys(last).sort()).toEqual(
      [
        'GroupDescription',
        'GroupEnabled',
        'GroupRatio',
        'UserUsableGroups',
      ].sort(),
    );
  });
});
