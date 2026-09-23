import { describe, it, expect, beforeEach } from 'vitest';
import { calculateModelPrice, getModelPriceItems } from '../utils';

/**
 * 「套餐内」价格行。
 *
 * 最要紧的不是新行长得对，而是**没有套餐时一行不多**——这个功能对未登录用户、
 * 无套餐用户、以及套餐没覆盖的模型必须完全隐形，否则就是给所有人改了定价页。
 */

const t = (s) => s;

beforeEach(() => {
  localStorage.setItem('quota_per_unit', '500000');
  localStorage.setItem('quota_per_compute_point', '100');
  localStorage.setItem('quota_display_type', 'USD');
});

const coverage = (over = {}) => ({
  plan_title: '专业版',
  consume_points: true,
  discount: 1,
  limit_count: 0,
  used_count: 0,
  reset_period: 'never',
  channel_limited: false,
  ...over,
});

const tokenRecord = {
  model_name: 'gpt-test',
  quota_type: 0,
  model_ratio: 1,
  completion_ratio: 1,
  enable_groups: ['default'],
};

const perCallRecord = {
  model_name: 'img-test',
  quota_type: 1,
  model_price: 0.04,
  enable_groups: ['default'],
};

const priceOf = (record, entitlementCoverage) =>
  calculateModelPrice({
    record,
    selectedGroup: 'default',
    groupRatio: { default: 1 },
    groupModelRatio: {},
    displayPrice: (v) => String(v),
    quotaDisplayType: 'USD',
    entitlementCoverage,
    quotaPerComputePoint: 100,
  });

const itemsOf = (record, entitlementCoverage) =>
  getModelPriceItems(priceOf(record, entitlementCoverage), t, 'USD');

const entitlementRows = (items) => items.filter((i) => i.isEntitlement);

describe('无套餐时完全隐形', () => {
  it('按量计费模型不多出任何行', () => {
    expect(entitlementRows(itemsOf(tokenRecord, null))).toHaveLength(0);
  });

  it('按次计费模型不多出任何行', () => {
    expect(entitlementRows(itemsOf(perCallRecord, null))).toHaveLength(0);
  });

  // 「未登录」与「有套餐但没覆盖这个模型」在这里是同一件事
  it('覆盖为 undefined 时同样不多出行', () => {
    expect(entitlementRows(itemsOf(tokenRecord, undefined))).toHaveLength(0);
  });
});

describe('有覆盖时追加「套餐内」行', () => {
  it('按量计费每个价格档位后各追加一行', () => {
    const rows = entitlementRows(itemsOf(tokenRecord, coverage()));
    expect(rows.length).toBeGreaterThan(0);
    rows.forEach((r) => expect(r.value).toContain('算力点'));
  });

  it('按次计费追加一行', () => {
    const rows = entitlementRows(itemsOf(perCallRecord, coverage()));
    expect(rows).toHaveLength(1);
    // 0.04 美元 × 500000 quota/美元 ÷ 100 quota/点 = 200 点
    expect(rows[0].value).toBe('200 算力点');
  });

  // 折扣作用在消耗侧：×0.5 就是同样的调用只烧一半点数
  it('折扣让点数减半', () => {
    const rows = entitlementRows(
      itemsOf(perCallRecord, coverage({ discount: 0.5 })),
    );
    expect(rows[0].value).toBe('100 算力点');
  });

  // 原价必须留在原位：算力点烧完就按原价扣，这是用户最需要知道的数
  it('原价行仍在，且排在套餐内价之前', () => {
    const items = itemsOf(perCallRecord, coverage());
    const priceIdx = items.findIndex((i) => i.key === 'fixed');
    const entIdx = items.findIndex((i) => i.key === 'fixed-entitlement');
    expect(priceIdx).toBeGreaterThanOrEqual(0);
    expect(entIdx).toBeGreaterThan(priceIdx);
  });
});

describe('不消耗算力点的权益', () => {
  // 业务上当前不会出现，但开关存在；显示「0 算力点」会看起来像算错了，
  // 所以这类权益干脆不出这一行。
  it('不追加套餐内价行', () => {
    const rows = entitlementRows(
      itemsOf(perCallRecord, coverage({ consume_points: false })),
    );
    expect(rows).toHaveLength(0);
  });
});

// 带缓存写入 / 图片 / 音频倍率的模型：套餐内价此前只抄了前三项，这几行没有套餐内价。
// 积分价与套餐内价现在从同一张分项表映射，这里同时锁住两边的每一项。
describe('分项价格：积分价与套餐内价同一张表', () => {
  const richRecord = {
    ...tokenRecord,
    cache_ratio: 0.1,
    create_cache_ratio: 1.25,
    image_ratio: 3,
    audio_ratio: 4,
    audio_completion_ratio: 2,
  };
  const priceData = () =>
    calculateModelPrice({
      record: richRecord,
      selectedGroup: 'default',
      groupRatio: { default: 1 },
      groupModelRatio: {},
      displayPrice: (v) => String(v),
      quotaDisplayType: 'USD',
      pointsEnabled: true,
      quotaPerPoint: 100,
      pointsEnabledGroups: ['default'],
      entitlementCoverage: coverage(),
      quotaPerComputePoint: 100,
    });
  const expectedMultiplier = {
    input: 1,
    completion: 1,
    cache: 0.1,
    'create-cache': 1.25,
    image: 3,
    'audio-input': 4,
    'audio-output': 8,
  };

  it.each(Object.entries(expectedMultiplier))(
    '%s 项积分价与套餐内价都按倍率算出',
    (key, mult) => {
      const d = priceData();
      // 两边都是 null 时下面的断言会空转，先确认真的算出了数
      expect(d.points.input).toBeGreaterThan(0);
      expect(d.entitlement.input).toBeGreaterThan(0);
      expect(d.points[key]).toBeCloseTo(d.points.input * mult, 9);
      expect(d.entitlement[key]).toBeCloseTo(d.entitlement.input * mult, 9);
    },
  );

  it('图片与音频行后各追加一行套餐内价', () => {
    const keys = entitlementRows(getModelPriceItems(priceData(), t, 'USD')).map(
      (r) => r.key,
    );
    expect(keys).toEqual(
      expect.arrayContaining([
        'image-entitlement',
        'audio-input-entitlement',
        'audio-output-entitlement',
        'create-cache-entitlement',
      ]),
    );
  });
});
