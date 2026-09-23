import React from 'react';
import { ProgressBar } from 'antd-mobile';

// 与 PC 端「我的订阅」共用同一份余量口径（行构造、20% 阈值、提示文案），
// 两端各写一份的话，同一个套餐在手机和电脑上会显示不同的「还剩多少」。
import {
  buildUsageRows,
  buildLowUsageWarnings,
} from '@classic/helpers/subscriptionUsage';

import { quotaToComputePoints } from '../utils/quota';

const WARN = '#d46b08';

const formatDate = (ts) =>
  ts > 0 ? new Date(ts * 1000).toLocaleDateString() : '';

// 一个活跃订阅的算力点与权益次数余量（设计文档 §10.3：mobile 只展示余量，不做管理）。
const SubscriptionUsageCard = ({ summary }) => {
  const rows = buildUsageRows(summary, quotaToComputePoints);
  if (rows.length === 0) return null;
  const warnings = buildLowUsageWarnings(rows);
  const endTime = summary?.subscription?.end_time;

  return (
    <div className='m-list-card' style={{ padding: '12px 14px' }}>
      <div
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          alignItems: 'baseline',
          marginBottom: 8,
        }}
      >
        <span style={{ fontWeight: 600, fontSize: 15 }}>
          {summary.plan_title || '套餐'}
        </span>
        {endTime > 0 && (
          <span style={{ fontSize: 12, color: '#9aa1ad' }}>
            有效期至 {formatDate(endTime)}
          </span>
        )}
      </div>
      {rows.map((row) => (
        <div key={row.key} style={{ marginBottom: 10 }}>
          <div
            style={{
              display: 'flex',
              justifyContent: 'space-between',
              fontSize: 13,
              gap: 8,
            }}
          >
            <span
              style={{
                overflow: 'hidden',
                textOverflow: 'ellipsis',
                whiteSpace: 'nowrap',
              }}
            >
              {row.label}
            </span>
            <span style={{ flexShrink: 0, color: '#6b7280' }}>
              {row.kind === 'unlimited'
                ? '不限次'
                : `${row.remain.toLocaleString('en-US')} / ${row.total.toLocaleString('en-US')}${row.kind === 'count' ? ' 次' : ''}`}
            </span>
          </div>
          {row.kind !== 'unlimited' && (
            <ProgressBar
              percent={Math.round(row.ratio * 100)}
              style={{
                '--fill-color': row.low ? WARN : 'var(--brand-primary)',
                '--track-width': '4px',
                marginTop: 4,
              }}
            />
          )}
          <div
            style={{
              display: 'flex',
              justifyContent: 'space-between',
              fontSize: 11.5,
              color: '#9aa1ad',
              marginTop: 2,
            }}
          >
            <span>{row.note}</span>
            {row.resetAt > 0 && (
              <span>
                {formatDate(row.resetAt)}{' '}
                {row.resetKind === 'reset' ? '重置' : '到期'}
              </span>
            )}
          </div>
        </div>
      ))}
      {warnings.map((w) => (
        <div key={w} style={{ fontSize: 12, color: WARN, marginTop: 2 }}>
          ⚠ {w}
        </div>
      ))}
      <div style={{ fontSize: 11.5, color: '#9aa1ad', marginTop: 6 }}>
        超出套餐的部分按账户余额计费
      </div>
    </div>
  );
};

export default SubscriptionUsageCard;
