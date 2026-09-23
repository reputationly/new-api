import { describe, it, expect, beforeEach } from 'vitest';
import { buildPlanComparisonRows } from '../planComparison';

// 1 点 = 100 quota，1 美元 = 500000 quota → 50000 点 = 10 美元
beforeEach(() => {
  localStorage.setItem('quota_per_compute_point', '100');
  localStorage.setItem('quota_per_unit', '500000');
});

const POINTS_50K = 50000 * 100; // quota unit

const plans = [
  { plan: { id: 1, title: '基础版' } },
  { plan: { id: 2, title: '专业版' } },
];

const img = { model_name: 'img-hd', quota_type: 1, model_price: 0.04 };
const gpt = { model_name: 'gpt-5', quota_type: 0, model_ratio: 2.5 };

const column = (over = {}) => ({
  plan_id: 1,
  compute_points_per_period: POINTS_50K,
  coverage: {},
  limits: [],
  ...over,
});

const covered = (over = {}) => ({
  consume_points: true,
  discount: 1,
  channel_limited: false,
  ...over,
});

const rowOf = (result, key) => result.rows.find((r) => r.key === key);

describe('buildPlanComparisonRows', () => {
  it('无数据时不出表', () => {
    expect(buildPlanComparisonRows(null, plans)).toBeNull();
    expect(buildPlanComparisonRows({ plans: [] }, plans)).toBeNull();
  });

  it('列标题取套餐名', () => {
    const r = buildPlanComparisonRows(
      { models: [], plans: [column(), column({ plan_id: 2 })] },
      plans,
    );
    expect(r.columns.map((c) => c.title)).toEqual(['基础版', '专业版']);
  });

  it('每期算力点按展示点数显示', () => {
    const r = buildPlanComparisonRows({ models: [], plans: [column()] }, plans);
    expect(rowOf(r, 'points').cells[0].text).toBe('50,000');
  });

  it('覆盖的按次模型换算成次数', () => {
    const r = buildPlanComparisonRows(
      {
        models: [img],
        plans: [column({ coverage: { 'img-hd': covered() } })],
      },
      plans,
    );
    // 10 美元 / 0.04 = 250 次
    expect(rowOf(r, 'model-img-hd').cells[0].text).toBe('≈ 250 次');
  });

  // 点数只能花在权益覆盖的模型上；没覆盖还换算出数字就是虚假宣传
  it('套餐没覆盖的模型显示 —', () => {
    const r = buildPlanComparisonRows(
      { models: [img], plans: [column()] },
      plans,
    );
    expect(rowOf(r, 'model-img-hd').cells[0].text).toBe('—');
  });

  // 折扣作用在消耗侧：×0.5 就是同样的点数能用两倍的量
  it('折扣让换算量翻倍', () => {
    const r = buildPlanComparisonRows(
      {
        models: [img],
        plans: [column({ coverage: { 'img-hd': covered({ discount: 0.5 }) } })],
      },
      plans,
    );
    expect(rowOf(r, 'model-img-hd').cells[0].text).toBe('≈ 500 次');
  });

  it('按量计费模型按万 tokens 显示', () => {
    const r = buildPlanComparisonRows(
      { models: [gpt], plans: [column({ coverage: { 'gpt-5': covered() } })] },
      plans,
    );
    // 10 美元 / (2.5 × 2 美元每 1M) = 2M tokens = 200 万
    expect(rowOf(r, 'model-gpt-5').cells[0].text).toBe('约 200 万 tokens');
  });

  it('限渠道的格子要带标记', () => {
    const r = buildPlanComparisonRows(
      {
        models: [img],
        plans: [
          column({
            coverage: { 'img-hd': covered({ channel_limited: true }) },
          }),
        ],
      },
      plans,
    );
    expect(rowOf(r, 'model-img-hd').cells[0].channelLimited).toBe(true);
  });

  // 次数上限是硬承诺，与估算分段；某个套餐没有这条限制时显示 —
  it('次数上限按模型范围归行，跨套餐对齐', () => {
    const r = buildPlanComparisonRows(
      {
        models: [],
        plans: [
          column({
            limits: [
              { models: 'gpt-5', limit_count: 100, reset_period: 'monthly' },
            ],
          }),
          column({
            plan_id: 2,
            limits: [
              { models: 'gpt-5', limit_count: 500, reset_period: 'monthly' },
              { models: 'a,b', limit_count: 20, reset_period: 'never' },
            ],
          }),
        ],
      },
      plans,
    );
    // 两个套餐都限了 gpt-5，只能出一行，否则跨套餐就对不齐了
    expect(r.rows.filter((x) => x.kind === 'limit')).toHaveLength(2);
    const gptRow = rowOf(r, 'limit-gpt-5');
    expect(gptRow.kind).toBe('limit');
    expect(gptRow.cells.map((c) => c.text)).toEqual(['100 次/月', '500 次/月']);
    const abRow = rowOf(r, 'limit-a,b');
    expect(abRow.label).toBe('a、b');
    expect(abRow.cells.map((c) => c.text)).toEqual(['—', '20 次']);
  });

  it('限渠道的次数上限也要带标记', () => {
    const r = buildPlanComparisonRows(
      {
        models: [],
        plans: [
          column({
            limits: [
              {
                models: 'gpt-5',
                limit_count: 100,
                reset_period: 'monthly',
                channel_limited: true,
              },
            ],
          }),
        ],
      },
      plans,
    );
    expect(rowOf(r, 'limit-gpt-5').cells[0].channelLimited).toBe(true);
  });

  // 只配了权益、没配算力点的套餐：点数行写 —，不写 0
  it('无算力点的套餐点数行显示 —', () => {
    const r = buildPlanComparisonRows(
      {
        models: [],
        plans: [column(), column({ plan_id: 2, compute_points_per_period: 0 })],
      },
      plans,
    );
    expect(rowOf(r, 'points').cells[1].text).toBe('—');
  });
});
