import React from 'react';
import { Avatar, Table, Typography } from '@douyinfe/semi-ui';
import { IconPriceTag } from '@douyinfe/semi-icons';
import {
  flattenVideoMatrix,
  videoSecondsRank,
  getModelPricingCurrencyConfig,
} from '../../../../../helpers';

const { Text } = Typography;

/**
 * 视频计费矩阵的价目表（docs/video-billing-matrix-design.md §2.6 接入点 3）。
 *
 * 这类模型的实收由「分辨率 × 输入是否含视频」查表决定，与 model_ratio 无关——
 * 后者只是提交时的预扣锚点。不单独展示矩阵的话，定价页会把锚点当成单价给用户看
 * （480p 实际 ¥46 却显示 ¥51），还会多出一个对视频模型毫无意义的「补全价格」。
 *
 * 版式是**交叉表**：纵轴分辨率、横轴场景、交汇处是价格。与配置页的矩阵编辑器同构，
 * 运营配了什么、用户看到什么，一眼能对上。摊平成一行一价要读 6 行才拼得出全貌。
 */
export default function VideoMatrixBreakdown({ videoPricing, t }) {
  const cells = flattenVideoMatrix(videoPricing);
  if (!cells.length) return null;

  const isToken = videoPricing.mode === 'token';
  const { symbol, rate } = getModelPricingCurrencyConfig();

  // 列：token 模式固定两列（与配置页、供应商价目表同序）；按次模式取出现过的秒数。
  // 秒数用 videoSecondsRank 解析——后端接受 '5s' / '5秒' 这类列名，
  // 直接 Number() 会得到 NaN、比较器恒返回 NaN，排序退化成原序。
  const columnKeys = isToken
    ? ['without_video', 'with_video']
    : [...new Set(cells.map((c) => c.column))].sort(
        (a, b) => videoSecondsRank(a) - videoSecondsRank(b),
      );

  const columnLabel = (key) => {
    if (!isToken) return `${key} ${t('秒')}`;
    return key === 'without_video' ? t('输入不含视频') : t('输入包含视频');
  };

  // 行：按分辨率聚合，交汇格取价格。未配置的格子留空，显示为「—」——
  // 那是「该档不支持/未定价」的真实状态，填 0 会被误读成免费。
  const byResolution = new Map();
  cells.forEach(({ resolution, column, priceUSD }) => {
    if (!byResolution.has(resolution)) byResolution.set(resolution, {});
    byResolution.get(resolution)[column] = priceUSD;
  });

  const dataSource = [...byResolution.entries()].map(([resolution, row]) => ({
    key: resolution,
    resolution,
    ...Object.fromEntries(columnKeys.map((k) => [k, row[k]])),
  }));

  const columns = [
    {
      title: t('分辨率'),
      dataIndex: 'resolution',
      width: 100,
      render: (v) => <Text strong>{v}</Text>,
    },
    ...columnKeys.map((key) => ({
      title: columnLabel(key),
      dataIndex: key,
      align: 'right',
      render: (priceUSD) =>
        priceUSD === undefined ? (
          <Text type='tertiary'>—</Text>
        ) : (
          <Text strong>
            {symbol}
            {Number((priceUSD * rate).toFixed(4))}
          </Text>
        ),
    })),
  ];

  return (
    <div>
      <div className='flex items-center mb-4'>
        <Avatar size='small' color='amber' className='mr-2 shadow-md'>
          <IconPriceTag size={16} />
        </Avatar>
        <div>
          <Text className='text-lg font-medium'>{t('场景计费')}</Text>
          <div className='text-xs text-gray-600'>
            {isToken
              ? t(
                  '按上游返回的 token 数计费，单价随分辨率与是否含视频输入变化。时长已隐含在 token 数里。',
                )
              : t('按次计费，单价随分辨率与时长变化。')}
          </div>
        </div>
      </div>

      <div className='text-xs text-gray-500 mb-2'>
        {isToken
          ? `${t('单位')}：${symbol} / 1M tokens`
          : `${t('单位')}：${symbol} / ${t('次')}`}
      </div>

      <Table
        dataSource={dataSource}
        columns={columns}
        pagination={false}
        size='small'
        bordered={false}
        className='!rounded-lg'
      />

      <div className='text-xs text-gray-600 mt-2'>
        {t('上表为基础单价，实际按下方各分组的倍率折算。')}
      </div>
    </div>
  );
}
