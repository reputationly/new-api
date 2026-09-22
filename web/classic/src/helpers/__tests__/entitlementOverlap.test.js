import { describe, it, expect } from 'vitest';
import {
  modelMatchesPattern,
  patternsOverlap,
  splitModels,
  findEntitlementOverlaps,
} from '../entitlementOverlap';

describe('modelMatchesPattern', () => {
  it('字面量要完全相等', () => {
    expect(modelMatchesPattern('gpt-5', 'gpt-5')).toBe(true);
    expect(modelMatchesPattern('gpt-5', 'gpt-4')).toBe(false);
    expect(modelMatchesPattern('gpt-5-mini', 'gpt-5')).toBe(false);
  });

  it('通配符匹配任意后缀，含空串', () => {
    expect(modelMatchesPattern('claude-opus-4', 'claude-opus-*')).toBe(true);
    expect(modelMatchesPattern('claude-opus-', 'claude-opus-*')).toBe(true);
    expect(modelMatchesPattern('claude-sonnet-4', 'claude-opus-*')).toBe(false);
  });

  // 模型名里带 . 和 + 很常见（ltx2.5、gpt-4.1），不转义会让它们被当成正则元字符，
  // 把「任意字符」误判成匹配。
  it('模型名里的正则元字符按字面量处理', () => {
    expect(modelMatchesPattern('ltx2.5', 'ltx2.5')).toBe(true);
    expect(modelMatchesPattern('ltx2x5', 'ltx2.5')).toBe(false);
    expect(modelMatchesPattern('gpt-4.1-mini', 'gpt-4.1-*')).toBe(true);
    expect(modelMatchesPattern('gpt-4x1-mini', 'gpt-4.1-*')).toBe(false);
  });
});

describe('patternsOverlap', () => {
  it('两个字面量只有相等才重叠', () => {
    expect(patternsOverlap('gpt-5', 'gpt-5')).toBe(true);
    expect(patternsOverlap('gpt-5', 'gpt-4')).toBe(false);
  });

  it('通配与字面量按匹配关系判断', () => {
    expect(patternsOverlap('claude-*', 'claude-opus-4')).toBe(true);
    expect(patternsOverlap('claude-opus-4', 'claude-*')).toBe(true);
    expect(patternsOverlap('claude-*', 'gpt-5')).toBe(false);
  });

  it('两个通配按前缀包含关系判断', () => {
    expect(patternsOverlap('claude-*', 'claude-opus-*')).toBe(true);
    expect(patternsOverlap('claude-opus-*', 'claude-*')).toBe(true);
    expect(patternsOverlap('gpt-*', 'claude-*')).toBe(false);
  });

  it('空值不算重叠', () => {
    expect(patternsOverlap('', 'gpt-5')).toBe(false);
    expect(patternsOverlap('gpt-5', undefined)).toBe(false);
  });
});

describe('splitModels', () => {
  it('拆逗号串并去掉空白与空项', () => {
    expect(splitModels(' gpt-5 , claude-* ,, ')).toEqual(['gpt-5', 'claude-*']);
  });

  it('已经是数组时原样规整', () => {
    expect(splitModels([' a ', '', 'b'])).toEqual(['a', 'b']);
  });

  it('空输入返回空数组', () => {
    expect(splitModels(undefined)).toEqual([]);
    expect(splitModels('')).toEqual([]);
  });
});

describe('findEntitlementOverlaps', () => {
  it('没有重叠时返回空', () => {
    const result = findEntitlementOverlaps([
      { models: 'qwen3-*,glm-*' },
      { models: 'gpt-5,claude-opus-*' },
    ]);
    expect(result).toEqual([]);
  });

  it('指出具体是哪个模型被前面的权益盖住', () => {
    const result = findEntitlementOverlaps([
      { models: 'glm-*' },
      { models: 'gpt-5,glm-4-plus' },
    ]);
    expect(result).toHaveLength(1);
    expect(result[0].first).toBe(0);
    expect(result[0].second).toBe(1);
    expect(result[0].models).toEqual(['glm-4-plus']);
  });

  // 顺序即优先级，只报「前面的盖住后面的」。反过来报既没有意义，
  // 也会让同一处冲突出现两条告警。
  it('只报前面对后面的覆盖，不重复报', () => {
    const result = findEntitlementOverlaps([
      { models: 'a-*' },
      { models: 'a-1' },
      { models: 'a-2' },
    ]);
    expect(result).toHaveLength(2);
    expect(result.map((r) => [r.first, r.second])).toEqual([
      [0, 1],
      [0, 2],
    ]);
  });

  it('容忍缺字段与非数组输入', () => {
    expect(findEntitlementOverlaps(null)).toEqual([]);
    expect(findEntitlementOverlaps([{}, {}])).toEqual([]);
  });
});
