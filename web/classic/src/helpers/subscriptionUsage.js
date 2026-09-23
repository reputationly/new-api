/**
 * 套餐余量展示（设计文档 §10.2）。classic 与 mobile 共用。
 *
 * 本模块刻意不引任何 UI / 渲染依赖（包括 quota.js，它会带进 render.jsx）：
 * 点数换算由调用方传入，mobile 复用时就不会把 Semi 打进移动端包。
 *
 * 数据来自 /api/subscription/self 的活跃订阅：compute_points（quota unit）与
 * entitlements（本期计数器）。
 */

// 余量低于这个比例时标橙并提示「用尽后将按账户余额计费」
export const LOW_USAGE_RATIO = 0.2;

const RESET_PERIOD_LABEL = {
  daily: '天',
  weekly: '周',
  monthly: '月',
  custom: '周期',
};

const modelsLabel = (models) =>
  String(models || '')
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean)
    .join('、');

/**
 * 生成一个订阅的余量行。
 *
 * @param summary       /api/subscription/self 里的一项（{subscription, compute_points, entitlements}）
 * @param toPoints      quota → 展示点数（classic 传 quotaToComputePoints）
 * @returns {Array<{
 *   key: string, label: string, kind: 'points'|'count'|'unlimited',
 *   used?: number, total?: number, remain?: number, ratio?: number,
 *   resetAt?: number, resetKind?: 'reset'|'expire', low: boolean, note?: string,
 * }>}
 */
export const buildUsageRows = (summary, toPoints) => {
  if (!summary) return [];
  const rows = [];
  const sub = summary.subscription || {};

  const cp = summary.compute_points;
  if (cp && Number(cp.total) > 0) {
    const total = toPoints(Number(cp.total));
    const remain = toPoints(Number(cp.available));
    const ratio = total > 0 ? remain / total : 0;
    // 点数随订阅重置发新批次、旧批次作废；不重置的套餐则在批次到期（订阅结束）时作废
    const nextReset = Number(sub.next_reset_time) || 0;
    rows.push({
      key: 'points',
      label: '算力点',
      kind: 'points',
      used: total - remain,
      total,
      remain,
      ratio,
      resetAt: nextReset > 0 ? nextReset : Number(cp.expires_at) || 0,
      resetKind: nextReset > 0 ? 'reset' : 'expire',
      low: ratio < LOW_USAGE_RATIO,
    });
  }

  (summary.entitlements || []).forEach((e) => {
    const label = modelsLabel(e.models);
    const limit = Number(e.limit_count) || 0;
    if (limit <= 0) {
      // 不限次不等于没有约束：速率限制是唯一兜底，要让用户知道
      const parts = [];
      if (Number(e.rate_limit_rpm) > 0) parts.push(`${e.rate_limit_rpm} RPM`);
      if (!e.consume_points) parts.push('不消耗算力点');
      if (e.channel_limited) parts.push('限特定渠道');
      rows.push({
        key: `ent-${e.entitlement_id}`,
        label,
        kind: 'unlimited',
        low: false,
        note: parts.join(' · '),
      });
      return;
    }
    const used = Math.min(Number(e.used_count) || 0, limit);
    const remain = limit - used;
    const ratio = remain / limit;
    const unit = RESET_PERIOD_LABEL[e.reset_period];
    rows.push({
      key: `ent-${e.entitlement_id}`,
      label,
      kind: 'count',
      used,
      total: limit,
      remain,
      ratio,
      resetAt: Number(e.next_reset_time) || 0,
      resetKind: 'reset',
      low: ratio < LOW_USAGE_RATIO,
      note: [
        unit ? `每${unit}` : '',
        e.consume_points ? '' : '不消耗算力点',
        e.channel_limited ? '限特定渠道' : '',
      ]
        .filter(Boolean)
        .join(' · '),
    });
  });
  return rows;
};

/**
 * 余量不足时的提示文案。每条低余量行一句，设计文档 §10.1：「超出部分按账户余额
 * 计费」是防客诉的关键——用户要在用完之前就知道用完之后会发生什么。
 */
export const buildLowUsageWarnings = (rows) =>
  rows
    .filter((r) => r.low)
    .map((r) =>
      r.kind === 'points'
        ? `算力点剩余 ${r.remain.toLocaleString('en-US')}，用尽后将按账户余额计费`
        : `${r.label} 剩余 ${r.remain} 次，用尽后将按账户余额计费`,
    );
