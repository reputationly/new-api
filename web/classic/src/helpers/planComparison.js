import { quotaToComputePoints } from './quota';
import {
  previewComputePoints,
  formatPreviewAmount,
} from './computePointPreview';
import { formatCountLimit } from './entitlementPricing';

/**
 * 套餐对比表（对外换算表）的行构造。设计见 docs/subscription-entitlement-design.md §8.3。
 *
 * 表里有两类性质不同的数字，必须分段、不能混排：
 *   - 估算：点数换成秒数 / 张数 / tokens，参数不同实际用量会有出入；
 *   - 硬上限：权益的次数闸门，是真实承诺。
 *
 * 覆盖判定（哪个套餐覆盖哪个模型）由后端用扣费侧的 MatchesModel 算好送来，
 * 这里只做换算与排版，不重写通配匹配。
 */

const EMPTY = '—';

/** tokens 量级大，「约 1,234,567 tokens」读不出多少；按万 / 亿取整。 */
const formatTokens = (n) => {
  if (n >= 1e8) return `约 ${Math.floor(n / 1e8)} 亿 tokens`;
  if (n >= 1e4) return `约 ${Math.floor(n / 1e4)} 万 tokens`;
  return `约 ${formatPreviewAmount(n)} tokens`;
};

const estimateCell = (column, record) => {
  const cov = column.coverage?.[record.model_name];
  // 没覆盖 = 点数花不到这个模型上。换算出数字就是虚假宣传。
  if (!cov) return { text: EMPTY };
  if (!cov.consume_points) {
    return { text: '不消耗算力点', channelLimited: cov.channel_limited };
  }
  const points = quotaToComputePoints(column.compute_points_per_period);
  if (points <= 0) return { text: EMPTY };
  // 折扣作用在消耗侧：×0.5 就是同样的点数能用两倍的量
  const discount = Number(cov.discount) > 0 ? Number(cov.discount) : 1;
  const r = previewComputePoints(points / discount, record);
  if (r.kind === 'unknown') return { text: EMPTY };
  return {
    text:
      r.kind === 'tokens'
        ? formatTokens(r.amount)
        : `≈ ${formatPreviewAmount(r.amount)} ${r.unit}`,
    channelLimited: cov.channel_limited,
  };
};

/**
 * @param comparison 后端 /api/subscription/plans/comparison 的 data
 * @param plans      用户侧套餐列表（[{plan}]），用来取列标题
 * @returns {{columns: {planId:number,title:string}[], rows: object[]}|null}
 */
export const buildPlanComparisonRows = (comparison, plans = []) => {
  const cols = comparison?.plans;
  if (!Array.isArray(cols) || cols.length === 0) return null;

  const titleOf = new Map(
    (plans || []).map((p) => [p?.plan?.id, p?.plan?.title || '']),
  );
  const columns = cols.map((c) => ({
    planId: c.plan_id,
    title: titleOf.get(c.plan_id) || `套餐 #${c.plan_id}`,
  }));

  const rows = [];
  if (cols.some((c) => Number(c.compute_points_per_period) > 0)) {
    rows.push({
      key: 'points',
      kind: 'points',
      label: '每期算力点',
      cells: cols.map((c) => {
        const p = quotaToComputePoints(c.compute_points_per_period);
        return { text: p > 0 ? formatPreviewAmount(p) : EMPTY };
      }),
    });
  }

  (comparison.models || []).forEach((record) => {
    rows.push({
      key: `model-${record.model_name}`,
      kind: 'estimate',
      label: record.model_name,
      cells: cols.map((c) => estimateCell(c, record)),
    });
  });

  // 硬上限按「模型范围」原文归行，首次出现的顺序即行序
  const limitKeys = [];
  cols.forEach((c) =>
    (c.limits || []).forEach((l) => {
      if (!limitKeys.includes(l.models)) limitKeys.push(l.models);
    }),
  );
  limitKeys.forEach((models) => {
    rows.push({
      key: `limit-${models}`,
      kind: 'limit',
      label: models.split(',').join('、'),
      cells: cols.map((c) => {
        const l = (c.limits || []).find((x) => x.models === models);
        return {
          text: l ? formatCountLimit(l.limit_count, l.reset_period) : EMPTY,
          channelLimited: !!l?.channel_limited,
        };
      }),
    });
  });

  // 只有表头、没有任何一行时不出表
  if (rows.length === 0) return null;
  return { columns, rows };
};
