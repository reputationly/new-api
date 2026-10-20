import { describe, it, expect } from 'vitest';

import { getSelectAllState } from '../modals/ColumnSelectorModal';

// 「全选」的聚合状态只能统计当前用户看得到的列。管理员专属列对普通用户恒为
// false 且被隐藏，把它们算进 every/some 会让全选框永远停在半选，点它也只
// 能把可见列全开、永远走不到「全关」。

const COLUMN_KEYS = {
  TIME: 'time',
  CHANNEL: 'channel',
  USERNAME: 'username',
  TOKEN: 'token',
  GROUP: 'group',
  TYPE: 'type',
  MODEL: 'model',
  USE_TIME: 'use_time',
  PROMPT: 'prompt',
  COMPLETION: 'completion',
  COST: 'cost',
  RETRY: 'retry',
  IP: 'ip',
  DETAILS: 'details',
};

const COLUMNS = Object.values(COLUMN_KEYS).map((key) => ({ key }));

const defaults = (isAdminUser) => ({
  [COLUMN_KEYS.TIME]: true,
  [COLUMN_KEYS.CHANNEL]: isAdminUser,
  [COLUMN_KEYS.USERNAME]: isAdminUser,
  [COLUMN_KEYS.TOKEN]: true,
  [COLUMN_KEYS.GROUP]: true,
  [COLUMN_KEYS.TYPE]: true,
  [COLUMN_KEYS.MODEL]: true,
  [COLUMN_KEYS.USE_TIME]: true,
  [COLUMN_KEYS.PROMPT]: true,
  [COLUMN_KEYS.COMPLETION]: true,
  [COLUMN_KEYS.COST]: true,
  [COLUMN_KEYS.RETRY]: isAdminUser,
  [COLUMN_KEYS.IP]: isAdminUser,
  [COLUMN_KEYS.DETAILS]: true,
});

const stateOf = (visibleColumns, isAdminUser) =>
  getSelectAllState(COLUMNS, visibleColumns, isAdminUser, COLUMN_KEYS);

describe('列设置的全选聚合状态', () => {
  it('普通用户默认是「已全选」，不是半选', () => {
    expect(stateOf(defaults(false), false)).toEqual({
      checked: true,
      indeterminate: false,
    });
  });

  it('普通用户取消一列后变半选', () => {
    const visible = { ...defaults(false), [COLUMN_KEYS.COST]: false };
    expect(stateOf(visible, false)).toEqual({
      checked: false,
      indeterminate: true,
    });
  });

  it('普通用户全部取消后不是半选', () => {
    const visible = Object.fromEntries(
      Object.values(COLUMN_KEYS).map((key) => [key, false]),
    );
    expect(stateOf(visible, false)).toEqual({
      checked: false,
      indeterminate: false,
    });
  });

  it('管理员默认是「已全选」', () => {
    expect(stateOf(defaults(true), true)).toEqual({
      checked: true,
      indeterminate: false,
    });
  });

  it('管理员取消一列后变半选', () => {
    const visible = { ...defaults(true), [COLUMN_KEYS.IP]: false };
    expect(stateOf(visible, true)).toEqual({
      checked: false,
      indeterminate: true,
    });
  });
});
