import React, { useEffect, useMemo, useState } from 'react';
import {
  Button,
  Card,
  Empty,
  Space,
  Spin,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';

import { API, showError } from '../../helpers';

const { Text, Title } = Typography;

/**
 * 充值套餐。设计见 docs/user-tier-pricing-and-topup-package-design.md §6。
 *
 * 只送积分：1:1 到账 + 赠送积分，不送额度、不附带折扣档位。
 *
 * 支付方式不在这里判断——payMethods 由 /api/user/topup/info 按站点实际配置返回
 * （配了支付宝就有 alipay_direct，没配就没有）。在这里再写一遍判断会多出一处
 * 要同步维护的真相。
 */

// 只有自研直连支付支持套餐下单：后端 package_id 分支目前只接了这两条路径。
// 其余支付方式（易支付 / Stripe / Creem）即便被配置出来，也不该在套餐上出现——
// 用户点了会得到一个忽略 package_id 的普通充值订单，钱付了但积分不发。
export function getPackagePayMethods(payMethods = []) {
  return (payMethods || []).filter(
    (m) => m?.type === 'alipay_direct' || m?.type === 'wxpay_direct',
  );
}

export default function TopupPackagesCard({
  payMethods = [],
  paymentLoading,
  onBuy,
}) {
  const { t } = useTranslation();
  const [packages, setPackages] = useState([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const res = await API.get('/api/user/topup/packages');
        if (cancelled) return;
        if (res.data?.success) {
          setPackages(res.data.data || []);
        }
      } catch (e) {
        if (!cancelled) showError(t('加载充值套餐失败'));
      }
      if (!cancelled) setLoading(false);
    })();
    return () => {
      cancelled = true;
    };
  }, [t]);

  const availableMethods = useMemo(
    () => getPackagePayMethods(payMethods),
    [payMethods],
  );

  // 没有套餐时整块不渲染：这是个可选功能，站点没配就不该占位
  if (!loading && packages.length === 0) {
    return null;
  }

  return (
    <Card className='!rounded-2xl mb-4' title={t('充值套餐')}>
      <Spin spinning={loading}>
        {availableMethods.length === 0 ? (
          <Empty description={t('当前没有可用于套餐购买的支付方式')} />
        ) : (
          <div className='grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 gap-3'>
            {packages.map((pkg) => (
              <Card
                key={pkg.id}
                className='!rounded-xl'
                bodyStyle={{ padding: 16 }}
              >
                <div className='flex items-baseline justify-between'>
                  <Text strong>{pkg.title}</Text>
                  <Title heading={4} className='!mb-0'>
                    ¥{Number(pkg.price_amount).toFixed(2)}
                  </Title>
                </div>

                {pkg.subtitle && (
                  <Text type='tertiary' size='small' className='block mt-1'>
                    {pkg.subtitle}
                  </Text>
                )}

                <div className='mt-2'>
                  <Tag color='blue' size='small'>
                    {t('到账')} ¥{Number(pkg.price_amount).toFixed(2)}
                  </Tag>
                  {pkg.grant_points > 0 && (
                    <Tag color='green' size='small' className='ml-1'>
                      {t('赠送')} {pkg.grant_points} {t('积分')}
                    </Tag>
                  )}
                </div>

                <Space className='mt-3' wrap>
                  {availableMethods.map((m) => (
                    <Button
                      key={m.type}
                      size='small'
                      theme='solid'
                      loading={paymentLoading}
                      onClick={() => onBuy?.(m.type, pkg.id)}
                    >
                      {m.name}
                    </Button>
                  ))}
                </Space>
              </Card>
            ))}
          </div>
        )}
      </Spin>
    </Card>
  );
}
