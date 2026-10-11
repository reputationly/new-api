/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import i18next from 'i18next';

// ---- quota ↔ 展示币种的换算率 ----
//
// 这几个值正常情况下都由 /api/status 在页面渲染前写入 localStorage，下面的 fallback
// 只在读不到时生效（例如直接打开某个子页面、localStorage 被单独清理）。全站金额都按这些
// 换算率算，取错就是系统性错价，所以 fallback 只在 console 里告警一次，不刷屏。
//
// quota_per_unit 是「1 美元对应的 quota」，只留给美元语义的地方（余额/充值/令牌额度
// 这类按美元记数的旧数据）。人民币金额一律用 quota_per_yuan，**不再乘汇率**。
export const FALLBACK_QUOTA_PER_UNIT = 730000000; // 与后端 QuotaPerUnit 的出厂默认一致
export const FALLBACK_QUOTA_PER_YUAN = 100000000; // 1 元 = 10⁸ quota（后端 QuotaPerYuan）
export const FALLBACK_QUOTA_PER_POINT = 1000000;

const warnedFallbacks = new Set();

function readPositiveNumber(key) {
  const raw = parseFloat(localStorage.getItem(key) || '');
  return Number.isFinite(raw) && raw > 0 ? raw : null;
}

function warnQuotaFallback(key, fallback) {
  if (warnedFallbacks.has(key)) return;
  warnedFallbacks.add(key);
  console.warn(
    `localStorage 缺少可用的 ${key}（应由 /api/status 写入），暂按 ${fallback} 换算金额展示`,
  );
}

export const getQuotaPerUnit = () => {
  const raw = readPositiveNumber('quota_per_unit');
  if (raw !== null) return raw;
  warnQuotaFallback('quota_per_unit', FALLBACK_QUOTA_PER_UNIT);
  return FALLBACK_QUOTA_PER_UNIT;
};

export const getQuotaPerYuan = () => {
  const raw = readPositiveNumber('quota_per_yuan');
  if (raw !== null) return raw;
  warnQuotaFallback('quota_per_yuan', FALLBACK_QUOTA_PER_YUAN);
  return FALLBACK_QUOTA_PER_YUAN;
};

/**
 * 站点汇率（1 美元 = X 元）。只有两处该读它：
 *   1. 展示币种换算（getDisplayCurrency，USD/CUSTOM 模式要把人民币折回美元）；
 *   2. 历史日志/历史表达式里的美元口径定价字段（normalizeLogPricing）。
 * 计费与定价配置一律不读它——它们已经是人民币。
 */
export const getUsdExchangeRate = () => {
  try {
    const status = JSON.parse(localStorage.getItem('status') || '{}');
    const rate = Number(status?.usd_exchange_rate);
    return Number.isFinite(rate) && rate > 0 ? rate : 7.3;
  } catch (e) {
    return 7.3;
  }
};

/**
 * 当前展示币种的配置。
 *
 * 基准是**人民币**：`人民币金额 = quota / quota_per_yuan`，再按 ratePerCny 折成展示币种。
 *   - CNY    → 1（原样显示人民币）
 *   - USD    → 1 / 汇率
 *   - CUSTOM → 自定义汇率 / 汇率（自定义汇率原本是「1 美元 = X 自定义币」）
 *   - TOKENS → 金额不折算（调用方直接显示 quota 原值），symbol 只作占位。
 *
 * @returns {{symbol: string, ratePerCny: number, type: string}}
 */
export const getDisplayCurrency = () => {
  const type = localStorage.getItem('quota_display_type') || 'USD';
  let status = {};
  try {
    status = JSON.parse(localStorage.getItem('status') || '{}') || {};
  } catch (e) {}
  const usdRate = getUsdExchangeRate();

  if (type === 'CNY') {
    return { symbol: '¥', ratePerCny: 1, type };
  }
  if (type === 'CUSTOM') {
    const customRate = Number(status?.custom_currency_exchange_rate);
    return {
      symbol: status?.custom_currency_symbol || '¤',
      ratePerCny:
        (Number.isFinite(customRate) && customRate > 0 ? customRate : 1) /
        usdRate,
      type,
    };
  }
  return { symbol: '$', ratePerCny: 1 / usdRate, type };
};

