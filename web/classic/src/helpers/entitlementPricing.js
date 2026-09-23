/**
 * 模型广场的套餐权益展示。设计见 docs/subscription-entitlement-design.md §8.2。
 *
 * 价格显示**按用户身份自适应**：
 *   未登录 / 无套餐         → 原分组价（现状不变，完全不出现算力点）
 *   有套餐且模型被覆盖      → 原价不动，其后追加「套餐内 N 算力点」
 *                             （算力点烧完就按原价扣，原价是用户最需要知道的数）
 *   有套餐但模型未被覆盖    → 与无套餐一致
 *
 * 后端只回「覆盖了什么」，价格在这里由同一份倍率数据算出——不让后端再算一遍价，
 * 否则页面上的价和实际扣费会分叉，而分叉那天没有任何报错。
 *
 * 本模块刻意不引任何 UI / 渲染依赖：mobile 端直接复用，引了 render.jsx 就会把
 * Semi 整个打进移动端包。
 */

const RESET_PERIOD_LABEL = {
  daily: '天',
  weekly: '周',
  monthly: '月',
  custom: '周期',
};

/** 「500 次/月」。不重置的权益不编造周期，只写「500 次」。 */
export const formatCountLimit = (count, resetPeriod) => {
  const unit = RESET_PERIOD_LABEL[resetPeriod];
  return unit ? `${count} 次/${unit}` : `${count} 次`;
};

/**
 * 取某个模型的权益覆盖。无覆盖返回 null。
 *
 * coverage 为 null 表示未登录或无活跃套餐——后端对「有套餐但一个模型都没覆盖」
 * 也返回 null，两者在展示上本就是同一件事。
 */
export const getModelCoverage = (coverage, modelName) => {
  if (!coverage || !modelName) return null;
  return coverage[modelName] || null;
};

/**
 * 生成该模型要显示的角标。
 *
 * 顺序固定：套餐名 → 免费 / 次数上限 → 限渠道。套餐名放最前是因为用户扫一眼
 * 最想确认的是「这个在不在我的套餐里」。
 */
export const buildEntitlementBadges = (coverage) => {
  if (!coverage) return [];
  const badges = [];
  if (coverage.plan_title) {
    badges.push({ key: 'plan', color: 'violet', text: coverage.plan_title });
  }
  if (!coverage.consume_points) {
    badges.push({ key: 'free', color: 'green', text: '不消耗算力点' });
  } else if (Number(coverage.limit_count) > 0) {
    badges.push({
      key: 'limit',
      color: 'teal',
      text: formatCountLimit(coverage.limit_count, coverage.reset_period),
    });
  }
  // 限渠道的覆盖是**有条件的**：展示侧不知道请求会落到哪个渠道。不标的话用户会
  // 以为这个价必然拿得到，实际被路由到别的渠道时却按原价扣了。
  if (coverage.channel_limited) {
    badges.push({ key: 'channel', color: 'amber', text: '限特定渠道' });
  }
  return badges;
};

/**
 * 多条权益覆盖时的说明文案。
 *
 * 用户持有多个套餐时最想知道的正是「为什么是这个价」，只给一个数字解释不了。
 * 同一个套餐也可能出现两条：排在前面的限了渠道，请求落到别的渠道时扣费会跳过它、
 * 走后面那条。只有一条时返回空串——那时没什么要解释的，多一行字只是噪音。
 *
 * 「当前走哪个」取顶层字段而不是 all[0]：后端把顶层定为请求落在不限渠道时扣费
 * 命中的那条，all[0] 可能是只在特定渠道生效的那条。
 */
export const buildCoverageTooltip = (coverage) => {
  const all = coverage?.all;
  if (!Array.isArray(all) || all.length <= 1) return '';
  const planCount = new Set(all.map((e) => e.plan_id)).size;
  const lines = all.map((e) => {
    const how = e.consume_points
      ? `${Number(e.discount) === 1 ? '原价' : `${e.discount} 折扣`}`
      : '不消耗算力点';
    const where = e.channel_limited ? '（限特定渠道）' : '';
    return `${e.plan_title || '未命名套餐'}${where}：${how}`;
  });
  const current = coverage.plan_title || '未命名套餐';
  return [
    planCount > 1
      ? `本模型被 ${planCount} 个套餐覆盖：`
      : '本模型在套餐内按渠道计费不同：',
    ...lines,
    planCount > 1
      ? `实际扣费按套餐到期先后，当前走「${current}」`
      : `当前走「${current}」`,
    ...(all.some((e) => e.channel_limited)
      ? ['限特定渠道的条目只在请求落到对应渠道时生效']
      : []),
  ].join('\n');
};

/** 剩余次数。不限次返回 null（调用方据此不显示）。 */
export const remainingCount = (coverage) => {
  if (!coverage) return null;
  const limit = Number(coverage.limit_count);
  if (!Number.isFinite(limit) || limit <= 0) return null;
  const used = Number(coverage.used_count) || 0;
  return Math.max(limit - used, 0);
};
