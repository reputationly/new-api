#!/usr/bin/env node
/**
 * 静态检查：可编辑表格的 `columns` useMemo 必须是稳定的。
 *
 * 为什么需要它：Semi 的 Table 在 `columns` 数组身份变化时会重建单元格，
 * 里面的 Input / InputNumber 光标随之跳到末尾。而「敲键 → onChange → 父组件
 * setState → 新 props → useMemo 重算 → 回调换身份 → columns 重建」这条链非常
 * 容易在写新组件时无意中接上——同一个 bug 在本仓库已经出现过三次
 * （GroupTable、ModelRatioEditor、GroupExtraSettings），每次都是靠人工 review
 * 才发现的。
 *
 * 判据：`columns` 的依赖里出现的每个 useCallback/useMemo，其依赖链必须最终
 * 收敛到空数组。允许直接依赖 ALLOWED_DIRECT 里的值——它们不会在打字过程中变化。
 *
 * 用法：node scripts/check-stable-columns.mjs [目录...]
 */
import { readdirSync, readFileSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';

// 这些值不会在打字过程中改变，作为 columns 的直接依赖是安全的
const ALLOWED_DIRECT = new Set(['t', 'selected', 'selectedRowKeys']);

/** 取 `const NAME = useCallback(...)` / `useMemo(...)` 的依赖数组，返回 null 表示不是这两种 */
function readHook(src, name) {
  const decl = `const ${name} = use`;
  const start = src.indexOf(decl);
  if (start === -1) return null;
  const kind = src.slice(start + decl.length).match(/^(Callback|Memo)\b/);
  if (!kind) return null;

  let depth = 0;
  let i = src.indexOf('(', start);
  const open = i;
  for (; i < src.length; i++) {
    if (src[i] === '(') depth++;
    else if (src[i] === ')') {
      depth--;
      if (depth === 0) break;
    }
  }
  const call = src.slice(open, i);
  const m = call.match(/\[([^[\]]*)\]\s*,?\s*$/);
  if (!m) return { deps: null }; // 没有依赖数组 = 每次渲染都变
  return {
    deps: m[1]
      .split(',')
      .map((x) => x.trim())
      .filter(Boolean),
  };
}

/** `const X = useRef(...)` —— 身份恒定 */
function isRef(src, name) {
  return new RegExp(`const\\s+${name}\\s*=\\s*useRef\\s*\\(`).test(src);
}

/** `const [x, setX] = useState(...)` 里的 setX —— React 保证身份恒定 */
function isStateSetter(src, name) {
  return new RegExp(
    `const\\s*\\[[^\\]]*,\\s*${name}\\s*\\]\\s*=\\s*useState\\s*\\(`,
  ).test(src);
}

/**
 * 依赖链是否最终收敛到空数组。
 *
 * **默认不稳定**。这一点是这个脚本第一版写错、被回退验证抓出来的地方：
 * 当时把「识别不出来的标识符」当成稳定放行，而 props（value / inputs）恰恰
 * 就是最主要的不稳定源——于是三处已知的 bug 一个都没报出来，脚本全绿。
 * 能静态证明稳定的只有三种：空依赖的 hook、useRef、useState 的 setter。
 */
function isStable(src, name, seen = new Set()) {
  if (ALLOWED_DIRECT.has(name)) return true;
  if (seen.has(name)) return true; // 循环引用，交给别处报
  seen.add(name);

  if (isRef(src, name) || isStateSetter(src, name)) return true;

  const hook = readHook(src, name);
  if (!hook) return false; // prop、解构出来的值、外部变量……一律按不稳定处理
  if (hook.deps === null) return false; // 没写依赖数组 = 每次渲染都变
  return hook.deps.every((d) => isStable(src, d, seen));
}

function checkFile(file) {
  const src = readFileSync(file, 'utf8');
  const hook = readHook(src, 'columns');
  if (!hook || hook.deps === null) return [];
  const bad = hook.deps.filter((d) => !isStable(src, d));
  return bad.length
    ? [
        `${file}: columns 依赖了不稳定的 ${bad.join(', ')} —— ` +
          `打字时会重建表格列，输入框光标跳到末尾。改用渲染期同步的 ref，把回调依赖降为 []。`,
      ]
    : [];
}

function walk(dir, out = []) {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    if (statSync(full).isDirectory()) walk(full, out);
    else if (full.endsWith('.jsx') || full.endsWith('.js')) out.push(full);
  }
  return out;
}

const targets = process.argv.slice(2);
if (targets.length === 0) {
  console.error('用法: node scripts/check-stable-columns.mjs <目录|文件>...');
  process.exit(2);
}

const problems = [];
let scanned = 0;
for (const target of targets) {
  const files = statSync(target).isDirectory() ? walk(target) : [target];
  for (const f of files) {
    scanned++;
    problems.push(...checkFile(f));
  }
}

if (problems.length) {
  console.error(`发现 ${problems.length} 处不稳定的 columns：\n`);
  for (const p of problems) console.error('  ✗ ' + relative(process.cwd(), p));
  process.exit(1);
}
console.log(`✓ 已扫描 ${scanned} 个文件，columns 依赖链均稳定`);
