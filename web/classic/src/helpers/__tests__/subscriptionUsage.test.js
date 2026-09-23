import { describe, it, expect } from 'vitest';
import {
  buildUsageRows,
  buildLowUsageWarnings,
  LOW_USAGE_RATIO,
} from '../subscriptionUsage';

// 1 点 = 1 quota，数字直接对得上
const toPoints = (q) => Math.floor(q);

const summary = (over = {}) => ({
  subscription: { id: 1, next_reset_time: 1790000000 },
  ...over,
});

describe('buildUsageRows：算力点', () => {
  it('按可用余量算比例，重置时间取订阅的下次重置', () => {
    const rows = buildUsageRows(
      summary({
        compute_points: {
          total: 50000,
          used: 17860,
          available: 32140,
          expires_at: 1790000000,
        },
      }),
      toPoints,
    );
    expect(rows[0]).toMatchObject({
      kind: 'points',
      total: 50000,
      remain: 32140,
      used: 17860,
      resetKind: 'reset',
      resetAt: 1790000000,
      low: false,
    });
  });

  // 不重置的套餐：批次在订阅结束时作废，要写「到期」而不是「重置」
  it('不重置的套餐显示到期而不是重置', () => {
    const rows = buildUsageRows(
      summary({
        subscription: { id: 1, next_reset_time: 0 },
        compute_points: { total: 100, available: 100, expires_at: 1795000000 },
      }),
      toPoints,
    );
    expect(rows[0].resetKind).toBe('expire');
    expect(rows[0].resetAt).toBe(1795000000);
  });

  it('老式套餐没有算力点时不出这一行', () => {
    expect(buildUsageRows(summary(), toPoints)).toEqual([]);
  });

  it('低于 20% 标低余量', () => {
    const rows = buildUsageRows(
      summary({ compute_points: { total: 1000, available: 199 } }),
      toPoints,
    );
    expect(rows[0].low).toBe(true);
    expect(LOW_USAGE_RATIO).toBe(0.2);
  });
});

describe('buildUsageRows：权益次数', () => {
  const ent = (over = {}) => ({
    entitlement_id: 7,
    models: 'gpt-5',
    consume_points: true,
    limit_count: 500,
    used_count: 347,
    reset_period: 'monthly',
    next_reset_time: 1789000000,
    rate_limit_rpm: 0,
    channel_limited: false,
    ...over,
  });

  it('限次权益给出已用 / 上限与重置时间', () => {
    const [row] = buildUsageRows(summary({ entitlements: [ent()] }), toPoints);
    expect(row).toMatchObject({
      kind: 'count',
      label: 'gpt-5',
      used: 347,
      total: 500,
      remain: 153,
      resetAt: 1789000000,
      low: false,
      note: '每月',
    });
  });

  // 运营中途调小上限时，已用可能超过上限：不显示负数
  it('已用超过上限时剩余归零', () => {
    const [row] = buildUsageRows(
      summary({ entitlements: [ent({ used_count: 900 })] }),
      toPoints,
    );
    expect(row.remain).toBe(0);
    expect(row.low).toBe(true);
  });

  it('不限次显示速率限制', () => {
    const [row] = buildUsageRows(
      summary({
        entitlements: [
          ent({
            models: 'qwen3-*,glm-4',
            limit_count: 0,
            rate_limit_rpm: 60,
            consume_points: false,
          }),
        ],
      }),
      toPoints,
    );
    expect(row.kind).toBe('unlimited');
    expect(row.label).toBe('qwen3-*、glm-4');
    expect(row.note).toBe('60 RPM · 不消耗算力点');
  });

  it('限渠道要标出来', () => {
    const [row] = buildUsageRows(
      summary({ entitlements: [ent({ channel_limited: true })] }),
      toPoints,
    );
    expect(row.note).toContain('限特定渠道');
  });
});

describe('buildLowUsageWarnings', () => {
  it('每条低余量行一句，并说明用尽后按余额计费', () => {
    const rows = buildUsageRows(
      summary({
        compute_points: { total: 1000, available: 100 },
        entitlements: [
          {
            entitlement_id: 1,
            models: 'gpt-5',
            consume_points: true,
            limit_count: 500,
            used_count: 450,
          },
          {
            entitlement_id: 2,
            models: 'm',
            consume_points: true,
            limit_count: 500,
            used_count: 10,
          },
        ],
      }),
      toPoints,
    );
    expect(buildLowUsageWarnings(rows)).toEqual([
      '算力点剩余 100，用尽后将按账户余额计费',
      'gpt-5 剩余 50 次，用尽后将按账户余额计费',
    ]);
  });
});
