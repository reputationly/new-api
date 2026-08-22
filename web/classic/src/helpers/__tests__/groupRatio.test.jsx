import { describe, it, expect } from 'vitest';
import { getEffectiveGroupRatio, calculateModelPrice } from '../utils';

// 这些函数直接决定用户在模型广场看到的价格。改错了不会有任何报错，
// 只会静默显示一个和实际扣费对不上的数——所以必须有断言钉住。
describe('getEffectiveGroupRatio', () => {
  const groupRatio = { default: 1, premium: 1.5 };
  const groupModelRatio = { premium: { 'GLM-5': 2.2 } };

  it('命中模型折扣时用后端算好的终值', () => {
    expect(
      getEffectiveGroupRatio(groupRatio, groupModelRatio, 'premium', 'GLM-5'),
    ).toBe(2.2);
  });

  it('未命中时退回分组基础倍率', () => {
    expect(
      getEffectiveGroupRatio(groupRatio, groupModelRatio, 'premium', 'other'),
    ).toBe(1.5);
    expect(
      getEffectiveGroupRatio(groupRatio, groupModelRatio, 'default', 'GLM-5'),
    ).toBe(1);
  });

  it('groupModelRatio 缺省时不炸', () => {
    expect(getEffectiveGroupRatio(groupRatio, undefined, 'premium', 'x')).toBe(
      1.5,
    );
    expect(getEffectiveGroupRatio(groupRatio, {}, 'premium', 'x')).toBe(1.5);
  });

  it('倍率 0 是合法值，不能被当成未配置吞掉', () => {
    expect(
      getEffectiveGroupRatio({ free: 1 }, { free: { m: 0 } }, 'free', 'm'),
    ).toBe(0);
  });
});
