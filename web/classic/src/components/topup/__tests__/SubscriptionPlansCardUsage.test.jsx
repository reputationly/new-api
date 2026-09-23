import React from 'react';
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

vi.mock('../../../helpers', async () => {
  const actual = await vi.importActual('../../../helpers');
  return {
    ...actual,
    // 对比表会自己请求；这里只守余量与购买卡片的接线
    API: { get: vi.fn().mockResolvedValue({ data: { success: false } }) },
  };
});

import SubscriptionPlansCard from '../SubscriptionPlansCard';

// 「我的订阅」与购买卡片的真实渲染。
//
// 余量数据只挂在活跃订阅（subscriptions）上，而列表遍历的是全部订阅
// （all_subscriptions）——按 id 对不上的话面板一个都不出，helper 测试照样全绿。

const t = (s) => s;
const future = Math.floor(Date.now() / 1000) + 30 * 86400;

const newStyleSub = {
  subscription: {
    id: 11,
    plan_id: 1,
    status: 'active',
    end_time: future,
    next_reset_time: future,
    amount_total: 0,
  },
  no_legacy_quota: true,
};

const withUsage = {
  ...newStyleSub,
  compute_points: { total: 5000000, used: 4500000, available: 500000 },
  entitlements: [
    {
      entitlement_id: 3,
      models: 'gpt-5',
      consume_points: true,
      limit_count: 500,
      used_count: 347,
      reset_period: 'monthly',
      next_reset_time: future,
    },
  ],
};

const newStylePlan = {
  plan: {
    id: 1,
    title: '专业版',
    price_amount: 29,
    total_amount: 0,
    compute_points_per_period: 5000000,
    duration_unit: 'month',
    duration_value: 1,
  },
  no_legacy_quota: true,
};

const renderCard = (props) =>
  render(
    <SubscriptionPlansCard
      t={t}
      plans={[newStylePlan]}
      activeSubscriptions={[withUsage]}
      allSubscriptions={[newStyleSub]}
      withCard={false}
      {...props}
    />,
  );

beforeEach(() => {
  // 1 点 = 100 quota
  localStorage.setItem('quota_per_compute_point', '100');
  localStorage.setItem('quota_per_unit', '500000');
});

describe('我的订阅：余量面板', () => {
  it('按 id 把活跃订阅的余量接到列表项上', () => {
    renderCard();
    // 1 点 = 100 quota：总 5000000 quota = 50000 点，剩 500000 quota = 5000 点
    expect(screen.getByText('5,000 / 50,000')).toBeTruthy();
    expect(screen.getByText('153 / 500 次')).toBeTruthy();
  });

  it('余量低于 20% 时提示用尽后按余额计费', () => {
    renderCard();
    expect(
      screen.getByText(/算力点剩余 5,000，用尽后将按账户余额计费/),
    ).toBeTruthy();
  });

  // 新式套餐的 0 是「没有通用额度」：写「总额度：不限」会让用户以为套餐外也能随便用
  it('新式套餐不显示「总额度：不限」', () => {
    renderCard();
    expect(screen.queryByText(/总额度/)).toBeNull();
  });

  it('老式套餐照旧显示总额度', () => {
    const legacy = {
      subscription: { ...newStyleSub.subscription, id: 12 },
    };
    renderCard({
      activeSubscriptions: [legacy],
      allSubscriptions: [legacy],
    });
    expect(screen.getAllByText(/总额度/).length).toBeGreaterThan(0);
  });
});

describe('购买卡片', () => {
  it('新式套餐写每期算力点与「超出按余额计费」，不写「总额度：不限」', () => {
    renderCard({ activeSubscriptions: [], allSubscriptions: [] });
    expect(screen.getByText('每期算力点: 50,000')).toBeTruthy();
    expect(
      screen.getAllByText('超出套餐的部分按账户余额计费').length,
    ).toBeGreaterThan(0);
    expect(screen.queryByText(/总额度: 不限/)).toBeNull();
  });
});
