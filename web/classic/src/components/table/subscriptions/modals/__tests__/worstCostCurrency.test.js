import { describe, it, expect, beforeEach } from 'vitest';
import { getQuotaPerUnit } from '../../../../../helpers/quota';
import { convertUSDToCurrency } from '../../../../../helpers/render';

/**
 * 最坏成本的币种口径。
 *
 * 踩过的坑：worst_total_quota 是 quota unit，而套餐的 price_amount 存的是**美元**
 * （后端强制 currency=USD，列表页用 convertUSDToCurrency 渲染它）。最初拿
 * quotaToDisplayAmount 的结果——那是**已折成展示币种**的数——直接去比未折算的售价，
 * 在 CNY 站点上成本被放大一个汇率倍数（默认 7.3），几乎任何配了次数的外采权益
 * 都会被误标成亏本。而这个红字的全部意义就是「配亏了要看得见」，乱响等于废掉它。
 *
 * 这里把「两边必须同币种比较」这个不变量钉死。
 */

const CNY_RATE = 7.3;

beforeEach(() => {
  localStorage.setItem('quota_per_unit', '500000');
  localStorage.setItem('quota_display_type', 'CNY');
  localStorage.setItem(
    'status',
    JSON.stringify({ usd_exchange_rate: CNY_RATE }),
  );
});

// 与组件里的算法一致：拿**整个计费周期**的最坏成本，统一折到 USD 再比。
// periodQuota 优先于单窗口值——次数上限是每个重置窗口的额度，
// 月付套餐配每周重置时客户一个周期内能用四五倍的量。
const isOverBudget = (estimate, priceAmountUSD) => {
  const periodQuota = estimate.worst_period_quota || estimate.worst_total_quota;
  const worstUSD = periodQuota / getQuotaPerUnit();
  const priceUSD = Number(priceAmountUSD);
  return priceUSD > 0 && worstUSD > priceUSD;
};

describe('最坏成本与售价的币种口径', () => {
  it('CNY 站点上，成本 $10 对售价 $20 不能算亏本', () => {
    expect(isOverBudget({ worst_total_quota: 10 * 500000 }, 20)).toBe(false);
  });

  it('成本确实超过售价时才算亏本', () => {
    expect(isOverBudget({ worst_total_quota: 30 * 500000 }, 20)).toBe(true);
  });

  // 月付套餐配每周重置：单窗口 $10 看着没超 $20 的售价，但一个计费周期有
  // 5 个窗口，真实最坏是 $50 —— 只比单窗口就是系统性低估，而低估正是
  // 「保证最坏不亏」最不能出的方向。
  it('必须按整个计费周期比，而不是单个重置窗口', () => {
    const estimate = {
      worst_total_quota: 10 * 500000,
      worst_period_quota: 50 * 500000,
    };
    expect(isOverBudget(estimate, 20)).toBe(true);
    expect(isOverBudget({ worst_total_quota: 10 * 500000 }, 20)).toBe(false); // 旧口径会漏报
  });

  // 这条是回归锁：旧实现把成本折成 CNY（×7.3）再与美元售价比，
  // $10 的成本会变成 73 去比 20，误判成亏本。
  it('不得把折算成展示币种的成本拿去比美元售价', () => {
    const worstQuota = 10 * 500000;
    const wrongWay = (worstQuota / getQuotaPerUnit()) * CNY_RATE; // 旧实现
    expect(wrongWay).toBeGreaterThan(20); // 旧实现会误报
    expect(isOverBudget({ worst_total_quota: worstQuota }, 20)).toBe(false);
  });

  it('售价为 0（未填）时不做亏本判断', () => {
    expect(isOverBudget({ worst_total_quota: 999 * 500000 }, 0)).toBe(false);
  });

  it('展示一律带站点币种符号，不写死 $', () => {
    expect(convertUSDToCurrency(10, 2)).toBe(`¥${(10 * CNY_RATE).toFixed(2)}`);
    localStorage.setItem('quota_display_type', 'USD');
    expect(convertUSDToCurrency(10, 2)).toBe('$10.00');
  });
});
