import { getQuotaPerUnit } from './quota';

/**
 * 套餐售价 price_amount 就是**实付人民币**：支付宝 / 微信直连按它的元数下单，
 * 易支付也原样当人民币提交，资金流水同样按元入账。它不是美元，展示时不能再乘
 * 汇率——曾经卡片上写 ¥73、扫码实付 ¥10，差的正是 7.3 倍。
 */
export const formatCNY = (amount, digits = 2) =>
  `¥${(Number(amount) || 0).toFixed(digits)}`;

// quota → 人民币：quota / QuotaPerUnit 得美元，再乘站点美元汇率
export const quotaToCNY = (quota) => {
  let rate = 7.3;
  try {
    const status = JSON.parse(localStorage.getItem('status') || '{}');
    rate = Number(status?.usd_exchange_rate) || 7.3;
  } catch (e) {}
  return ((Number(quota) || 0) / getQuotaPerUnit()) * rate;
};

// 外采权益最坏成本是否超过套餐售价：两边都折成人民币再比；售价未填（0）不判断
export const isWorstCostOverPrice = (worstQuota, priceAmount) => {
  const price = Number(priceAmount) || 0;
  return price > 0 && quotaToCNY(worstQuota) > price;
};
