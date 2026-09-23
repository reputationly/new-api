import React from 'react';
import { describe, it, expect, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';

import { getLogsColumns } from '../UsageLogsColumnDefs';
import golden from '../../../../helpers/__tests__/fixtures/entitlementLogOther.json';
import { getEntitlementLogInfo } from '../../../../helpers/entitlementLog';

// 使用日志「花费」列的套餐展示。other 用后端写入函数产出的 golden fixture
// （service/entitlement_log_test.go 守着它）：前后端键名一旦对不上，这里会失败，
// 而不是两边各自全绿、页面上一个字都不出。

const t = (s) => s;
const COLUMN_KEYS = { COST: 'cost' };

const costCell = (other, quota = 500000) => {
  const col = getLogsColumns({ t, COLUMN_KEYS, isAdminUser: false }).find(
    (c) => c.key === 'cost',
  );
  const record = {
    type: 2,
    quota,
    points_consumed: 0,
    other: JSON.stringify(other),
  };
  return render(<div>{col.render(quota, record, 0)}</div>);
};

beforeEach(() => {
  localStorage.setItem('quota_per_unit', '500000');
  localStorage.setItem('quota_display_type', 'USD');
});

describe('golden fixture 能被前端正确解析', () => {
  it('套餐内', () => {
    expect(getEntitlementLogInfo(golden.entitlement)).toEqual({
      kind: 'entitlement',
      planTitle: '专业版',
      points: 120,
      limit: 500,
      used: 153,
      remain: 347,
    });
  });

  it('超额：点数不足 / 次数用尽', () => {
    expect(getEntitlementLogInfo(golden.overage_points).text).toBe(
      '本次需 120 算力点，「专业版」剩余 40 点不足，本次按账户余额计费',
    );
    expect(getEntitlementLogInfo(golden.overage_count).text).toBe(
      '「专业版」本期次数已用尽（上限 500 次），本次按账户余额计费',
    );
  });

  // 积分优先再扣余额：只写「按账户余额计费」会让用户对不上积分为什么少了
  it('超额：积分+余额混扣如实说明', () => {
    expect(getEntitlementLogInfo(golden.overage_hybrid).text).toBe(
      '「专业版」本期次数已用尽（上限 500 次），本次按积分与账户余额计费',
    );
  });
});

describe('花费列', () => {
  // 钱包一分没动：显示金额会让用户以为被扣了钱
  it('套餐内只显示「套餐名 N点」标签，不显示金额', () => {
    const { container } = costCell(golden.entitlement);
    expect(screen.getByText('专业版 120点')).toBeTruthy();
    expect(container.textContent).not.toContain('$');
  });

  it('超额显示金额并标「超额」', () => {
    const { container } = costCell(golden.overage_points);
    expect(screen.getByText('超额')).toBeTruthy();
    expect(container.textContent).toContain('$');
  });

  it('普通钱包消费不标超额', () => {
    costCell({ billing_source: 'wallet' });
    expect(screen.queryByText('超额')).toBeNull();
  });
});
