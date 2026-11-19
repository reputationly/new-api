import { describe, expect, it } from 'vitest';

import { buildVisibilitySignature } from '../useChannelVisibilitySummary';

// 可见性汇总跟着这个签名重算。两条要求都在这里锁住：
// 1) 标签聚合视图里，启停单个子渠道只改子行状态，标签行状态不变 —— 不带子行就漏；
// 2) 不能用数组引用，单渠道测速/查余额也会替换数组，那样每次都会白跑两条全表查询。
describe('buildVisibilitySignature', () => {
  it('标签行状态不变、子行状态变化时签名必须变', () => {
    const before = [
      {
        id: 'tag1',
        status: 1,
        children: [
          { id: 11, status: 1 },
          { id: 12, status: 1 },
        ],
      },
    ];
    const after = [
      {
        id: 'tag1',
        status: 1,
        children: [
          { id: 11, status: 2 },
          { id: 12, status: 1 },
        ],
      },
    ];
    expect(buildVisibilitySignature(before)).not.toBe(
      buildVisibilitySignature(after),
    );
  });

  it('只有子渠道被删除/新增时签名也变', () => {
    const before = [
      { id: 'tag1', status: 1, children: [{ id: 11, status: 1 }] },
    ];
    const after = [{ id: 'tag1', status: 1, children: [] }];
    expect(buildVisibilitySignature(before)).not.toBe(
      buildVisibilitySignature(after),
    );
  });

  it('非可见性字段变化（响应时间/余额）不影响签名', () => {
    const before = [{ id: 1, status: 1, response_time: 100, balance: 10 }];
    const after = [{ id: 1, status: 1, response_time: 999, balance: 0 }];
    expect(buildVisibilitySignature(before)).toBe(
      buildVisibilitySignature(after),
    );
  });

  it('普通渠道启停会改变签名，空输入安全', () => {
    expect(buildVisibilitySignature([{ id: 1, status: 1 }])).not.toBe(
      buildVisibilitySignature([{ id: 1, status: 2 }]),
    );
    expect(buildVisibilitySignature(undefined)).toBe('');
    expect(buildVisibilitySignature([])).toBe('');
  });
});
