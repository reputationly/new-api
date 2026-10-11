const FALLBACK_QUOTA_PER_POINT = 10000000;
const FALLBACK_QUOTA_PER_COMPUTE_POINT = 10000000;

// 额度展示换算，精简拷贝自 web/classic/src/helpers/render.jsx renderQuota
// （去掉 Semi 相关渲染，仅保留 USD/CNY/TOKENS/CUSTOM 数值逻辑）。

function readStatus() {
  try {
    const s = localStorage.getItem('status');
    return s ? JSON.parse(s) : {};
  } catch (e) {
    return {};
  }
}

export function pointsEnabled() {
  return localStorage.getItem('points_enabled') === 'true';
}

// 积分展示：quota → 积分。与 PC 端 helpers/quota.js quotaToPoints 完全同口径：
// floor 取整（积分不显示小数）+ 1e-9 浮点护栏 + quota_per_point 默认 10000000。
export function renderPoints(quota) {
  const raw = parseFloat(
    localStorage.getItem('quota_per_point') || String(FALLBACK_QUOTA_PER_POINT),
  );
  const per =
    Number.isFinite(raw) && raw > 0 ? raw : FALLBACK_QUOTA_PER_POINT;
  const q = Number(quota || 0);
  if (!Number.isFinite(q) || q <= 0) return '0';
  return String(Math.floor(q / per + 1e-9));
}

// 算力点展示：quota → 点数（数值）。与 PC 端 helpers/quota.js quotaToComputePoints
// 完全同口径：floor 取整 + 1e-9 浮点护栏 + quota_per_compute_point 默认 10000000。
// 不直接引 PC 那份：它带着 render.jsx，会把 Semi 打进移动端包。
export function quotaToComputePoints(quota) {
  const raw = parseFloat(
    localStorage.getItem('quota_per_compute_point') ||
      String(FALLBACK_QUOTA_PER_COMPUTE_POINT),
  );
  const per =
    Number.isFinite(raw) && raw > 0 ? raw : FALLBACK_QUOTA_PER_COMPUTE_POINT;
  const q = Number(quota || 0);
  if (!Number.isFinite(q) || q <= 0) return 0;
  return Math.floor(q / per + 1e-9);
}

// 大数字缩写，拷贝自 PC helpers/render.jsx renderNumber（TOKENS 展示模式用）
function renderNumber(num) {
  if (num >= 1000000000) {
    return (num / 1000000000).toFixed(1) + 'B';
  } else if (num >= 1000000) {
    return (num / 1000000).toFixed(1) + 'M';
  } else if (num >= 10000) {
    return (num / 1000).toFixed(1) + 'k';
  }
  return `${num}`;
}

// 费率兜底与 PC 端 helpers/quota.js 保持一致：1 元 = 10⁹ quota，1 美元 = 汇率 × 10⁹。
const FALLBACK_QUOTA_PER_YUAN = 1000000000;
const FALLBACK_USD_EXCHANGE_RATE = 7.3;

export function renderQuota(quota, digits = 2) {
  const quotaPerYuan =
    parseFloat(localStorage.getItem('quota_per_yuan')) ||
    FALLBACK_QUOTA_PER_YUAN;
  const quotaDisplayType = localStorage.getItem('quota_display_type') || 'USD';
  if (quotaDisplayType === 'TOKENS') {
    return renderNumber(quota);
  }
  // 汇率只影响 USD / CUSTOM 展示：ratePerCny 是「1 元 = X 展示币种」。
  const status = readStatus();
  const usdRate = Number(status?.usd_exchange_rate) || FALLBACK_USD_EXCHANGE_RATE;
  let symbol = '$';
  let ratePerCny = 1 / usdRate;
  if (quotaDisplayType === 'CNY') {
    symbol = '¥';
    ratePerCny = 1;
  } else if (quotaDisplayType === 'CUSTOM') {
    symbol = status?.custom_currency_symbol || '¤';
    ratePerCny = (Number(status?.custom_currency_exchange_rate) || 1) / usdRate;
  }
  const value = (quota / quotaPerYuan) * ratePerCny;
  return `${symbol}${value.toFixed(digits)}`;
}
