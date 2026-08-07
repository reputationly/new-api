// 模型广场的价格显示格式化。
//
// 单独成模块、不引任何 UI 依赖，是为了让手机端能**直接 import 而不是抄一份**：
// helpers/utils.jsx 会传染 Semi 等桌面依赖，在 mobile 的 vite 配置里被整模块换成
// src/shims/classic-utils.jsx，走那条路只能靠手抄同步。同款函数此前在
// web/mobile/src/pages/Models.jsx 里就有一份拷贝。

/**
 * 价格显示：固定两位小数，取整规则按「第一个有效数字落在第几位」分岔。
 *
 *   - 小数点后第 3 位就有数（≥ 0.001）→ **向上取整**。
 *     0.0073 → 0.01。显示成 0.00 会让人以为免费，宁可略高一点。
 *   - 第 4 位之后才有数（< 0.001）→ **四舍五入**（结果必为 0.00）。
 *     0.00012 向上取整会显示成 0.01，比真实值高出近百倍——那种失真比
 *     显示 0.00 更糟，而且会让所有极便宜的模型挤在同一个数字上、彼此看不出区别。
 *
 * 阈值是 10^-(precision+1)，随 precision 一起变，不写死 0.001。
 */
export const formatPriceWithCeiling = (value, precision = 2) => {
  const numericValue = Number(value);
  if (!Number.isFinite(numericValue)) return '-';

  const factor = 10 ** precision;
  const scaled = numericValue * factor;
  const tolerance = Number.EPSILON * Math.max(1, Math.abs(scaled));
  const ceilThreshold = 1 / (factor * 10);

  const settled =
    Math.abs(numericValue) >= ceilThreshold
      ? Math.ceil(scaled - tolerance)
      : Math.round(scaled);
  return (settled / factor).toFixed(precision);
};
