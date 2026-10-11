// 分段表达式的版本前缀解析。
//
// 表达式原文形如 `v2:tier("base", p * 14.6)`；无前缀一律按 v1（历史美元口径）处理，
// 以兼容存量数据。单独成模块是为了让 helpers/exprPrice.js、helpers/render.jsx 与
// 定价设置页共用同一份语法：helpers/utils.jsx 不能 import helpers/render.jsx
// （render 依赖 utils，会成环），所以判定不能只放在 render.jsx 里。
//
// 原定义在 helpers/render.jsx，现由该文件再导出，公共 API 不变。

/**
 * @param {string} exprStr - 表达式原文
 * @returns {{version: number, body: string}} 版本号与去掉前缀后的正文
 */
export function stripExprVersion(exprStr) {
  if (!exprStr) return { version: 1, body: '' };
  const m = exprStr.match(/^v(\d+):([\s\S]*)$/);
  if (m) return { version: Number(m[1]), body: m[2] };
  return { version: 1, body: exprStr };
}
