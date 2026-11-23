// 表达式「按项」处理：切分、取系数、缩放。
//
// 为什么单独抽出来：展示（模型广场/日志）与录入（改汇率时缩放）必须用同一套判定，
// 否则会出现"编辑器缩放了、广场没缩放"或者"广场乘了汇率、实扣没乘"。
// 之前的实现按空格切分、只认「变量 * 数字」一种顺序，实测有四处会出错：
//   - `p*1.5*fx+c*7.5`（无空格）判断不出 fx 归属
//   - `1.5 * p`（数字在前）取不到系数，展示成 0
//   - `1.5 * p * 2` 被前后两种正则各缩放一次，系数翻倍
//   - `tier("fx-档", ...)` 里的字符串被当成 fx 引用

import { BILLING_PRICING_VARS } from '../constants/billing.constants';

const VAR_NAMES = BILLING_PRICING_VARS.map((v) => v.key);
const VAR_PATTERN = VAR_NAMES.join('|');
const NUM_PATTERN = '\\d+(?:\\.\\d+)?(?:[eE][+-]?\\d+)?';

// 一次扫描同时匹配两种顺序，保证同一个系数只会被处理一次。
const COEFF_REGEX = new RegExp(
  `\\b(${VAR_PATTERN})\\s*\\*\\s*(${NUM_PATTERN})|(${NUM_PATTERN})\\s*\\*\\s*\\b(${VAR_PATTERN})\\b`,
  'g',
);

/** 去掉字符串字面量，避免档位标签里的内容被当成变量/关键字。 */
export const stripStringLiterals = (expr) =>
  String(expr || '').replace(/"[^"]*"/g, '""');

/** 表达式是否引用了 fx（本模型的源货币汇率）。 */
export const exprUsesFX = (expr) => /\bfx\b/.test(stripStringLiterals(expr));

/** 某一项是否引用了 fx。 */
export const termUsesFX = (term) => /\bfx\b/.test(stripStringLiterals(term));

/**
 * 把 tier(...) 的成本体按顶层加减号切成若干项。
 * 括号内的加减号不切；`1e-7` 这类指数符号也不切（前一个字符是 e/E）。
 */
export function splitTopLevelTerms(bodyStr) {
  const body = String(bodyStr || '');
  const terms = [];
  let depth = 0;
  let current = '';
  for (let i = 0; i < body.length; i += 1) {
    const ch = body[i];
    if (ch === '(') depth += 1;
    if (ch === ')') depth -= 1;
    const prev = i > 0 ? body[i - 1] : '';
    const isSign =
      (ch === '+' || ch === '-') &&
      depth === 0 &&
      current.trim() !== '' &&
      !/[eE]/.test(prev);
    if (isSign) {
      terms.push(current.trim());
      current = ch;
      continue;
    }
    current += ch;
  }
  if (current.trim() !== '') terms.push(current.trim());
  return terms;
}

/** 取出一项里的「变量 → 系数」，两种书写顺序都支持。 */
export function extractTermCoefficients(term) {
  const out = [];
  const re = new RegExp(COEFF_REGEX.source, 'g');
  let m;
  while ((m = re.exec(String(term || ''))) !== null) {
    const varName = m[1] || m[4];
    const numText = m[1] ? m[2] : m[3];
    const value = Number(numText);
    if (!varName || !Number.isFinite(value)) continue;
    out.push({ varName, value });
  }
  return out;
}

/** 缩放一项里的系数（每个系数只缩放一次），统一写成 `变量 * 数字`。 */
export function scaleTermCoefficients(term, factor) {
  return String(term || '').replace(
    COEFF_REGEX,
    (whole, varA, numA, numB, varB) => {
      const varName = varA || varB;
      const value = Number(varA ? numA : numB);
      if (!varName || !Number.isFinite(value)) return whole;
      const scaled = value * factor;
      if (!Number.isFinite(scaled)) return whole;
      return `${varName} * ${Number(scaled.toFixed(12))}`;
    },
  );
}
