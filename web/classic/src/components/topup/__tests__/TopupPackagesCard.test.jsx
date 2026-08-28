import { describe, it, expect } from 'vitest';

import { getPackagePayMethods } from '../TopupPackagesCard';

/**
 * 套餐购买入口的支付方式过滤。
 *
 * 后端的 package_id 分支目前只接了自研支付宝 / 微信两条到账路径。其他支付方式即便
 * 被站点配置出来，也不能出现在套餐上——用户点了会得到一个**忽略 package_id 的普通
 * 充值订单**：钱照付、额度照到账，唯独积分不发，而且全程不报错。
 */

describe('getPackagePayMethods', () => {
  it('只保留自研支付宝与微信直连', () => {
    const got = getPackagePayMethods([
      { type: 'alipay_direct', name: '支付宝' },
      { type: 'wxpay_direct', name: '微信支付' },
      { type: 'stripe', name: 'Stripe' },
      { type: 'creem', name: 'Creem' },
      { type: 'alipay', name: '易支付-支付宝' },
    ]);

    expect(got.map((m) => m.type)).toEqual(['alipay_direct', 'wxpay_direct']);
  });

  it('易支付的 alipay 不等于直连的 alipay_direct', () => {
    // 两者只差一个后缀，但走的是完全不同的后端路径：易支付那条没接 package_id
    const got = getPackagePayMethods([{ type: 'alipay', name: '易支付' }]);
    expect(got).toEqual([]);
  });

  it('站点只配了支付宝时就只回支付宝', () => {
    const got = getPackagePayMethods([
      { type: 'alipay_direct', name: '支付宝' },
    ]);
    expect(got.map((m) => m.type)).toEqual(['alipay_direct']);
  });

  it('空输入与脏数据不炸', () => {
    expect(getPackagePayMethods()).toEqual([]);
    expect(getPackagePayMethods(null)).toEqual([]);
    expect(getPackagePayMethods([null, {}, { name: '无 type' }])).toEqual([]);
  });
});
