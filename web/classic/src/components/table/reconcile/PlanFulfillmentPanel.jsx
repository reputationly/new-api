import React, { useCallback, useEffect, useState } from 'react';
import {
  Card,
  DatePicker,
  Descriptions,
  Spin,
  Table,
  Typography,
} from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import { API, showError } from '../../../helpers';

const { Text } = Typography;

// 套餐经营（履约率）面板。设计见 docs/subscription-entitlement-design.md §8.4、P7。
//
//   履约率 = 外采成本 ÷ 套餐收入
//
// 这个数直接回答「套餐定价亏不亏」。成本只算有记录的（请求时按渠道成本比落库），
// 自有算力或没配成本比的调用单独列出——数字只会偏低不会虚高。

export const fen2yuan = (fen) => (Number(fen || 0) / 100).toFixed(2);

export const formatRate = (v) =>
  v === null || v === undefined ? '—' : `${(v * 100).toFixed(1)}%`;

// 履约率 ≥ 100%：成本超过了收入，这个套餐在亏钱
export const isLossRate = (v) => v !== null && v !== undefined && v >= 1;

export default function PlanFulfillmentPanel() {
  const { t } = useTranslation();
  const now = Math.floor(Date.now() / 1000);
  const [range, setRange] = useState([
    new Date((now - 30 * 24 * 3600) * 1000),
    new Date(now * 1000),
  ]);
  const [loading, setLoading] = useState(false);
  const [report, setReport] = useState(null);

  const startTs = Math.floor(range?.[0]?.getTime() / 1000) || 0;
  const endTs = Math.floor(range?.[1]?.getTime() / 1000) || 0;

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const res = await API.get(
        `/api/reconcile/admin/plan/fulfillment?start=${startTs}&end=${endTs}`,
      );
      if (res.data?.success) setReport(res.data.data);
      else showError(res.data?.message);
    } catch (e) {
      showError(e.message);
    }
    setLoading(false);
  }, [startTs, endTs]);

  useEffect(() => {
    if (startTs && endTs) load();
  }, [startTs, endTs, load]);

  const rateCell = (v) => (
    <Text
      strong
      style={isLossRate(v) ? { color: 'var(--semi-color-danger)' } : undefined}
    >
      {formatRate(v)}
      {isLossRate(v) ? ` ${t('亏损')}` : ''}
    </Text>
  );

  const columns = [
    {
      title: t('套餐'),
      dataIndex: 'plan_title',
      render: (v, r) => v || `#${r.plan_id}`,
    },
    { title: t('售出'), dataIndex: 'order_count' },
    {
      title: t('收入'),
      dataIndex: 'revenue_fen',
      render: (v) => `¥${fen2yuan(v)}`,
    },
    { title: t('套餐内调用'), dataIndex: 'call_count' },
    {
      title: t('等值金额'),
      dataIndex: 'value_fen',
      render: (v) => `¥${fen2yuan(v)}`,
    },
    {
      title: t('外采成本'),
      dataIndex: 'cost_fen',
      render: (v) => `¥${fen2yuan(v)}`,
    },
    {
      title: t('履约率'),
      dataIndex: 'fulfillment_rate',
      render: rateCell,
    },
    {
      title: t('成本未知'),
      dataIndex: 'uncosted_call_count',
      render: (v, r) =>
        Number(v) > 0 ? (
          <Text type='tertiary'>
            {v} {t('次')} · ¥{fen2yuan(r.uncosted_value_fen)}
          </Text>
        ) : (
          '—'
        ),
    },
    {
      title: t('超额'),
      dataIndex: 'overage_count',
      render: (v, r) =>
        Number(v) > 0 ? `${v} ${t('次')} · ¥${fen2yuan(r.overage_fen)}` : '—',
    },
    {
      title: t('过期作废'),
      dataIndex: 'expired_unused_ratio',
      render: (v, r) =>
        v === null || v === undefined ? (
          '—'
        ) : (
          <span>
            {formatRate(v)}
            <Text type='tertiary' size='small'>
              {' '}
              ({r.points_expired_unused.toLocaleString('en-US')}/
              {r.points_expired_granted.toLocaleString('en-US')})
            </Text>
          </span>
        ),
    },
  ];

  const total = report?.total;

  return (
    <div className='flex flex-col gap-3'>
      <Card>
        <div className='flex items-center gap-3 flex-wrap'>
          <DatePicker
            type='dateTimeRange'
            value={range}
            onChange={(v) => setRange(v)}
          />
          <Text type='tertiary' size='small'>
            {t(
              '收入与消耗不同时发生（月初售卖、全月消耗），短窗口下履约率会波动',
            )}
          </Text>
        </div>
      </Card>

      <Spin spinning={loading}>
        {total && (
          <Card title={t('套餐经营总览')}>
            <Descriptions
              row
              data={[
                {
                  key: t('套餐收入'),
                  value: `¥${fen2yuan(total.revenue_fen)}`,
                },
                { key: t('外采成本'), value: `¥${fen2yuan(total.cost_fen)}` },
                {
                  key: t('履约率'),
                  value: rateCell(total.fulfillment_rate),
                },
                {
                  key: t('等值金额'),
                  value: `¥${fen2yuan(total.value_fen)}`,
                },
                {
                  key: t('超额收入'),
                  value: `¥${fen2yuan(total.overage_fen)}`,
                },
                {
                  key: t('过期作废'),
                  value: formatRate(total.expired_unused_ratio),
                },
              ]}
            />
          </Card>
        )}

        <Card title={t('按套餐')} className='mt-3'>
          <Table
            columns={columns}
            dataSource={report?.rows || []}
            rowKey='plan_id'
            pagination={false}
            size='small'
            empty={t('该时间段内没有套餐收入或套餐内调用')}
          />
          <div className='mt-3 flex flex-col gap-1'>
            <Text type='tertiary' size='small'>
              {t(
                '履约率 = 外采成本 ÷ 收入。外采成本取请求时按渠道成本比落库的值，退款已冲销；管理员直接开通的套餐没有收入，履约率显示为 —。',
              )}
            </Text>
            <Text type='tertiary' size='small'>
              {t(
                '成本未知：自有算力或未配置成本比的套餐内调用，不计入外采成本。数量大时先去渠道成本里补配，否则履约率偏低。',
              )}
            </Text>
            <Text type='tertiary' size='small'>
              {t(
                '等值金额：套餐内调用按原价折算的金额，即不买套餐时这些调用要花的钱。超额：被套餐覆盖却因次数、点数或速率上限按余额扣的调用。',
              )}
            </Text>
            <Text type='tertiary' size='small'>
              {t(
                '过期作废：期内已到期的算力点批次里没用完的比例。长期偏高说明套餐点数给多了，或「点数不足整笔按余额计费」让余量卡到了期末。',
              )}
            </Text>
          </div>
        </Card>
      </Spin>
    </div>
  );
}
