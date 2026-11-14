import React, { useMemo } from 'react';
import { Table, Tag, Typography, Tooltip } from '@douyinfe/semi-ui';
import { renderNumber, renderQuota } from '../../helpers';

const { Text } = Typography;

// 百万 tokens，两位小数。小于 0.01 但确实有量时显示「<0.01」，
// 否则运营会以为这一行没有 token 消耗。
function formatMillionTokens(value) {
  const tokens = Number(value) || 0;
  if (tokens <= 0) return '0.00';
  const millions = tokens / 1000000;
  if (millions < 0.01) return '<0.01';
  return millions.toFixed(2);
}

/**
 * 数据看板「分组消耗」表。
 *
 * 维度是**线路（使用分组）**，不是用户档：日志里逐请求记录的只有线路名
 * （logs.group，auto 已被解析成真实分组）。表里「余额消耗」是真正从钱包扣掉的钱，
 * 「总消耗」含积分、授信、套餐/订阅；两者之差就是这些非现金来源。
 *
 * tokens 与金额是两条独立口径（按次计费的模型 tokens 为 0 或与金额无关），
 * 所以这里**不显示「每百万 tokens 单价」**——那个数对按次模型是假的。
 */
const GroupQuotaTable = ({ data, loading, t }) => {
  const rows = useMemo(
    () =>
      (data || []).map((item, index) => ({
        key: `${item.group || '__empty__'}-${index}`,
        group: item.group || '',
        known: item.known !== false,
        count: item.count || 0,
        tokens: item.tokens || 0,
        totalQuota: item.total_quota || 0,
        cashQuota: item.cash_quota || 0,
      })),
    [data],
  );

  const totals = useMemo(
    () =>
      rows.reduce(
        (acc, row) => {
          acc.count += row.count;
          acc.tokens += row.tokens;
          acc.totalQuota += row.totalQuota;
          acc.cashQuota += row.cashQuota;
          return acc;
        },
        { count: 0, tokens: 0, totalQuota: 0, cashQuota: 0 },
      ),
    [rows],
  );

  const columns = useMemo(
    () => [
      {
        title: t('分组（线路）'),
        dataIndex: 'group',
        render: (value, record) => {
          if (!value) {
            return <Text type='tertiary'>{t('未记录分组')}</Text>;
          }
          return (
            <div className='flex items-center gap-2'>
              <Text>{value}</Text>
              {!record.known && (
                <Tooltip
                  content={t('该线路已不在当前分组配置中，历史消耗仍保留')}
                >
                  <Tag size='small' color='grey'>
                    {t('已下线')}
                  </Tag>
                </Tooltip>
              )}
            </div>
          );
        },
      },
      {
        title: t('调用次数'),
        dataIndex: 'count',
        render: (value) => renderNumber(value),
      },
      {
        title: (
          <Tooltip
            content={t(
              '输入 + 输出 tokens 合计。按次计费的模型 tokens 可能为 0 或与金额无关',
            )}
          >
            <span>{t('Tokens（百万）')}</span>
          </Tooltip>
        ),
        dataIndex: 'tokens',
        render: (value) => formatMillionTokens(value),
      },
      {
        title: (
          <Tooltip
            content={t(
              '实际从钱包余额扣掉的钱。已按退款冲销，不含积分抵扣、授信与套餐权益',
            )}
          >
            <span>{t('余额消耗')}</span>
          </Tooltip>
        ),
        dataIndex: 'cashQuota',
        render: (value) => renderQuota(value, 2),
      },
      {
        title: (
          <Tooltip
            content={t(
              '本期计费总额，含积分抵扣、授信与套餐/订阅。与「余额消耗」之差就是这些非现金来源',
            )}
          >
            <span>{t('总消耗')}</span>
          </Tooltip>
        ),
        dataIndex: 'totalQuota',
        render: (value) => renderQuota(value, 2),
      },
    ],
    [t],
  );

  return (
    <div className='h-full overflow-auto flex flex-col gap-2'>
      <div className='flex flex-wrap items-center gap-2 text-xs text-gray-500 px-1'>
        <Text strong>{t('总计')}</Text>
        <span>
          {t('调用')} {renderNumber(totals.count)}
        </span>
        <span>{formatMillionTokens(totals.tokens)} M tokens</span>
        <span>
          {t('余额消耗')} {renderQuota(totals.cashQuota, 2)}
        </span>
        <span>
          {t('总消耗')} {renderQuota(totals.totalQuota, 2)}
        </span>
      </div>
      <Table
        columns={columns}
        dataSource={rows}
        loading={loading}
        pagination={false}
        size='small'
        scroll={{ x: 'max-content' }}
        empty={<Text type='tertiary'>{t('该时间段无消耗记录')}</Text>}
      />
    </div>
  );
};

export default GroupQuotaTable;
