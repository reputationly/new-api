import { describe, it, expect, beforeEach } from 'vitest';
import {
  formatCNY,
  isWorstCostOverPrice,
  quotaToCNY,
} from '../../../../../helpers/planPrice';

/**
 * 最坏成本与套餐售价的币种口径。
 *
 * 套餐的 price_amount 是**实付人民币**（支付宝 / 微信直连按它的元数收款），
 * 最坏成本是 quota unit。两边必须折到同一币种（人民币）再比，否则差一个汇率
 * 倍数（默认 7.3）：要么把赚钱的套餐误报成亏本，要么把亏本的漏报——这个红字
 * 的全部意义就是「配亏了要看得见」。
 *
 * 这里把「两边都按人民币比较」这个不变量钉死。
 */

const QPY = 1000000000; // 1 元 = 10⁹ quota（后端 QuotaPerYuan）
// 人民币金额 → quota：¥x × quota_per_yuan（不再经汇率）
const yuan = (x) => x * QPY;

beforeEach(() => {
  localStorage.setItem('quota_per_yuan', String(QPY));
  localStorage.setItem('quota_display_type', 'CNY');
});

describe('最坏成本与售价的币种口径', () => {
  it('quota 直接按 1 元 = 10⁹ quota 折成人民币', () => {
    expect(quotaToCNY(QPY)).toBeCloseTo(1, 6);
    expect(quotaToCNY(QPY * 7.3)).toBeCloseTo(7.3, 6);
  });

  it('成本 ¥50 对售价 ¥99 不算亏本', () => {
    expect(isWorstCostOverPrice(yuan(50), 99)).toBe(false);
  });

  it('成本 ¥120 超过售价 ¥99 才算亏本', () => {
    expect(isWorstCostOverPrice(yuan(120), 99)).toBe(true);
  });

  // 回归锁：旧实现把售价当美元、成本折到美元再比。成本 $10（≈¥73）对售价
  // ¥20，旧口径得出 10 < 20「不亏」，实际上每卖一份亏 ¥53。
  it('不得把人民币售价当美元比', () => {
    const worst = 73 * QPY; // ¥73
    expect(quotaToCNY(worst)).toBeCloseTo(73, 6);
    expect(isWorstCostOverPrice(worst, 20)).toBe(true);
  });

  it('售价为 0（未填）时不做亏本判断', () => {
    expect(isWorstCostOverPrice(999 * QPY, 0)).toBe(false);
  });

  // 售价不随展示币种换算：切到 USD 展示也还是 ¥
  it('售价一律按人民币展示，不乘汇率', () => {
    expect(formatCNY(99)).toBe('¥99.00');
    localStorage.setItem('quota_display_type', 'USD');
    expect(formatCNY(99)).toBe('¥99.00');
    expect(isWorstCostOverPrice(yuan(120), 99)).toBe(true);
  });
});
