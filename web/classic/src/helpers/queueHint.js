/**
 * 排队回显文案：把门面给的 queue_ahead / estimated_start_seconds 折成一句人话。
 *
 * 四个体验区（视频 / 音乐 / 语音 / 图片）共用，因为它们面对的是同一个后端队列，
 * 文案口径不一致会让同一件事在不同页面上读起来像两回事。
 *
 * 关键约定 —— **说不准就闭嘴**：queue_ahead 为 null/undefined 时返回 null，调用方
 * 退回原来那句笼统的「任务排队中…」。门面在派发中、无运行实例、或老版本不带这个
 * 字段时都会回 null，这些情况下编一个位置出来比不说更糟。
 */

/**
 * ETA 上界系数。
 *
 * 延迟表里的数是**热态实测的最小值**（多实例路由下取 3 次最小值，最接近「热实例」），
 * 所以它系统性偏小，真实耗时只会更长、不会更短。给一个上界而不是报单点值，是因为
 * 单点值在这三个地方都会被打脸：
 *   1. 表是每模型一个常数，不区分载荷 —— ltx2.5-hd 出 5 秒和 15 秒差约 3 倍；
 *   2. 冷启动 —— indextts 实测首发 96s、稳态 15s；
 *   3. 队头那条已经跑了一半，我们按整条算。
 * 1.5 不是测出来的，是个保守的展示区间：宁可让用户觉得「比说的快」。
 */
export const QUEUE_ETA_SPREAD = 1.5;

/**
 * @param {number|null|undefined} queueAhead 前面还要跑完几次生成
 * @param {number|null|undefined} etaSeconds 预计还有多少秒轮到
 * @param {(s: string) => string} t i18n
 * @returns {string|null} 文案；null 表示「说不准」，调用方自行兜底
 */
export function formatQueueHint(queueAhead, etaSeconds, t) {
  if (typeof queueAhead !== 'number' || !Number.isFinite(queueAhead))
    return null;
  if (queueAhead <= 0) return t('即将开始…');

  const head = `${t('前面还有')} ${queueAhead} ${t('个任务')}`;
  const eta = formatEta(etaSeconds, t);
  return eta ? `${head} · ${eta}` : head;
}

/**
 * 秒 → 「预计约 X–Y 分钟」。不足一分钟不报区间：「约 0–1 分钟」既难看又没信息量。
 */
export function formatEta(etaSeconds, t) {
  if (typeof etaSeconds !== 'number' || !Number.isFinite(etaSeconds))
    return null;
  if (etaSeconds <= 0) return null;
  if (etaSeconds < 60) return t('预计 1 分钟内开始');

  const lo = Math.round(etaSeconds / 60);
  // 上界至少比下界大 1 分钟，否则四舍五入会把区间压成「约 5–5 分钟」。
  const hi = Math.max(lo + 1, Math.ceil((etaSeconds * QUEUE_ETA_SPREAD) / 60));
  return `${t('预计约')} ${lo}–${hi} ${t('分钟后开始')}`;
}
