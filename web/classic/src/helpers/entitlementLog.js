/**
 * 使用日志里的套餐权益信息（设计文档 §8.4、§10.3）。classic 与 mobile 共用，
 * 不引任何 UI / 渲染依赖。
 *
 * 数据来自日志 other（后端 appendEntitlementInfo 写入）：
 *   套餐内：billing_source=entitlement + entitlement_plan_title / compute_points /
 *           entitlement_limit_count / entitlement_used_count
 *   超额：  entitlement_fallback = {reason, plan_title, points_needed, points_available, limit_count}
 *
 * 点数读落库时的展示值（compute_points），不按当前换算率现算：换算率可调，现算会让
 * 历史日志的数字整体变化。
 */

const planName = (title) => title || '套餐';

// 超额只会落在两种来源上（后端 appendEntitlementInfo 的白名单）：纯余额，或积分优先
// 再扣余额的混扣。后者写「按账户余额计费」会让用户对不上积分为什么少了。
const chargedBy = (billingSource) =>
  billingSource === 'points_wallet' ? '按积分与账户余额计费' : '按账户余额计费';

/** 超额原因的一句话说明。用户问「买了套餐怎么还扣钱」时，这句就是答案。 */
export const overageText = (fb, billingSource) => {
  if (!fb) return '';
  const title = planName(fb.plan_title);
  const charged = chargedBy(billingSource);
  if (fb.reason === 'count_exhausted') {
    const limit = Number(fb.limit_count) || 0;
    return `「${title}」本期次数已用尽${limit > 0 ? `（上限 ${limit} 次）` : ''}，本次${charged}`;
  }
  if (fb.reason === 'points_insufficient') {
    return `本次需 ${Number(fb.points_needed) || 0} 算力点，「${title}」剩余 ${Number(fb.points_available) || 0} 点不足，本次${charged}`;
  }
  if (fb.reason === 'rate_limited') {
    const rpm = Number(fb.rate_limit_rpm) || 0;
    return `超出「${title}」每分钟 ${rpm} 次的速率上限，本次${charged}`;
  }
  return `未能使用「${title}」，本次${charged}`;
};

/**
 * 从日志 other 里取套餐信息。
 * @returns {null | {kind:'entitlement', planTitle, points, limit, used, remain}
 *                 | {kind:'overage', planTitle, text}}
 */
export const getEntitlementLogInfo = (other) => {
  if (!other) return null;
  if (other.billing_source === 'entitlement') {
    const limit = Number(other.entitlement_limit_count) || 0;
    const used = Number(other.entitlement_used_count) || 0;
    return {
      kind: 'entitlement',
      planTitle: planName(other.entitlement_plan_title),
      points: Number(other.compute_points) || 0,
      limit,
      used,
      remain: limit > 0 ? Math.max(limit - used, 0) : null,
    };
  }
  const fb = other.entitlement_fallback;
  if (fb && fb.reason) {
    return {
      kind: 'overage',
      planTitle: planName(fb.plan_title),
      text: overageText(fb, other.billing_source),
    };
  }
  return null;
};

/** 套餐内那笔的标签文案：用户更关心「这笔算在哪个套餐头上」，所以写套餐名而非「算力点」。 */
export const entitlementTagText = (info) =>
  info.points > 0
    ? `${info.planTitle} ${info.points.toLocaleString('en-US')}点`
    : `${info.planTitle} 免费`;
