import { describe, it, expect } from 'vitest';
import { buildCreditSummary } from '../creditDisplay';

describe('buildCreditSummary', () => {
  it('没开授信时不展示', () => {
    expect(buildCreditSummary({ credit_limit: 0, credit_used: 0 })).toBeNull();
    expect(buildCreditSummary(null)).toBeNull();
  });

  it('子账户不展示（授信挂在企业主账户上）', () => {
    expect(
      buildCreditSummary({ credit_limit: 1000, parent_user_id: 3 }),
    ).toBeNull();
  });

  it('正常情况给出可用额度', () => {
    expect(
      buildCreditSummary({ credit_limit: 1000, credit_used: 300 }),
    ).toEqual({ limit: 1000, used: 300, available: 700, over: 0 });
  });

  // 结算时服务已交付，欠款可以超过上限：可用截到 0，超出单独给出，不显示负数
  it('欠款超过上限时可用为 0、给出超出额', () => {
    expect(
      buildCreditSummary({ credit_limit: 10000, credit_used: 15000 }),
    ).toEqual({ limit: 10000, used: 15000, available: 0, over: 5000 });
  });
});
