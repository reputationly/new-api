import { describe, it, expect } from 'vitest';
import {
  PLAYGROUND_BATCH_COUNTS,
  PLAYGROUND_BATCH_DEFAULT,
  normalizeBatchCount,
  deriveSeeds,
} from '../playgroundBatch.constants';

// 「一次生成几张」的核心是 **seed 必须各不相同** —— 相同就是 N 份一样的东西,
// 白花 N 倍的钱,而且不报错。所以这一组守的都是 seed 派生。

describe('normalizeBatchCount', () => {
  it('只认白名单里的档位,其余一律回落默认', () => {
    for (const n of PLAYGROUND_BATCH_COUNTS) {
      expect(normalizeBatchCount(n)).toBe(n);
      expect(normalizeBatchCount(String(n))).toBe(n);
    }
    // 老会话没有这个字段 / 脏值 → 回落 1,与改造前行为一致
    for (const bad of [undefined, null, '', 0, -1, 4, 99, 'abc', {}]) {
      expect(normalizeBatchCount(bad)).toBe(PLAYGROUND_BATCH_DEFAULT);
    }
  });

  it('默认是 1 —— 多花钱的事不能是默认值', () => {
    expect(PLAYGROUND_BATCH_DEFAULT).toBe(1);
  });
});

describe('deriveSeeds', () => {
  it('用户留空:抽 n 个互不相同的随机 seed', () => {
    for (const n of PLAYGROUND_BATCH_COUNTS) {
      const seeds = deriveSeeds('', n);
      expect(seeds).toHaveLength(n);
      // 同一批内不能撞车,撞了就是两个一模一样的候选
      expect(new Set(seeds).size).toBe(n);
      for (const s of seeds) {
        expect(Number.isInteger(s)).toBe(true);
        // 0 不能出现:部分引擎把 0 当"未指定"
        expect(s).toBeGreaterThan(0);
        expect(s).toBeLessThanOrEqual(2147483647);
      }
    }
  });

  it('null / undefined 与留空等价', () => {
    expect(deriveSeeds(null, 2)).toHaveLength(2);
    expect(deriveSeeds(undefined, 2)).toHaveLength(2);
    expect(new Set(deriveSeeds(null, 3)).size).toBe(3);
  });

  it('用户填了 seed:从它开始递增,整组可复现', () => {
    expect(deriveSeeds(100, 3)).toEqual([100, 101, 102]);
    expect(deriveSeeds('100', 2)).toEqual([100, 101]);
    // 递增而不是"n 个都用同一个":那样出来的是 n 份一样的东西
    expect(new Set(deriveSeeds(7, 3)).size).toBe(3);
    // 也不是"填了就再随机":记下起始 seed 要能把整组重放出来
    expect(deriveSeeds(7, 3)).toEqual(deriveSeeds(7, 3));
  });

  // 面板上的种子输入框只有 min={0}、没有上界,所以用户真能填到 32 位边界上。
  // 递增越界不报错 —— 引擎收到超出它 seed 类型的值,要么拒、要么静默截断成另一个数,
  // 两种都表现为"这一张跟我给的种子对不上"。
  it('递增越过 32 位上界时回卷,且回卷后仍两两不同', () => {
    const MAX = 2147483646;
    const seeds = deriveSeeds(MAX, 3);
    expect(seeds).toEqual([MAX, 1, 2]);
    // 关键:不能截断成 [MAX, MAX, MAX] —— 那会让末几张撞成同一个候选,白花钱
    expect(new Set(seeds).size).toBe(3);
    for (const x of seeds) {
      expect(x).toBeGreaterThan(0);
      expect(x).toBeLessThanOrEqual(MAX);
    }
  });

  it('区间内的 seed 不受回卷影响', () => {
    expect(deriveSeeds(100, 3)).toEqual([100, 101, 102]);
    expect(deriveSeeds(1, 3)).toEqual([1, 2, 3]);
  });

  it('非整数 / 0 / 负数都被规整进 [1, MAX]', () => {
    for (const bad of [1.7, 0, -5, -2147483650]) {
      for (const x of deriveSeeds(bad, 3)) {
        expect(Number.isInteger(x), `${bad} 派生出非整数 ${x}`).toBe(true);
        expect(x).toBeGreaterThan(0);
        expect(x).toBeLessThanOrEqual(2147483646);
      }
    }
  });

  it('张数非法时按默认 1 走,不会因为脏值一次发一堆请求', () => {
    // 99 不在档位表里 → 回落 1。这条守的是"脏值不能变成 99 路并发"。
    expect(deriveSeeds(100, 99)).toEqual([100]);
    expect(deriveSeeds('', 99)).toHaveLength(1);
    expect(deriveSeeds(100, undefined)).toEqual([100]);
    expect(deriveSeeds('', 'abc')).toHaveLength(1);
  });
});
