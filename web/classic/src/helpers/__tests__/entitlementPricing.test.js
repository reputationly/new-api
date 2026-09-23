import { describe, it, expect, beforeEach } from 'vitest';
import {
  getModelCoverage,
  buildEntitlementBadges,
  buildCoverageTooltip,
  remainingCount,
} from '../entitlementPricing';

beforeEach(() => {
  // 1 算力点 = 100 quota unit，好算
  localStorage.setItem('quota_per_compute_point', '100');
});

const cov = (over = {}) => ({
  entitlement_id: 1,
  plan_id: 1,
  plan_title: '专业版',
  consume_points: true,
  discount: 1,
  limit_count: 0,
  used_count: 0,
  reset_period: 'never',
  channel_limited: false,
  ...over,
});

describe('getModelCoverage', () => {
  // 后端对「未登录」「无套餐」「有套餐但没覆盖任何模型」统一返回 null，
  // 三者在展示上是同一件事。
  it('无覆盖数据时返回 null', () => {
    expect(getModelCoverage(null, 'gpt-5')).toBeNull();
    expect(getModelCoverage({}, 'gpt-5')).toBeNull();
    expect(getModelCoverage({ 'gpt-5': cov() }, 'other')).toBeNull();
  });

  it('命中时返回该模型的覆盖', () => {
    const c = cov();
    expect(getModelCoverage({ 'gpt-5': c }, 'gpt-5')).toBe(c);
  });
});

describe('buildEntitlementBadges', () => {
  it('无覆盖不出角标', () => {
    expect(buildEntitlementBadges(null)).toEqual([]);
  });

  it('套餐名排最前', () => {
    const badges = buildEntitlementBadges(cov());
    expect(badges[0].text).toBe('专业版');
  });

  it('限次时显示次数与周期', () => {
    const badges = buildEntitlementBadges(
      cov({ limit_count: 500, reset_period: 'monthly' }),
    );
    expect(badges.map((b) => b.text)).toContain('500 次/月');
  });

  it('不重置的权益不编造周期', () => {
    const badges = buildEntitlementBadges(
      cov({ limit_count: 500, reset_period: 'never' }),
    );
    expect(badges.map((b) => b.text)).toContain('500 次');
  });

  // 免费与限次互斥：不消耗算力点时次数不是成本闸门，显示次数会误导
  it('不消耗算力点时显示「免费」而非次数', () => {
    const badges = buildEntitlementBadges(
      cov({ consume_points: false, limit_count: 500, reset_period: 'monthly' }),
    );
    const texts = badges.map((b) => b.text);
    expect(texts).toContain('不消耗算力点');
    expect(texts).not.toContain('500 次/月');
  });

  // 限渠道的覆盖是有条件的，必须标出来
  it('限渠道要出角标', () => {
    const badges = buildEntitlementBadges(cov({ channel_limited: true }));
    expect(badges.map((b) => b.key)).toContain('channel');
  });
});

describe('buildCoverageTooltip', () => {
  // 单套餐时没什么要解释的，多一行字只是噪音
  it('单个套餐不生成说明', () => {
    expect(buildCoverageTooltip(cov({ all: [cov()] }))).toBe('');
    expect(buildCoverageTooltip(cov())).toBe('');
  });

  // 多套餐时用户最想知道「为什么是这个价」，且必须指明当前走哪个
  it('多套餐时列全并指明当前走哪个', () => {
    const c = cov({
      all: [
        cov({ plan_id: 1, plan_title: '专业版', discount: 1 }),
        cov({ plan_id: 2, plan_title: '基础版', discount: 0.5 }),
      ],
    });
    const text = buildCoverageTooltip(c);
    expect(text).toContain('2 个套餐');
    expect(text).toContain('专业版');
    expect(text).toContain('基础版');
    expect(text).toContain('当前走「专业版」');
  });

  // 同一套餐里排在前面的限了渠道：all 有两条但只有一个套餐，不能说「2 个套餐」
  it('同套餐两条时按套餐去重计数，并标出限渠道的那条', () => {
    const c = cov({
      entitlement_id: 2,
      discount: 0.5,
      all: [
        cov({
          entitlement_id: 1,
          channel_limited: true,
          consume_points: false,
        }),
        cov({ entitlement_id: 2, discount: 0.5 }),
      ],
    });
    const text = buildCoverageTooltip(c);
    expect(text).not.toContain('2 个套餐');
    expect(text).toContain('专业版（限特定渠道）：不消耗算力点');
    expect(text).toContain('只在请求落到对应渠道时生效');
  });

  // 「当前走哪个」必须取顶层（不限渠道时扣费命中的那条），不是 all[0]
  it('当前走哪个取顶层字段而不是 all[0]', () => {
    const c = cov({
      plan_id: 2,
      plan_title: '基础版',
      all: [
        cov({ plan_id: 1, plan_title: '专业版', channel_limited: true }),
        cov({ plan_id: 2, plan_title: '基础版' }),
      ],
    });
    expect(buildCoverageTooltip(c)).toContain('当前走「基础版」');
  });
});

describe('remainingCount', () => {
  it('不限次返回 null', () => {
    expect(remainingCount(cov({ limit_count: 0 }))).toBeNull();
    expect(remainingCount(null)).toBeNull();
  });

  it('限次时返回剩余', () => {
    expect(remainingCount(cov({ limit_count: 500, used_count: 120 }))).toBe(
      380,
    );
  });

  // 本期已用超过上限（运营中途调小了上限）时不显示负数
  it('已用超过上限时归零而不是负数', () => {
    expect(remainingCount(cov({ limit_count: 100, used_count: 300 }))).toBe(0);
  });
});
