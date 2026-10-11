// 分段表达式的价格口径。
//
// v2（原文带 `v2:` 前缀）的系数是 **¥/百万 tokens**；无前缀 / `v1:` 的是历史美元口径
// （`$/百万 tokens`），后端已拒绝结算（见 pkg/billingexpr/settle.go 的
// ErrExprV1Unsupported），前端不改写存量表达式，只在展示时把系数折成人民币，
// 保证定价页/日志显示的数字与历史价格一致。
//
// 单独成模块是为了让 helpers/utils.jsx 与 model-pricing 组件共用同一份判定：
// helpers/utils.jsx 不能 import helpers/render.jsx（render 依赖 utils，会成环）。
import { getUsdExchangeRate } from './quota';
import { stripExprVersion } from './exprVersion';

/**
 * 该表达式原文对应的「系数 → 人民币」倍数。
 *
 * @param {string} exprStr - 表达式原文（可能带 `v2:` / `v1:` 前缀）
 * @returns {number} v2 及以上是 1；v1 / 无前缀是当前美元汇率（1 美元 = X 元）
 */
export function getExprPriceScale(exprStr) {
  const { version } = stripExprVersion(String(exprStr || ''));
  return version >= 2 ? 1 : getUsdExchangeRate();
}
