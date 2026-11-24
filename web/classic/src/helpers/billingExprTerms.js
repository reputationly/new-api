// 表达式「按项」处理：切分、取系数、缩放。
//
// 为什么单独抽出来：展示（模型广场/日志）与录入（改汇率时缩放）必须用同一套判定，
// 否则会出现"编辑器缩放了、广场没缩放"或者"广场乘了汇率、实扣没乘"。
// 之前的实现按空格切分、只认「变量 * 数字」一种顺序，实测有四处会出错：
//   - `p*1.5*fx+c*7.5`（无空格）判断不出 fx 归属
//   - `1.5 * p`（数字在前）取不到系数，展示成 0
//   - `1.5 * p * 2` 被前后两种正则各缩放一次，系数翻倍
//   - `tier("fx-档", ...)` 里的字符串被当成 fx 引用

import {
  BILLING_PRICING_VARS,
  BILLING_VAR_KEY_TO_FIELD,
} from '../constants/billing.constants';
import { stripExprVersion } from './exprVersion';
import {
  EXPR_STRING_LITERAL,
  decodeExprStringLiteral,
} from './exprStringLiteral';
import {
  splitBillingExprAndRequestRules,
  unwrapOuterParens,
} from '../pages/Setting/Ratio/components/requestRuleExpr';

const VAR_NAMES = BILLING_PRICING_VARS.map((v) => v.key);
const VAR_PATTERN = VAR_NAMES.join('|');
const NUM_PATTERN = '\\d+(?:\\.\\d+)?(?:[eE][+-]?\\d+)?';
const TIER_CALL = new RegExp(
  '^tier\\s*\\(\\s*(' + EXPR_STRING_LITERAL.source + ')\\s*,([\\s\\S]+)\\)$',
);

// 一次扫描同时匹配两种顺序，保证同一个系数只会被处理一次。
const COEFF_REGEX = new RegExp(
  `\\b(${VAR_PATTERN})\\s*\\*\\s*(${NUM_PATTERN})|(${NUM_PATTERN})\\s*\\*\\s*\\b(${VAR_PATTERN})\\b`,
  'g',
);

/** 去掉字符串字面量，避免档位标签里的内容被当成变量/关键字。 */
export const stripStringLiterals = (expr) =>
  String(expr || '').replace(new RegExp(EXPR_STRING_LITERAL.source, 'g'), '""');

/** 表达式是否引用了 fx（本模型的源货币汇率）。 */
export const exprUsesFX = (expr) => /\bfx\b/.test(stripStringLiterals(expr));

/** 某一项是否引用了 fx。 */
export const termUsesFX = (term) => /\bfx\b/.test(stripStringLiterals(term));

export function linearTokenPrices(body, fx) {
  const token = new RegExp(`\\s*(${NUM_PATTERN}|[a-zA-Z_]\\w*|[()+*/-])`, 'y');
  const source = body.trim();
  let pos = 0;
  const peek = () => {
    token.lastIndex = pos;
    return token.exec(source);
  };
  const take = () => {
    const match = peek();
    if (!match) throw new Error('Unexpected token');
    pos += match[0].length;
    return match[1];
  };
  const constant = (value) => [value, {}];
  const scale = ([offset, prices], factor) => [
    offset * factor,
    Object.fromEntries(
      Object.entries(prices).map(([key, value]) => [key, value * factor]),
    ),
  ];
  const atom = () => {
    const value = take();
    if (value === '+' || value === '-')
      return scale(atom(), value === '-' ? -1 : 1);
    if (value === '(') {
      const result = sum();
      if (take() !== ')') throw new Error('Expected closing parenthesis');
      return result;
    }
    if (VAR_NAMES.includes(value)) return [0, { [value]: 1 }];
    if (value === 'fx' && Number(fx) > 0) return constant(Number(fx));
    if (/^\d/.test(value)) return constant(Number(value));
    throw new Error('Unsupported variable');
  };
  const product = () => {
    let result = atom();
    while (['*', '/'].includes(peek()?.[1])) {
      const op = take();
      const right = atom();
      if (
        Object.keys(right[1]).length === 0 &&
        (op === '*' || right[0] !== 0)
      ) {
        result = scale(result, op === '*' ? right[0] : 1 / right[0]);
      } else if (op === '*' && Object.keys(result[1]).length === 0) {
        result = scale(right, result[0]);
      } else {
        throw new Error('Nonlinear price');
      }
    }
    return result;
  };
  const sum = () => {
    let [offset, prices] = product();
    while (['+', '-'].includes(peek()?.[1])) {
      const sign = take() === '+' ? 1 : -1;
      const [rightOffset, rightPrices] = product();
      offset += sign * rightOffset;
      for (const [key, value] of Object.entries(rightPrices)) {
        prices[key] = (prices[key] || 0) + sign * value;
      }
    }
    return [offset, prices];
  };
  try {
    const [offset, prices] = sum();
    return pos === source.length &&
      offset === 0 &&
      Object.values(prices).every(
        (value) => Number.isFinite(value) && value >= 0,
      )
      ? prices
      : null;
  } catch {
    return null;
  }
}

function splitTierBranches(body, conditions = []) {
  body = unwrapOuterParens(body);
  const condition = (text) =>
    text.split(/\s*&&\s*/).flatMap((part) => {
      const match = part
        .trim()
        .match(/^(p|c|len)\s*(<=|>=|<|>)\s*([\d.eE+-]+)$/);
      return match
        ? [{ var: match[1], op: match[2], value: Number(match[3]) }]
        : [];
    });
  let depth = 0;
  let question = -1;
  let branches = 0;
  const structural = new RegExp(EXPR_STRING_LITERAL.source + '|[()?:]', 'g');
  for (const { 0: char, index } of body.matchAll(structural)) {
    if (char === '(') depth += 1;
    else if (char === ')') depth -= 1;
    else if (depth === 0 && char === '?') {
      if (branches === 0) question = index;
      branches += 1;
    } else if (
      depth === 0 &&
      char === ':' &&
      branches > 0 &&
      --branches === 0
    ) {
      return [
        ...splitTierBranches(
          body.slice(question + 1, index),
          condition(body.slice(0, question)),
        ),
        ...splitTierBranches(body.slice(index + 1), conditions),
      ];
    }
  }
  const match = body.trim().match(TIER_CALL);
  if (!match) return [{ body, conditions }];
  return [
    { label: decodeExprStringLiteral(match[1]), body: match[2], conditions },
  ];
}

export function parseTiersFromExpr(exprStr, fx = 0) {
  if (!exprStr) return [];
  try {
    const { billingExpr } = splitBillingExprAndRequestRules(
      stripExprVersion(exprStr).body,
    );
    return splitTierBranches(billingExpr).map(({ label, body, conditions }) => {
      const prices = linearTokenPrices(body, fx);
      const isBareZero =
        label === undefined &&
        prices !== null &&
        Object.values(prices).every((price) => price === 0);
      return {
        label,
        conditions,
        ...(prices === null || (label === undefined && !isBareZero)
          ? { indivisible: true }
          : Object.fromEntries(
              Object.entries(prices).map(([key, price]) => [
                BILLING_VAR_KEY_TO_FIELD[key],
                price,
              ]),
            )),
      };
    });
  } catch {
    return [];
  }
}

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