// ---- 积分换算（内部 quota unit ↔ 展示积分数） ----

export const getQuotaPerPoint = () => {
  const raw = readPositiveNumber('quota_per_point');
  if (raw !== null) return raw;
  warnQuotaFallback('quota_per_point', FALLBACK_QUOTA_PER_POINT);
  return FALLBACK_QUOTA_PER_POINT;
};

// 后台「积分设置」里换算率输入框的说明文案：写死的数字会在单位/汇率变动后变成误导
// （管理员很可能照着填回去），所以显示当前生效值。
export const quotaPerPointHintText = () =>
  i18next.t(
    '0 = 自动（1 积分 = 1 分钱）；当前生效 {{value}}，上线后不建议修改',
    {
      value: getQuotaPerPoint(),
    },
  );

export const isPointsEnabled = () =>
  localStorage.getItem('points_enabled') === 'true';

// quota unit -> 积分数（floor 取整，积分对用户不显示小数）。
// 1e-9 为浮点护栏：整数 quota 的相邻比值间距 ≈1/qpp 远大于该值，不会虚增，
// 只防止除法在整数边界向下抖动（如 ceil 发放后的精确往返被浮点误差打破）
export const quotaToPoints = (quota) => {
  const q = Number(quota || 0);
  if (!Number.isFinite(q) || q <= 0) return 0;
  return Math.floor(q / getQuotaPerPoint() + 1e-9);
};

// 积分数 -> quota unit（ceil 取整，用于提交给后端）。与后端 PointsToQuota 一致：
// 向上取整保证 quotaToPoints(pointsToQuota(n)) === n 精确往返，差额 <1 quota unit 让利用户
export const pointsToQuota = (points) => {
  const p = Number(points || 0);
  if (!Number.isFinite(p) || p <= 0) return 0;
  return Math.ceil(p * getQuotaPerPoint());
};

// ---- 算力点换算（内部 quota unit ↔ 展示算力点数） ----
//
// 与积分完全同构，取整方向也必须一致：展示侧 floor、提交侧 ceil。
// 套餐里存的 compute_points_per_period 是 quota unit，编辑页显示的是点数，
// 两边换算若用不同取整方向，会出现「配 50000 点、存完再打开变 49999」。

export const getQuotaPerComputePoint = () => {
  const raw = readPositiveNumber('quota_per_compute_point');
  if (raw !== null) return raw;
  warnQuotaFallback('quota_per_compute_point', FALLBACK_QUOTA_PER_POINT);
  return FALLBACK_QUOTA_PER_POINT;
};

export const quotaToComputePoints = (quota) => {
  const q = Number(quota || 0);
  if (!Number.isFinite(q) || q <= 0) return 0;
  return Math.floor(q / getQuotaPerComputePoint() + 1e-9);
};

export const computePointsToQuota = (points) => {
  const p = Number(points || 0);
  if (!Number.isFinite(p) || p <= 0) return 0;
  return Math.ceil(p * getQuotaPerComputePoint());
};

export const quotaToDisplayAmount = (quota) => {
  const q = Number(quota || 0);
  if (!Number.isFinite(q) || q === 0) return 0;
  const sign = Math.sign(q);
  const abs = Math.abs(q);
  const { type, ratePerCny } = getDisplayCurrency();
  if (type === 'TOKENS') return q;
  const cny = abs / getQuotaPerYuan();
  if (type === 'CNY') return sign * cny;
  return sign * cny * (ratePerCny || 1);
};

export const displayAmountToQuota = (amount) => {
  const val = Number(amount || 0);
  if (!Number.isFinite(val) || val === 0) return 0;
  const sign = Math.sign(val);
  const abs = Math.abs(val);
  const { type, ratePerCny } = getDisplayCurrency();
  if (type === 'TOKENS') return Math.round(val);
  const cny = type === 'CNY' ? abs : abs / (ratePerCny || 1);
  return sign * Math.round(cny * getQuotaPerYuan());
};
