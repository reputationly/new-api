import { describe, it, expect } from 'vitest';

import { buildNewRuleDraft } from '../ChannelPointsRewards';

/**
 * Q1（二期第 1 轮验收）：渠道奖励的「有效期」预填值必须被保存。
 *
 * 输入框用 initValue 显示预填值，而待保存的 draft 只在 onChange 时才写入。
 * 运营不动输入框直接保存，存下的规则就没有 expire_days，后端按 0（永不过期）处理——
 * 界面显示 90 天、实际永不过期，且没有任何提示。
 */

describe('buildNewRuleDraft', () => {
  it('把注册礼有效期带进 draft（不动输入框直接保存也是 90）', () => {
    const draft = buildNewRuleDraft(90);
    expect(draft.expire_days).toBe(90);

    // addRule 提交的是 { ...draft, enabled: true }
    const payload = { ...draft, enabled: true };
    expect(payload.expire_days).toBe(90);
  });

  it('注册礼为 0（永不过期）时 draft 也是 0，不是缺字段', () => {
    const draft = buildNewRuleDraft(0);
    expect(Object.prototype.hasOwnProperty.call(draft, 'expire_days')).toBe(
      true,
    );
    expect(draft.expire_days).toBe(0);
  });

  it('脏输入被收敛成非负整数', () => {
    expect(buildNewRuleDraft(undefined).expire_days).toBe(0);
    expect(buildNewRuleDraft(null).expire_days).toBe(0);
    expect(buildNewRuleDraft('30').expire_days).toBe(30);
    expect(buildNewRuleDraft(-5).expire_days).toBe(0);
    expect(buildNewRuleDraft(12.7).expire_days).toBe(12);
  });

  it('积分默认 0（不赠送），避免预填成其它值', () => {
    expect(buildNewRuleDraft(90).points).toBe(0);
  });
});
