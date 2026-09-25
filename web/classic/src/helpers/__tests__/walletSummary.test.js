import { describe, it, expect } from 'vitest';
import { buildWalletSummary } from '../walletSummary';

describe('buildWalletSummary', () => {
  it('普通用户：可用 = 余额 + 积分，没有授信块', () => {
    const s = buildWalletSummary(
      { quota: 1000, points_balance: 300 },
      { pointsEnabled: true },
    );
    expect(s).toEqual({
      available: 1300,
      balance: 1000,
      points: 300,
      showPoints: true,
      creditAvailable: 0,
      credit: null,
      overdue: 0,
    });
  });

  it('积分未启用时不计入总额也不展示', () => {
    const s = buildWalletSummary({ quota: 1000, points_balance: 300 });
    expect(s.available).toBe(1000);
    expect(s.points).toBe(0);
    expect(s.showPoints).toBe(false);
  });

  it('子账户不展示积分（积分是主账号资产）', () => {
    const s = buildWalletSummary(
      { quota: 1000, points_balance: 300, parent_user_id: 7 },
      { pointsEnabled: true },
    );
    expect(s.showPoints).toBe(false);
    expect(s.available).toBe(1000);
  });

  // 普通用户被打负是估算偏差造成的欠费，不是授信：总额截到 0，欠费单独给出
  it('普通用户负余额：可用截到 0、给出欠费', () => {
    const s = buildWalletSummary({ quota: -160 }, { pointsEnabled: false });
    expect(s.available).toBe(0);
    expect(s.balance).toBe(-160);
    expect(s.overdue).toBe(160);
    // 积分能盖住欠费时，可用是差额（与后端 quota + points 一致）
    const t = buildWalletSummary(
      { quota: -160, points_balance: 500 },
      { pointsEnabled: true },
    );
    expect(t.available).toBe(340);
  });

  it('授信用户：可用 = 余额 + 积分 + 授信可用', () => {
    const s = buildWalletSummary(
      { quota: 200, points_balance: 100, credit_limit: 1000, credit_used: 300 },
      { pointsEnabled: true },
    );
    expect(s.available).toBe(200 + 100 + 700);
    expect(s.creditAvailable).toBe(700);
    expect(s.credit).toEqual({
      limit: 1000,
      used: 300,
      available: 700,
      over: 0,
    });
    expect(s.overdue).toBe(0);
  });

  // 在途透支停在负余额上：余额截到 0，透支只体现在授信「已用」里，同一笔不出现两次；
  // 总额与后端预扣口径恒等：quota(负) + (limit − used) == 0 + (limit − used − |quota|)
  it('授信用户负余额：余额截 0，透支只记在授信已用里', () => {
    const user = { quota: -160, credit_limit: 1000, credit_used: 230 };
    const s = buildWalletSummary(user);
    expect(s.balance).toBe(0);
    expect(s.credit.used).toBe(390);
    expect(s.creditAvailable).toBe(610);
    expect(s.available).toBe(610);
    expect(s.available).toBe(
      user.quota + (user.credit_limit - user.credit_used),
    );
    expect(s.overdue).toBe(0);
  });

  it('授信超限：可用为 0、超出额单独给出', () => {
    const s = buildWalletSummary({
      quota: 0,
      credit_limit: 1000,
      credit_used: 1200,
    });
    expect(s.available).toBe(0);
    expect(s.credit.over).toBe(200);
  });
});
