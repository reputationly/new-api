import { buildCreditSummary } from './creditDisplay';

/**
 * 钱包页顶部的汇总。classic 与 mobile 共用，不引任何 UI / 渲染依赖。
 *
 * 页面要先回答「我现在还能用多少」，再回答「我欠多少」，最后才是「我用了多少」。
 * 三个钱包（余额 / 积分 / 授信）各自一个数字的话，用户得自己加起来才知道能不能
 * 继续调用；而后端预扣检查看的正是它们的和（service/billing_session.go：
 * available = quota + points + creditAvailable），所以这里给出同一口径的总数。
 *
 * 授信用户的负余额是在途透支，已经被 buildCreditSummary 折进「已用」，所以余额
 * 一格截到 0、总额里也只按 0 计——同一笔欠款只出现一次。两边算出的总数与后端
 * 恒等：后端 quota(负) + (limit − used)，这里 0 + (limit − used − (−quota))。
 *
 * 普通用户余额被打负是估算不准造成的欠费，不是授信：总额按 quota + points 截到 0，
 * 欠费单独给出，让页面挂标签。
 *
 * @param {object} user  /api/user/self 的 user
 * @param {{pointsEnabled: boolean}} opts
 * @returns {{
 *   available: number,        // 可用总额（quota unit）
 *   balance: number,          // 账户余额；授信用户截到 0
 *   points: number,           // 积分余额（quota unit）；未启用积分或子账户为 0
 *   showPoints: boolean,      // 是否展示积分一项
 *   creditAvailable: number,  // 授信可用；未开授信为 0
 *   credit: null | {limit:number, used:number, available:number, over:number},
 *   overdue: number,          // 普通用户的欠费额；授信用户恒为 0（欠款在 credit.used）
 * }}
 */
export const buildWalletSummary = (user, { pointsEnabled = false } = {}) => {
  const quota = Number(user?.quota) || 0;
  const isSubAccount = (Number(user?.parent_user_id) || 0) > 0;
  const showPoints = Boolean(pointsEnabled) && !isSubAccount;
  const points = showPoints
    ? Math.max(Number(user?.points_balance) || 0, 0)
    : 0;
  const credit = buildCreditSummary(user);

  if (credit) {
    const balance = Math.max(quota, 0);
    return {
      available: balance + points + credit.available,
      balance,
      points,
      showPoints,
      creditAvailable: credit.available,
      credit,
      overdue: 0,
    };
  }
  return {
    available: Math.max(quota + points, 0),
    balance: quota,
    points,
    showPoints,
    creditAvailable: 0,
    credit: null,
    overdue: Math.max(-quota, 0),
  };
};
