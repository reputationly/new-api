import React, { useEffect, useMemo, useState } from 'react';
import { Table, Tag, Typography } from '@douyinfe/semi-ui';
import { API } from '../../helpers';
import { buildPlanComparisonRows } from '../../helpers/planComparison';

const { Text } = Typography;

/**
 * 套餐对比表（对外换算表，设计文档 §8.3）。
 *
 * 「50,000 点」客户听不出多少，「≈ 1,250 张高清图」听得出来。数字全部由当前
 * 模型价格实时算出，调价后自动跟随，不需要人维护。
 *
 * 没有任何算力点 / 权益套餐时整个组件不渲染——老式按额度的套餐页面保持原样。
 */
const PlanComparisonTable = ({ plans = [], t }) => {
  const [comparison, setComparison] = useState(null);

  useEffect(() => {
    let cancelled = false;
    API.get('/api/subscription/plans/comparison')
      .then((res) => {
        if (!cancelled && res.data?.success) setComparison(res.data.data);
      })
      // 对比表是锦上添花，拿不到就不出，不打断购买流程
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, []);

  const table = useMemo(
    () => buildPlanComparisonRows(comparison, plans),
    [comparison, plans],
  );
  if (!table) return null;

  const hasChannelLimited = table.rows.some((r) =>
    r.cells.some((c) => c.channelLimited),
  );
  const hasEstimate = table.rows.some((r) => r.kind === 'estimate');
  const hasLimit = table.rows.some((r) => r.kind === 'limit');

  const columns = [
    {
      title: '',
      dataIndex: 'label',
      render: (_, row) => (
        <span className='inline-flex items-center gap-1'>
          <Text strong={row.kind === 'points'}>
            {row.kind === 'points' ? t(row.label) : row.label}
          </Text>
          {row.kind === 'limit' && (
            <Tag size='small' color='teal'>
              {t('次数上限')}
            </Tag>
          )}
        </span>
      ),
    },
    ...table.columns.map((col, i) => ({
      title: col.title,
      key: `plan-${col.planId}`,
      render: (_, row) => {
        const cell = row.cells[i];
        return (
          <Text strong={row.kind === 'points'}>
            {cell.text}
            {cell.channelLimited ? ' *' : ''}
          </Text>
        );
      },
    })),
  ];

  return (
    <div className='w-full px-1'>
      <Text strong>{t('套餐对比')}</Text>
      <Table
        className='mt-2'
        size='small'
        columns={columns}
        dataSource={table.rows}
        rowKey='key'
        pagination={false}
        bordered
      />
      <div className='mt-2 flex flex-col gap-1'>
        {hasEstimate && (
          <Text size='small' type='tertiary'>
            {t(
              '≈ / 约 为按当前标准价估算的每期可用量，不同参数下实际生成数量会有差异',
            )}
          </Text>
        )}
        {hasLimit && (
          <Text size='small' type='tertiary'>
            {t(
              '次数上限为每个周期的硬性额度；次数或算力点用完后，该模型改按余额计费',
            )}
          </Text>
        )}
        {hasChannelLimited && (
          <Text size='small' type='tertiary'>
            {t('* 仅在特定渠道上按套餐计费')}
          </Text>
        )}
      </div>
    </div>
  );
};

export default PlanComparisonTable;
