import { describe, it, expect } from 'vitest';
import { isPointsEligible } from '../../../helpers/utils';

// 「仅看可积分抵扣」的筛选判据。
//
// **断言的是生产代码里那支函数本身**（helpers/utils.jsx 的 isPointsEligible），不是在
// 这里再实现一遍 —— 早先这个文件内联了一份自己的实现，还在注释里写着"改了 hook 那边、
// 忘了这边会红"，而实际上把真实判据改坏（去掉 quotaPerPoint > 0、去掉白名单、放松分组
// 判断）这套用例照样全绿。**宣称覆盖而没有覆盖，比没有测试更糟。**
//
// 它必须与 calculateModelPrice 里算积分价那段同口径：两处分叉就会出现「筛出来的模型
// 点进去显示不能抵扣」，而且不报错。
const eligible = (model, { config, selectedGroup }) =>
  isPointsEligible(model, { pointsConfig: config, selectedGroup });

// 现网实测形态：积分开着、白名单启用、只有 default 分组能抵扣。
const CONFIG = {
  enabled: true,
  quotaPerPoint: 684.93,
  enabledGroups: ['default'],
  enabledModels: ['gpt-4o', 'qwen-image'],
};

const m = (name, groups) => ({ model_name: name, enable_groups: groups });

describe('可积分抵扣的判据', () => {
  const CASES = [
    ['白名单内 + 分组匹配 → 可抵扣', m('gpt-4o', ['default']), 'all', true],
    ['白名单外 → 不可', m('claude', ['default']), 'all', false],
    ['白名单内但分组不匹配 → 不可', m('gpt-4o', ['vip']), 'all', false],
    [
      '多分组只要有一个命中 → 可抵扣',
      m('qwen-image', ['vip', 'default']),
      'all',
      true,
    ],
    // 选了具体分组时，那个分组必须同时在两边——否则等于告诉用户"这个模型能抵扣"，
    // 而他实际用的分组根本不行。
    ['选中 default → 可抵扣', m('gpt-4o', ['default', 'vip']), 'default', true],
    [
      '选中 vip（积分不覆盖该分组）→ 不可',
      m('gpt-4o', ['default', 'vip']),
      'vip',
      false,
    ],
    ['选中的分组该模型用不了 → 不可', m('gpt-4o', ['default']), 'vip', false],
  ];
  for (const [name, model, group, expected] of CASES) {
    it(name, () => {
      expect(eligible(model, { config: CONFIG, selectedGroup: group })).toBe(
        expected,
      );
    });
  }

  it('积分总开关关掉 → 一个都筛不出来', () => {
    const off = { ...CONFIG, enabled: false };
    expect(eligible(m('gpt-4o', ['default']), { config: off })).toBe(false);
  });

  // 单位积分额度为 0 时换算会除零，结算侧本来就不发积分价；筛选也必须跟着关掉，
  // 否则会筛出一批"看着能抵扣、点进去没有积分价"的模型。
  it('单位积分额度为 0 → 一个都筛不出来', () => {
    const zero = { ...CONFIG, quotaPerPoint: 0 };
    expect(eligible(m('gpt-4o', ['default']), { config: zero })).toBe(false);
  });

  // 白名单是后端「渠道白名单」那道闸的产物，null 表示没启用这一层。
  it('白名单为 null → 退回只看分组的旧口径', () => {
    const noWhitelist = { ...CONFIG, enabledModels: null };
    expect(eligible(m('任意模型', ['default']), { config: noWhitelist })).toBe(
      true,
    );
    expect(eligible(m('任意模型', ['vip']), { config: noWhitelist })).toBe(
      false,
    );
  });
});
