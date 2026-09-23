import { describe, it, expect } from 'vitest';
import {
  getEntitlementLogInfo,
  overageText,
  entitlementTagText,
} from '../entitlementLog';

describe('getEntitlementLogInfo', () => {
  it('套餐内：读落库的点数与次数快照', () => {
    const info = getEntitlementLogInfo({
      billing_source: 'entitlement',
      entitlement_plan_title: '专业版',
      compute_points: 120,
      entitlement_limit_count: 500,
      entitlement_used_count: 153,
    });
    expect(info).toEqual({
      kind: 'entitlement',
      planTitle: '专业版',
      points: 120,
      limit: 500,
      used: 153,
      remain: 347,
    });
  });

  it('不限次时不给剩余次数', () => {
    const info = getEntitlementLogInfo({
      billing_source: 'entitlement',
      compute_points: 5,
    });
    expect(info.remain).toBeNull();
    expect(info.planTitle).toBe('套餐');
  });

  it('超额：带出原因说明', () => {
    const info = getEntitlementLogInfo({
      billing_source: 'wallet',
      entitlement_fallback: {
        reason: 'points_insufficient',
        plan_title: '专业版',
        points_needed: 120,
        points_available: 40,
      },
    });
    expect(info.kind).toBe('overage');
    expect(info.text).toBe(
      '本次需 120 算力点，「专业版」剩余 40 点不足，本次按账户余额计费',
    );
  });

  it('普通钱包消费不是超额', () => {
    expect(getEntitlementLogInfo({ billing_source: 'wallet' })).toBeNull();
    expect(getEntitlementLogInfo(null)).toBeNull();
  });
});

describe('overageText', () => {
  it('次数用尽写明上限', () => {
    expect(
      overageText({
        reason: 'count_exhausted',
        plan_title: '专业版',
        limit_count: 500,
      }),
    ).toBe('「专业版」本期次数已用尽（上限 500 次），本次按账户余额计费');
  });

  it('未知原因给兜底说明而不是空串', () => {
    expect(overageText({ reason: 'x', plan_title: 'P' })).toContain(
      '按账户余额计费',
    );
  });
});

describe('entitlementTagText', () => {
  it('写套餐名与点数', () => {
    expect(entitlementTagText({ planTitle: '专业版', points: 1200 })).toBe(
      '专业版 1,200点',
    );
  });

  it('不扣点时写免费', () => {
    expect(entitlementTagText({ planTitle: '专业版', points: 0 })).toBe(
      '专业版 免费',
    );
  });
});
