/**
 * 分组折扣的展示口径。纯计算、不引 UI 依赖，手机端可直接 import
 * （同 priceFormat.js / videoMatrix.js 的约定）——两端各抄一份必然漂移，
 * 而这里一旦漂移，用户在两个端会看到不同的折扣数字。
 */

/**
 * 把最终倍率翻译成折扣标签。
 *
 * 入参是后端算好的 Final（分组倍率 × 模型折扣，见 controller/pricing.go 的
 * resolveGroupModelRatio），与计费、日志用的是同一个数，所以标签上的折扣就是
 * 用户真实付的折扣。
 *
 * 返回 null 表示不该显示标签：
 *   ratio >= 1  —— 没打折（新型号原价，标个「10折」是噪音）
 *   ratio < 0 / 非有限数 —— 倍率异常，宁可不显示也不能显示错的
 *
 * 配色按力度分三档，都用浅底：模型广场一屏几十个模型，实心色块会盖过模型名本身。
 */
export const getGroupDiscountInfo = (ratio) => {
  // 先挡 null / undefined / 空串：Number(null) 和 Number('') 都是 0，
  // 会一路走到下面的「免费」分支——倍率没下发却显示免费，是最坏的一种错。
  if (ratio === null || ratio === undefined || ratio === '') return null;
  const r = Number(ratio);
  if (!Number.isFinite(r) || r >= 1 || r < 0) return null;
  if (r === 0) {
    return { text: '免费', color: 'red', ratio: r };
  }
  // 0.85 -> 8.5折；0.5 -> 5折（整数不拖 .0）
  const tenths = Math.round(r * 1000) / 100;
  const text = `${Number.isInteger(tenths) ? tenths : tenths.toFixed(1)}折`;
  const color = r <= 0.7 ? 'red' : r <= 0.9 ? 'orange' : 'amber';
  return { text, color, ratio: r };
};

/**
 * 折扣标签的展示色 -> 手机端色值。
 *
 * antd-mobile 没有 Semi 那套 color token，两端各挑各的颜色会导致同一个折扣
 * 在两个端显示成不同颜色。这里把映射固定下来。
 */
export const DISCOUNT_HEX = {
  red: { bg: '#fff1f0', fg: '#cf1322' },
  orange: { bg: '#fff7e6', fg: '#d46b08' },
  amber: { bg: '#fffbe6', fg: '#ad8b00' },
};
