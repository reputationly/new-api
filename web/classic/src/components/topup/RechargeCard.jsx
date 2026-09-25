/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React, { useEffect, useRef, useState } from 'react';
import {
  Avatar,
  Typography,
  Card,
  Button,
  Banner,
  Skeleton,
  Form,
  Space,
  Row,
  Col,
  Spin,
  Tooltip,
  Tag,
  Tabs,
  TabPane,
  Progress,
} from '@douyinfe/semi-ui';
import { SiAlipay, SiWechat, SiStripe } from 'react-icons/si';
import {
  CreditCard,
  Coins,
  Wallet,
  BarChart2,
  TrendingUp,
  Receipt,
  Sparkles,
} from 'lucide-react';
import { IconGift } from '@douyinfe/semi-icons';
import { useMinimumLoadingTime } from '../../hooks/common/useMinimumLoadingTime';
import { getCurrencyConfig, getQuotaPerUnit } from '../../helpers/render';
import { quotaToPoints, isPointsEnabled } from '../../helpers/quota';
import { buildWalletSummary } from '../../helpers/walletSummary';
import SubscriptionPlansCard from './SubscriptionPlansCard';

const { Text } = Typography;

const RechargeCard = ({
  t,
  enableOnlineTopUp,
  enableStripeTopUp,
  enableCreemTopUp,
  creemProducts,
  creemPreTopUp,
  presetAmounts,
  selectedPreset,
  selectPresetAmount,
  formatLargeNumber,
  priceRatio,
  topUpCount,
  minTopUp,
  renderQuotaWithAmount,
  getAmount,
  setTopUpCount,
  setSelectedPreset,
  renderAmount,
  amountLoading,
  payMethods,
  preTopUp,
  paymentLoading,
  payWay,
  redemptionCode,
  setRedemptionCode,
  topUp,
  isSubmitting,
  topUpLink,
  openTopUpLink,
  userState,
  renderQuota,
  statusLoading,
  topupInfo,
  onOpenHistory,
  enableWaffoTopUp,
  enableWaffoPancakeTopUp,
  enableAlipayDirectTopUp = false,
  enableWxpayDirectTopUp = false,
  directPayQROpen = false,
  subscriptionLoading = false,
  subscriptionPlans = [],
  billingPreference,
  onChangeBillingPreference,
  activeSubscriptions = [],
  allSubscriptions = [],
  reloadSubscriptionSelf,
}) => {
  const onlineFormApiRef = useRef(null);
  const redeemFormApiRef = useRef(null);
  const initialTabSetRef = useRef(false);
  const showAmountSkeleton = useMinimumLoadingTime(amountLoading);
  const [activeTab, setActiveTab] = useState('topup');
  const shouldShowSubscription =
    !subscriptionLoading && subscriptionPlans.length > 0;
  const regularPayMethods = payMethods || [];
  // 页面先回答「还能用多少」：可用总额与后端预扣检查同口径（余额 + 积分 + 授信可用），
  // 三个来源列在下面。授信用户的在途透支只记在授信「已用」里，余额一格截到 0。
  const wallet = buildWalletSummary(userState?.user, {
    pointsEnabled: isPointsEnabled(),
  });
  const credit = wallet.credit;
  // When QuotaDisplayType = CNY, amounts are already in CNY — skip Price conversion.
  const isCNYDisplay =
    (localStorage.getItem('quota_display_type') || 'USD') === 'CNY';
  // Effective price ratio: 1 in CNY mode (no conversion), otherwise use priceRatio.
  const effectivePriceRatio = isCNYDisplay ? 1 : priceRatio;
  // Show conflict error when CNY mode AND standard (non-direct) methods are enabled.
  const hasMixedPayment =
    isCNYDisplay &&
    (enableAlipayDirectTopUp || enableWxpayDirectTopUp) &&
    (enableOnlineTopUp ||
      enableStripeTopUp ||
      enableWaffoTopUp ||
      enableWaffoPancakeTopUp ||
      enableCreemTopUp);
  // True when only direct-pay methods (Alipay/WeChat) are available — input is yuan.
  const isDirectPayOnly =
    !enableOnlineTopUp &&
    !enableStripeTopUp &&
    !enableWaffoTopUp &&
    !enableWaffoPancakeTopUp &&
    (enableAlipayDirectTopUp || enableWxpayDirectTopUp);

  useEffect(() => {
    if (initialTabSetRef.current) return;
    if (subscriptionLoading) return;
    setActiveTab(shouldShowSubscription ? 'subscription' : 'topup');
    initialTabSetRef.current = true;
  }, [shouldShowSubscription, subscriptionLoading]);

  useEffect(() => {
    if (!shouldShowSubscription && activeTab !== 'topup') {
      setActiveTab('topup');
    }
  }, [shouldShowSubscription, activeTab]);
  // 放在 Tabs 外面：有套餐时页面默认落在「订阅套餐」页签，放进「额度充值」里就看不到了。
  // 按信用卡的方式呈现：额度 / 已用 进度条，待结清就是已用（含在途透支），超限变红。
  const creditPanel = credit ? (
    <div
      className='rounded-lg border p-3 mb-3'
      style={{
        borderColor:
          credit.over > 0
            ? 'var(--semi-color-danger)'
            : 'var(--semi-color-border)',
      }}
      data-testid='credit-panel'
    >
      <div className='flex items-center justify-between mb-2'>
        <div className='flex items-center gap-2 text-sm'>
          <CreditCard size={16} />
          <Text strong>{t('授信额度')}</Text>
          <Text>{renderQuota(credit.limit)}</Text>
        </div>
        <Tag
          color={credit.over > 0 ? 'red' : 'green'}
          shape='circle'
          size='small'
        >
          {credit.over > 0 ? t('已超限') : t('正常')}
        </Tag>
      </div>
      <Progress
        percent={
          credit.limit > 0
            ? Math.min(Math.round((credit.used / credit.limit) * 100), 100)
            : 0
        }
        showInfo={false}
        size='small'
        stroke={
          credit.over > 0
            ? 'var(--semi-color-danger)'
            : 'var(--semi-color-primary)'
        }
        aria-label={t('授信额度')}
      />
      <div className='flex items-center justify-between text-xs text-gray-500 mt-2 flex-wrap gap-1'>
        <span>
          {t('待结清')} {renderQuota(credit.used)}
          {' · '}
          {t('按约定周期结算')}
        </span>
        <span>
          {credit.over > 0
            ? `${t('已超出')} ${renderQuota(credit.over)}，${t('新的请求将被拒绝，请尽快结算')}`
            : `${t('可用')} ${renderQuota(credit.available)}`}
        </span>
      </div>
    </div>
  ) : null;

  const topupContent = (
    <Space vertical style={{ width: '100%' }}>
      {/* 统计数据 */}
      <Card
        className='!rounded-xl w-full'
        cover={
          <div
            className='relative'
            style={{
              '--palette-primary-darkerChannel': '37 99 235',
              backgroundImage: `linear-gradient(0deg, rgba(var(--palette-primary-darkerChannel) / 80%), rgba(var(--palette-primary-darkerChannel) / 80%)), url('/cover-4.webp')`,
              backgroundSize: 'cover',
              backgroundPosition: 'center',
              backgroundRepeat: 'no-repeat',
            }}
          >
            <div className='relative z-10 h-full flex flex-col justify-between p-4'>
              <div className='flex justify-between items-center'>
                <Text strong style={{ color: 'white', fontSize: '16px' }}>
                  {t('账户统计')}
                </Text>
              </div>

              {/* 可用总额：唯一的大数字，与后端预扣检查同口径 */}
              <div className='mt-3'>
                <div className='flex items-end gap-3 flex-wrap'>
                  <div
                    className='text-2xl sm:text-3xl font-bold'
                    style={{ color: 'white' }}
                    data-testid='wallet-available'
                  >
                    {renderQuota(wallet.available)}
                  </div>
                  <div className='flex items-center pb-1'>
                    <Wallet
                      size={14}
                      className='mr-1'
                      style={{ color: 'rgba(255,255,255,0.8)' }}
                    />
                    <Text
                      style={{
                        color: 'rgba(255,255,255,0.8)',
                        fontSize: '12px',
                      }}
                    >
                      {t('可用总额')}
                    </Text>
                    {wallet.overdue > 0 && (
                      <Tag color='red' size='small' className='ml-2'>
                        {t('欠费')} {renderQuota(wallet.overdue)}
                      </Tag>
                    )}
                  </div>
                </div>

                {/* 三个来源 */}
                <div
                  className='flex flex-wrap gap-x-4 gap-y-1 mt-2 text-xs'
                  style={{ color: 'rgba(255,255,255,0.9)' }}
                >
                  <span>
                    {t('账户余额')} {renderQuota(wallet.balance)}
                  </span>
                  {wallet.showPoints && (
                    <span className='flex items-center'>
                      <Coins size={12} className='mr-1' />
                      {t('积分')} {quotaToPoints(wallet.points)}
                      {`（≈${renderQuota(wallet.points)}）`}
                    </span>
                  )}
                  {credit && (
                    <span className='flex items-center'>
                      <CreditCard size={12} className='mr-1' />
                      {t('授信可用')} {renderQuota(wallet.creditAvailable)}
                    </span>
                  )}
                </div>

                {/* 扣费顺序 + 累计统计 */}
                <div
                  className='flex flex-wrap justify-between gap-x-4 gap-y-1 mt-3 text-xs'
                  style={{ color: 'rgba(255,255,255,0.75)' }}
                >
                  <span>
                    {t('自动扣费顺序')}：
                    {[
                      wallet.showPoints ? t('积分') : null,
                      t('余额'),
                      credit ? t('授信') : null,
                    ]
                      .filter(Boolean)
                      .join(' → ')}
                  </span>
                  <span className='flex items-center gap-x-3'>
                    <Tooltip
                      content={t(
                        '按账单原价累计，含积分抵扣与套餐内用量，不等于余额减少额',
                      )}
                    >
                      <span className='flex items-center cursor-help'>
                        <TrendingUp size={12} className='mr-1' />
                        {t('累计消费')}{' '}
                        {renderQuota(userState?.user?.used_quota)}
                      </span>
                    </Tooltip>
                    <span className='flex items-center'>
                      <BarChart2 size={12} className='mr-1' />
                      {t('请求')} {userState?.user?.request_count || 0}{' '}
                      {t('次')}
                    </span>
                  </span>
                </div>
              </div>
            </div>
          </div>
        }
      >
        {/* 在线充值表单 */}
        {hasMixedPayment && (
          <Banner
            type='danger'
            description={t(
              '检测到配置冲突：人民币直连支付（支付宝/微信）与其他支付方式不能同时启用。直连支付额度以人民币计算，其他通道以汇率换算，混用会导致金额不一致。请在管理后台仅保留一种支付体系。',
            )}
            style={{ marginBottom: 12 }}
          />
        )}
        {statusLoading ? (
          <div className='py-8 flex justify-center'>
            <Spin size='large' />
          </div>
        ) : enableOnlineTopUp ||
          enableStripeTopUp ||
          enableCreemTopUp ||
          enableWaffoTopUp ||
          enableWaffoPancakeTopUp ||
          enableAlipayDirectTopUp ||
          enableWxpayDirectTopUp ? (
          <Form
            getFormApi={(api) => (onlineFormApiRef.current = api)}
            initValues={{ topUpCount: topUpCount }}
          >
            <div className='space-y-6'>
              {(enableOnlineTopUp ||
                enableStripeTopUp ||
                enableWaffoTopUp ||
                enableWaffoPancakeTopUp ||
                enableAlipayDirectTopUp ||
                enableWxpayDirectTopUp) && (
                <Row gutter={12}>
                  <Col xs={24} sm={24} md={24} lg={10} xl={10}>
                    <Form.InputNumber
                      field='topUpCount'
                      label={t('充值额度')}
                      disabled={
                        !enableOnlineTopUp &&
                        !enableStripeTopUp &&
                        !enableWaffoTopUp &&
                        !enableWaffoPancakeTopUp &&
                        !enableAlipayDirectTopUp &&
                        !enableWxpayDirectTopUp
                      }
                      placeholder={
                        t('充值额度，最低 ') + renderQuotaWithAmount(minTopUp)
                      }
                      value={topUpCount}
                      min={minTopUp}
                      max={999999999}
                      step={1}
                      precision={0}
                      onChange={async (value) => {
                        if (value && value > 0) {
                          setTopUpCount(value);
                          setSelectedPreset(null);
                          await getAmount(
                            value,
                            isDirectPayOnly ? 'direct' : undefined,
                          );
                        }
                      }}
                      onBlur={(e) => {
                        const value = parseInt(e.target.value);
                        if (!value || value < minTopUp) {
                          setTopUpCount(minTopUp);
                          getAmount(
                            minTopUp,
                            isDirectPayOnly ? 'direct' : undefined,
                          );
                        }
                      }}
                      formatter={(value) => (value ? `${value}` : '')}
                      parser={(value) =>
                        value ? parseInt(value.replace(/[^\d]/g, '')) : 0
                      }
                      extraText={
                        <Skeleton
                          loading={showAmountSkeleton}
                          active
                          placeholder={
                            <Skeleton.Title
                              style={{
                                width: 120,
                                height: 20,
                                borderRadius: 6,
                              }}
                            />
                          }
                        >
                          <Text type='secondary' className='text-red-600'>
                            {t('实付金额：')}
                            <span style={{ color: 'red' }}>
                              {renderAmount()}
                            </span>
                          </Text>
                        </Skeleton>
                      }
                      style={{ width: '100%' }}
                    />
                  </Col>
                  {regularPayMethods.length > 0 && (
                    <Col xs={24} sm={24} md={24} lg={14} xl={14}>
                      <Form.Slot label={t('选择支付方式')}>
                        <Space wrap>
                          {regularPayMethods.map((payMethod) => {
                            const minTopupVal =
                              Number(payMethod.min_topup) || 0;
                            const isStripe = payMethod.type === 'stripe';
                            const isWaffo =
                              typeof payMethod.type === 'string' &&
                              payMethod.type.startsWith('waffo:');
                            const isWaffoPancake =
                              payMethod.type === 'waffo_pancake';
                            const isAlipayDirect =
                              payMethod.type === 'alipay_direct';
                            const isWxpayDirect =
                              payMethod.type === 'wxpay_direct';
                            const disabled =
                              (!enableOnlineTopUp &&
                                !isStripe &&
                                !isWaffo &&
                                !isWaffoPancake &&
                                !isAlipayDirect &&
                                !isWxpayDirect) ||
                              (!enableStripeTopUp && isStripe) ||
                              (!enableWaffoTopUp && isWaffo) ||
                              (!enableWaffoPancakeTopUp && isWaffoPancake) ||
                              (!enableAlipayDirectTopUp && isAlipayDirect) ||
                              (!enableWxpayDirectTopUp && isWxpayDirect) ||
                              minTopupVal > Number(topUpCount || 0) ||
                              // Disable all payment buttons while a payment
                              // flow is in progress (request in-flight or
                              // direct-pay QR popup open) — prevents spam
                              // clicks and accidental switching.
                              paymentLoading ||
                              directPayQROpen;

                            const buttonEl = (
                              <Button
                                key={payMethod.type}
                                theme='outline'
                                type='tertiary'
                                onClick={() => preTopUp(payMethod.type)}
                                disabled={disabled}
                                loading={
                                  paymentLoading && payWay === payMethod.type
                                }
                                icon={
                                  payMethod.type === 'alipay' ||
                                  payMethod.type === 'alipay_direct' ? (
                                    <SiAlipay size={18} color='#1677FF' />
                                  ) : payMethod.type === 'wxpay' ||
                                    payMethod.type === 'wxpay_direct' ? (
                                    <SiWechat size={18} color='#07C160' />
                                  ) : payMethod.type === 'stripe' ? (
                                    <SiStripe size={18} color='#635BFF' />
                                  ) : payMethod.icon ? (
                                    <img
                                      src={payMethod.icon}
                                      alt={payMethod.name}
                                      style={{
                                        width: 18,
                                        height: 18,
                                        objectFit: 'contain',
                                      }}
                                    />
                                  ) : payMethod.type === 'waffo_pancake' ? (
                                    <CreditCard
                                      size={18}
                                      color='var(--semi-color-primary)'
                                    />
                                  ) : (
                                    <CreditCard
                                      size={18}
                                      color={
                                        payMethod.color ||
                                        'var(--semi-color-text-2)'
                                      }
                                    />
                                  )
                                }
                                className='!rounded-lg !px-4 !py-2'
                              >
                                {payMethod.name}
                              </Button>
                            );

                            return disabled &&
                              minTopupVal > Number(topUpCount || 0) ? (
                              <Tooltip
                                content={
                                  t('此支付方式最低充值金额为') +
                                  ' ' +
                                  minTopupVal
                                }
                                key={payMethod.type}
                              >
                                {buttonEl}
                              </Tooltip>
                            ) : (
                              <React.Fragment key={payMethod.type}>
                                {buttonEl}
                              </React.Fragment>
                            );
                          })}
                        </Space>
                      </Form.Slot>
                    </Col>
                  )}
                </Row>
              )}

              {(enableOnlineTopUp ||
                enableStripeTopUp ||
                enableWaffoTopUp ||
                enableAlipayDirectTopUp ||
                enableWxpayDirectTopUp) && (
                <Form.Slot
                  label={
                    <div className='flex items-center gap-2'>
                      <span>{t('选择充值额度')}</span>
                      {(() => {
                        const { symbol, rate, type } = getCurrencyConfig();
                        // Hide exchange rate hint in CNY display mode
                        if (type === 'USD' || isCNYDisplay) return null;

                        return (
                          <span
                            style={{
                              color: 'var(--semi-color-text-2)',
                              fontSize: '12px',
                              fontWeight: 'normal',
                            }}
                          >
                            (1 $ = {rate.toFixed(2)} {symbol})
                          </span>
                        );
                      })()}
                    </div>
                  }
                >
                  <div className='grid grid-cols-2 sm:grid-cols-3 md:grid-cols-4 gap-2'>
                    {presetAmounts.map((preset, index) => {
                      const discount =
                        preset.discount ||
                        topupInfo?.discount?.[preset.value] ||
                        1.0;
                      const originalPrice = preset.value * effectivePriceRatio;
                      const discountedPrice = originalPrice * discount;
                      const hasDiscount = discount < 1.0;
                      const actualPay = discountedPrice;
                      const save = originalPrice - discountedPrice;

                      // 根据当前货币类型换算显示金额和数量
                      const { symbol, rate, type } = getCurrencyConfig();
                      const statusStr = localStorage.getItem('status');
                      let usdRate = 7; // 默认CNY汇率
                      try {
                        if (statusStr) {
                          const s = JSON.parse(statusStr);
                          usdRate = s?.usd_exchange_rate || 7;
                        }
                      } catch (e) {}

                      let displayValue = preset.value; // 显示的数量
                      let displayActualPay = actualPay;
                      let displaySave = save;

                      if (isCNYDisplay) {
                        // CNY display mode: amounts are already in CNY, no conversion
                      } else if (type === 'USD') {
                        // 数量保持USD，价格从CNY转USD
                        displayActualPay = actualPay / usdRate;
                        displaySave = save / usdRate;
                      } else if (type === 'CNY') {
                        // 数量转CNY，价格已是CNY
                        displayValue = preset.value * usdRate;
                      } else if (type === 'CUSTOM') {
                        // 数量和价格都转自定义货币
                        displayValue = preset.value * rate;
                        displayActualPay = (actualPay / usdRate) * rate;
                        displaySave = (save / usdRate) * rate;
                      }

                      return (
                        <Card
                          key={index}
                          style={{
                            cursor: 'pointer',
                            border:
                              selectedPreset === preset.value
                                ? '2px solid var(--semi-color-primary)'
                                : '1px solid var(--semi-color-border)',
                            height: '100%',
                            width: '100%',
                          }}
                          bodyStyle={{ padding: '12px' }}
                          onClick={() => {
                            selectPresetAmount(preset);
                            onlineFormApiRef.current?.setValue(
                              'topUpCount',
                              preset.value,
                            );
                          }}
                        >
                          <div style={{ textAlign: 'center' }}>
                            <Typography.Title
                              heading={6}
                              style={{ margin: '0 0 8px 0' }}
                            >
                              <Coins size={18} />
                              {formatLargeNumber(displayValue)} {symbol}
                              {hasDiscount && (
                                <Tag style={{ marginLeft: 4 }} color='green'>
                                  {t('折').includes('off')
                                    ? (
                                        (1 - parseFloat(discount)) *
                                        100
                                      ).toFixed(1)
                                    : (discount * 10).toFixed(1)}
                                  {t('折')}
                                </Tag>
                              )}
                            </Typography.Title>
                            <div
                              style={{
                                color: 'var(--semi-color-text-2)',
                                fontSize: '12px',
                                margin: '4px 0',
                              }}
                            >
                              {t('实付')} {symbol}
                              {displayActualPay.toFixed(2)}，
                              {hasDiscount
                                ? `${t('节省')} ${symbol}${displaySave.toFixed(2)}`
                                : `${t('节省')} ${symbol}0.00`}
                            </div>
                          </div>
                        </Card>
                      );
                    })}
                  </div>
                </Form.Slot>
              )}

              {/* Creem 充值区域 */}
              {enableCreemTopUp && creemProducts.length > 0 && (
                <Form.Slot label={t('Creem 充值')}>
                  <div className='grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 gap-3'>
                    {creemProducts.map((product, index) => (
                      <Card
                        key={index}
                        onClick={() => creemPreTopUp(product)}
                        className='cursor-pointer !rounded-2xl transition-all hover:shadow-md border-gray-200 hover:border-gray-300'
                        bodyStyle={{ textAlign: 'center', padding: '16px' }}
                      >
                        <div className='font-medium text-lg mb-2'>
                          {product.name}
                        </div>
                        <div className='text-sm text-gray-600 mb-2'>
                          {t('充值额度')}: {product.quota}
                        </div>
                        <div className='text-lg font-semibold text-blue-600'>
                          {product.currency === 'EUR' ? '€' : '$'}
                          {product.price}
                        </div>
                      </Card>
                    ))}
                  </div>
                </Form.Slot>
              )}
            </div>
          </Form>
        ) : (
          <Banner
            type='info'
            description={t(
              '管理员未开启在线充值功能，请联系管理员开启或使用兑换码充值。',
            )}
            className='!rounded-xl'
            closeIcon={null}
          />
        )}
      </Card>

      {/* 兑换码充值 */}
      <Card
        className='!rounded-xl w-full'
        title={
          <Text type='tertiary' strong>
            {t('兑换码充值')}
          </Text>
        }
      >
        <Form
          getFormApi={(api) => (redeemFormApiRef.current = api)}
          initValues={{ redemptionCode: redemptionCode }}
        >
          <Form.Input
            field='redemptionCode'
            noLabel={true}
            placeholder={t('请输入兑换码')}
            value={redemptionCode}
            onChange={(value) => setRedemptionCode(value)}
            prefix={<IconGift />}
            suffix={
              <div className='flex items-center gap-2'>
                <Button
                  type='primary'
                  theme='solid'
                  onClick={topUp}
                  loading={isSubmitting}
                >
                  {t('兑换额度')}
                </Button>
              </div>
            }
            showClear
            style={{ width: '100%' }}
            extraText={
              topUpLink && (
                <Text type='tertiary'>
                  {t('在找兑换码？')}
                  <Text
                    type='secondary'
                    underline
                    className='cursor-pointer'
                    onClick={openTopUpLink}
                  >
                    {t('购买兑换码')}
                  </Text>
                </Text>
              )
            }
          />
        </Form>
      </Card>
    </Space>
  );

  return (
    <Card className='!rounded-2xl shadow-sm border-0'>
      {/* 卡片头部 */}
      <div className='flex items-center justify-between mb-4'>
        <div className='flex items-center'>
          <Avatar size='small' color='blue' className='mr-3 shadow-md'>
            <CreditCard size={16} />
          </Avatar>
          <div>
            <Typography.Text className='text-lg font-medium'>
              {t('账户充值')}
            </Typography.Text>
            <div className='text-xs'>{t('多种充值方式，安全便捷')}</div>
          </div>
        </div>
        <Button
          icon={<Receipt size={16} />}
          theme='solid'
          onClick={onOpenHistory}
        >
          {t('账单')}
        </Button>
      </div>

      {creditPanel}
      {shouldShowSubscription ? (
        <Tabs type='card' activeKey={activeTab} onChange={setActiveTab}>
          <TabPane
            tab={
              <div className='flex items-center gap-2'>
                <Sparkles size={16} />
                {t('订阅套餐')}
              </div>
            }
            itemKey='subscription'
          >
            <div className='py-2'>
              <SubscriptionPlansCard
                t={t}
                loading={subscriptionLoading}
                plans={subscriptionPlans}
                payMethods={payMethods}
                enableOnlineTopUp={enableOnlineTopUp}
                enableStripeTopUp={enableStripeTopUp}
                enableCreemTopUp={enableCreemTopUp}
                enableAlipayDirectTopUp={enableAlipayDirectTopUp}
                enableWxpayDirectTopUp={enableWxpayDirectTopUp}
                billingPreference={billingPreference}
                onChangeBillingPreference={onChangeBillingPreference}
                activeSubscriptions={activeSubscriptions}
                allSubscriptions={allSubscriptions}
                reloadSubscriptionSelf={reloadSubscriptionSelf}
                withCard={false}
              />
            </div>
          </TabPane>
          <TabPane
            tab={
              <div className='flex items-center gap-2'>
                <Wallet size={16} />
                {t('额度充值')}
              </div>
            }
            itemKey='topup'
          >
            <div className='py-2'>{topupContent}</div>
          </TabPane>
        </Tabs>
      ) : (
        topupContent
      )}
    </Card>
  );
};

export default RechargeCard;
