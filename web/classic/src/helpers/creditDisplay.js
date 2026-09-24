/**
 * 用户侧的授信展示。classic 与 mobile 共用，不引任何 UI / 渲染依赖。
 *
 * 授信客户的余额可以是 0 却照样能调用（透支自动记欠款），不展示的话客户看不懂
 * 「没钱为什么还能用」，也不知道自己欠了多少。
 *
 * 欠款允许超过上限（结算时服务已交付，超出的那笔照样记欠款、下一次请求被拒，
 * 见 model.SettleOverdraftToCredit），所以「可用」要截到 0，超出部分单独给出。
 *
 * 负余额也是欠款：请求在途（预扣已扣、尚未结算）时透支先落在 quota 上，结算后才
 * 挪进 credit_used。只看 credit_used 会高估可用额，与后端拦截口径
 * （quota − x >= −(limit − used)）对不上。
 *
 * @returns {null | {limit:number, used:number, available:number, over:number}} 单位 quota
 */
export const buildCreditSummary = (user) => {
  const limit = Number(user?.credit_limit) || 0;
  if (limit <= 0) return null;
  // 子账户不参与计费（恒为只读视图），额度与欠款都挂在企业主账户上
  if ((Number(user?.parent_user_id) || 0) > 0) return null;
  const overdraft = Math.max(-(Number(user?.quota) || 0), 0);
  const used = Math.max(Number(user?.credit_used) || 0, 0) + overdraft;
  return {
    limit,
    used,
    available: Math.max(limit - used, 0),
    over: Math.max(used - limit, 0),
  };
};
