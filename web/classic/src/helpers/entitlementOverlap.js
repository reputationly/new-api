/**
 * 权益模型范围的重叠检测。
 *
 * 权益按顺序匹配，先命中先生效。两条权益的模型范围一旦重叠，靠后那条对重叠部分
 * 永远不会生效——这本身是合法配置（可以用来做「先特例后通配」），但更多时候是
 * 运营没意识到的配置错误：以为给 glm-4-plus 配了 500 次上限，实际被前面的
 * 「glm-* 不限次」整个盖住了。所以这里只告警、不阻止保存，并指出具体是哪个模型，
 * 让运营自己决定要不要调顺序。
 */

/**
 * 判断单个模型名是否命中某个模式。模式里的 * 匹配任意字符（含空串）。
 */
export function modelMatchesPattern(name, pattern) {
  if (!name || !pattern) return false;
  if (!pattern.includes('*')) return name === pattern;
  // 转义正则元字符后再把 \* 还原成 .*，避免模型名里的 . + 等被当成正则
  const escaped = pattern.replace(/[.+?^${}()|[\]\\]/g, '\\$&');
  const regex = new RegExp(`^${escaped.split('*').join('.*')}$`);
  return regex.test(name);
}

/**
 * 两个模式是否存在交集。
 *
 * 字面量 vs 字面量：相等才算重叠。
 * 通配 vs 字面量：通配能匹配到该字面量即重叠。
 * 通配 vs 通配：任一方的固定前缀被另一方匹配即重叠（claude-* 与 claude-opus-*
 * 显然有交集，而 gpt-* 与 claude-* 没有）。
 */
export function patternsOverlap(a, b) {
  if (!a || !b) return false;
  if (a === b) return true;
  const aWild = a.includes('*');
  const bWild = b.includes('*');
  if (!aWild && !bWild) return false;
  if (aWild && !bWild) return modelMatchesPattern(b, a);
  if (!aWild && bWild) return modelMatchesPattern(a, b);
  const aPrefix = a.split('*')[0];
  const bPrefix = b.split('*')[0];
  return aPrefix.startsWith(bPrefix) || bPrefix.startsWith(aPrefix);
}

/**
 * 把逗号分隔的模型串拆成数组，顺带去空白与空项。
 */
export function splitModels(raw) {
  if (Array.isArray(raw)) {
    return raw.map((s) => String(s).trim()).filter(Boolean);
  }
  return String(raw || '')
    .split(',')
    .map((s) => s.trim())
    .filter(Boolean);
}

/**
 * 找出权益列表里两两重叠的组合。
 *
 * 返回 [{ first, second, models }]，下标从 0 开始，models 是导致重叠的模式，
 * 供 UI 直接指名道姓地提示。只比较排在前面的对后面的（i < j），因为「谁盖住谁」
 * 由顺序决定，反过来提示没有意义。
 */
export function findEntitlementOverlaps(entitlements) {
  const list = Array.isArray(entitlements) ? entitlements : [];
  const parsed = list.map((e) => splitModels(e?.models));
  const result = [];
  for (let i = 0; i < parsed.length; i++) {
    for (let j = i + 1; j < parsed.length; j++) {
      const hits = [];
      parsed[i].forEach((pa) => {
        parsed[j].forEach((pb) => {
          if (!patternsOverlap(pa, pb)) return;
          // 用靠后那条的模式来描述冲突：被盖住的是它
          if (!hits.includes(pb)) hits.push(pb);
        });
      });
      if (hits.length > 0) {
        result.push({ first: i, second: j, models: hits });
      }
    }
  }
  return result;
}
