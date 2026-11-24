import { describe, it, expect } from 'vitest';

import { buildRedemptionInitValues } from '../../table/redemptions/modals/EditRedemptionModal';
import { emptyForm } from '../../../pages/TopupPackage/index';

/**
 * 二期验收的硬要求（docs/points-expiry-design.md §十九 末尾）：
 *
 * 兑换码与套餐的编辑接口都是「整对象 PUT」——请求里没带的字段会被写成零值。
 * 有效期字段一旦接进写路径，「编辑弹窗不带当前值」就等于把已设的有效期**静默改成
 * 永不过期**，且界面上看不出来。所以两条编辑表单的初值里都必须有这个字段。
 *
 * 这里钉住的就是「初值不能缺字段」这一条——它是静默清零的根因。
 */

describe('有效期字段的表单初值', () => {
  it('兑换码弹窗初值含 points_expire_days，默认 0（永不过期）', () => {
    for (const pointsEnabled of [true, false]) {
      const values = buildRedemptionInitValues(pointsEnabled);
      expect(
        Object.prototype.hasOwnProperty.call(values, 'points_expire_days'),
      ).toBe(true);
      expect(values.points_expire_days).toBe(0);
      // 与「兑换码有效期」是两个字段，不能混
      expect(
        Object.prototype.hasOwnProperty.call(values, 'expired_time'),
      ).toBe(true);
    }
  });

  it('套餐表单初值含 grant_points_expire_days，默认 0', () => {
    expect(
      Object.prototype.hasOwnProperty.call(
        emptyForm,
        'grant_points_expire_days',
      ),
    ).toBe(true);
    expect(emptyForm.grant_points_expire_days).toBe(0);
  });

  it('初值里的 0 表示永不过期，不是「未填写」', () => {
    // 后端把 0 解释为永不过期（发放时 expires_at=0，永不作废）。
    // 表单如果把「未填」序列化成 0，语义上等价于「改成永不过期」——
    // 这正是为什么编辑必须回带真实值而不是留空。
    expect(buildRedemptionInitValues(true).points_expire_days).toBe(0);
    expect(emptyForm.grant_points_expire_days).toBe(0);
  });
});
