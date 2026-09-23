import React from 'react';
import { Progress, Typography } from '@douyinfe/semi-ui';
import { quotaToComputePoints } from '../../helpers/quota';
import {
  buildUsageRows,
  buildLowUsageWarnings,
} from '../../helpers/subscriptionUsage';

const { Text } = Typography;

const formatDate = (ts) =>
  ts > 0 ? new Date(ts * 1000).toLocaleDateString() : '';

/**
 * 一个活跃订阅的算力点与权益次数余量（设计文档 §10.2）。
 *
 * 进度条表示**剩余**而不是已用：用户关心的是「还能用多少」。余量低于 20% 标橙，
 * 并提前说明用尽后会发生什么——等到被按余额扣费时才发现，客诉就已经产生了。
 */
const SubscriptionUsagePanel = ({ summary, t }) => {
  const rows = buildUsageRows(summary, quotaToComputePoints);
  if (rows.length === 0) return null;
  const warnings = buildLowUsageWarnings(rows);

  return (
    <div className='mb-2'>
      {rows.map((row) => (
        <div key={row.key} className='text-xs mb-1'>
          <div className='flex items-center justify-between gap-2'>
            <span className='font-medium truncate'>
              {row.kind === 'points' ? t('算力点') : row.label}
            </span>
            <span className='text-gray-500 shrink-0'>
              {row.kind === 'unlimited'
                ? t('不限次')
                : `${row.remain.toLocaleString('en-US')} / ${row.total.toLocaleString('en-US')}${row.kind === 'count' ? ` ${t('次')}` : ''}`}
            </span>
          </div>
          {row.kind !== 'unlimited' && (
            <Progress
              percent={Math.round(row.ratio * 100)}
              showInfo={false}
              size='small'
              stroke={
                row.low
                  ? 'var(--semi-color-warning)'
                  : 'var(--semi-color-primary)'
              }
              aria-label={row.label}
            />
          )}
          <div className='flex items-center justify-between text-gray-500'>
            <span>{row.note}</span>
            {row.resetAt > 0 && (
              <span>
                {formatDate(row.resetAt)}{' '}
                {row.resetKind === 'reset' ? t('重置') : t('到期')}
              </span>
            )}
          </div>
        </div>
      ))}
      {warnings.map((w) => (
        <Text
          key={w}
          size='small'
          style={{ color: 'var(--semi-color-warning)', display: 'block' }}
        >
          ⚠ {w}
        </Text>
      ))}
      {/* 走到这里说明是新式套餐（有算力点或权益）：套餐覆盖不到的都按余额计费 */}
      <Text size='small' type='tertiary' style={{ display: 'block' }}>
        {t('超出套餐的部分按账户余额计费')}
      </Text>
    </div>
  );
};

export default SubscriptionUsagePanel;
