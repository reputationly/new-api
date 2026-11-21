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

import React from 'react';
import { Avatar, Typography, Table, Tag, Tooltip } from '@douyinfe/semi-ui';
import { IconCoinMoneyStroked, IconClock } from '@douyinfe/semi-icons';
import {
  calculateModelPrice,
  getEffectiveGroupRatio,
  getGroupDiscountInfo,
  getModelPriceItems,
  formatVideoMatrixSummary,
  buildTimeWindowRows,
} from '../../../../../helpers';
import { DISCOUNT_HEX } from '../../../../../helpers/discount';

import { getModelCoverage } from '../../../../../helpers/entitlementPricing';
const { Text } = Typography;

const ModelPricingTable = ({
  modelData,
  groupRatio,
  groupModelRatio,
  groupTimeRatio,
  groupModelCompletionRatio,
  currency,
  siteDisplayType,
  displayPrice,
  usableGroup,
  autoGroups = [],
  pointsConfig,
  entitlementConfig,
  t,
}) => {
  const modelEnableGroups = Array.isArray(modelData?.enable_groups)
    ? modelData.enable_groups
    : [];
  const autoChain = autoGroups.filter((g) => modelEnableGroups.includes(g));
  const renderGroupPriceTable = () => {
    // 仅展示模型可用的分组：模型 enable_groups 与用户可用分组的交集

    const availableGroups = Object.keys(usableGroup || {})
      .filter((g) => g !== '')
      .filter((g) => g !== 'auto')
      .filter((g) => modelEnableGroups.includes(g));

    // 准备表格数据
    const tableData = availableGroups.map((group) => {
      const priceData = modelData
        ? calculateModelPrice({
            record: modelData,
            selectedGroup: group,
            groupRatio,
            groupModelRatio,
            displayPrice,
            currency,
            quotaDisplayType: siteDisplayType,
            completionRatioMap: groupModelCompletionRatio,
            pointsEnabled: pointsConfig?.enabled,
            quotaPerPoint: pointsConfig?.quotaPerPoint,
            pointsEnabledGroups: pointsConfig?.enabledGroups,
            pointsEnabledModels: pointsConfig?.enabledModels,
            // 套餐权益：只传该模型自己的覆盖，未命中时为 null，价格展示与加这个功能之前一致
            entitlementCoverage: getModelCoverage(
              entitlementConfig?.coverage,
              modelData.model_name,
            ),
            quotaPerComputePoint: entitlementConfig?.quotaPerComputePoint,
          })
        : { inputPrice: '-', outputPrice: '-', price: '-' };

      // 获取分组倍率。这张表逐分组列价，是模型折扣最该生效的地方——
      // 用基础倍率的话，「倍率」列会和同一行的价格自相矛盾。
      const groupRatioValue =
        getEffectiveGroupRatio(
          groupRatio,
          groupModelRatio,
          group,
          modelData?.model_name,
        ) ?? 1;

      return {
        key: group,
        group: group,
        ratio: groupRatioValue,
        billingType:
          modelData?.billing_mode === 'tiered_expr'
            ? t('动态计费')
            : modelData?.video_pricing?.mode
              ? t('场景计费')
              : modelData?.quota_type === 0
                ? t('按量计费')
                : modelData?.quota_type === 1
                  ? t('按次计费')
                  : '-',
        priceItems: getModelPriceItems(priceData, t, siteDisplayType),
        // 视频矩阵行要按本行分组倍率折算出区间，需要原始 priceData
        priceData,
      };
    });

    // 定义表格列
    const columns = [
      {
        title: t('令牌分组'),
        dataIndex: 'group',
        render: (text) => (
          <Tag color='white' size='small' shape='circle'>
            {text}
            {t('分组')}
          </Tag>
        ),
      },
    ];

    const isDynamic = modelData?.billing_mode === 'tiered_expr';

    // 动态计费时始终显示倍率列，否则根据设置
    // 动态计费（计费表达式）的模型仍要列分组倍率——那是它定价的组成部分，
    // 与已下线的「显示倍率」开关无关。
    if (isDynamic) {
      columns.push({
        title: t('分组倍率'),
        dataIndex: 'ratio',
        render: (text) => (
          <Tag color='blue' size='small' shape='circle'>
            {text}x
          </Tag>
        ),
      });
    }

    columns.push({
      title: t('计费类型'),
      dataIndex: 'billingType',
      render: (text) => {
        let color = 'white';
        if (text === t('按量计费')) color = 'violet';
        else if (text === t('按次计费')) color = 'teal';
        else if (text === t('动态计费')) color = 'amber';
        else if (text === t('场景计费')) color = 'orange';
        return (
          <Tag color={color} size='small' shape='circle'>
            {text || '-'}
          </Tag>
        );
      },
    });

    // 折扣：不同分组/令牌的实际折扣不同（线路倍率 × 模型折扣 × 用户档），
    // 摊在这一列里，用户能直接比较「我这个令牌几折」。
    columns.push({
      title: t('折扣'),
      dataIndex: 'ratio',
      render: (ratio, record) => {
        const d = getGroupDiscountInfo(record?.priceData?.usedGroupRatio);
        if (!d) {
          return (
            <Text type='tertiary' size='small'>
              {t('原价')}
            </Text>
          );
        }
        return (
          <Tag color={d.color} shape='circle' size='small'>
            {d.text}
          </Tag>
        );
      },
    });

    // 积分抵扣：支持就标一个，不列具体积分价目（消耗随套餐与用量变化）。
    columns.push({
      title: t('积分抵扣'),
      dataIndex: 'points',
      render: (_, record) =>
        record?.priceData?.points ? (
          <Tooltip content={t('可用积分抵扣，实际消耗以账单为准')}>
            <Tag color='light-blue' shape='circle' size='small'>
              {t('可用')}
            </Tag>
          </Tooltip>
        ) : (
          <Text type='tertiary' size='small'>
            -
          </Text>
        ),
    });

    columns.push({
      title: siteDisplayType === 'TOKENS' ? t('计费摘要') : t('价格摘要'),
      dataIndex: 'priceItems',
      render: (items, record) => {
        if (items.length === 1 && items[0].isDynamic) {
          return (
            <Text type='tertiary' size='small'>
              {t('见上方动态计费详情')}
            </Text>
          );
        }
        // 场景计费：给出本分组折算后的价格区间，逐档拆解在上方矩阵里。
        // 只写「见上方」的话，非 1 倍率的分组用户还得自己乘一遍。
        if (items.length === 1 && items[0].isVideoMatrix) {
          return (
            <div className='space-y-1'>
              <div className='font-semibold text-orange-600'>
                {formatVideoMatrixSummary(record.priceData, t)}
              </div>
              <div className='text-xs text-gray-500'>
                {t('见上方场景计费价目表')}
              </div>
            </div>
          );
        }
        return (
          <div className='space-y-1'>
            {items.map((item) => (
              <div key={item.key}>
                <div className='font-semibold text-orange-600'>
                  {item.label}{' '}
                  {item.originalValue && (
                    <span className='mr-1 font-normal text-gray-400 line-through'>
                      {item.originalValue}
                    </span>
                  )}
                  {item.value}
                </div>
                <div className='text-xs text-gray-500'>{item.suffix}</div>
              </div>
            ))}
          </div>
        );
      },
    });

    return (
      <Table
        dataSource={tableData}
        columns={columns}
        pagination={false}
        size='small'
        bordered={false}
        className='!rounded-lg'
      />
    );
  };

  // 分时价目表。只在该模型确有时段价时渲染——没配的模型多一张空表只是噪音。
  //
  // 逐分组列出是必要的：时段规则按使用分组配，同一个模型在 default 有夜间折扣、
  // 在 premium 没有，是完全正常的配置。只列当前最优分组会让另一个分组的用户
  // 看到一个对自己不成立的优惠。
  const renderTimeWindowTable = () => {
    const rows = buildTimeWindowRows({
      usableGroup,
      modelEnableGroups,
      groupTimeRatio,
      modelName: modelData?.model_name,
      normalLabel: t('其余时段'),
    });

    if (rows.length === 0) return null;

    return (
      <div className='mt-6'>
        <div className='flex items-center mb-4'>
          <Avatar size='small' color='cyan' className='mr-2 shadow-md'>
            <IconClock size={16} />
          </Avatar>
          <div>
            <Text className='text-lg font-medium'>{t('分时定价')}</Text>
            <div className='text-xs text-gray-600'>
              {t(
                '时段价乘在常规价上：小于 1 是空闲时段更便宜，大于 1 是紧缺时段更贵',
              )}
            </div>
          </div>
        </div>
        <Table
          dataSource={rows}
          pagination={false}
          size='small'
          bordered={false}
          className='!rounded-lg'
          columns={[
            {
              title: t('令牌分组'),
              dataIndex: 'group',
              render: (text) => (
                <Tag color='white' size='small' shape='circle'>
                  {text}
                  {t('分组')}
                </Tag>
              ),
            },
            {
              title: t('生效时段'),
              dataIndex: 'range',
              render: (text, record) => (
                <span className='flex items-center gap-1 flex-wrap'>
                  {text}
                  {record.active && (
                    <Tag
                      size='small'
                      shape='circle'
                      style={{
                        backgroundColor: DISCOUNT_HEX.cyan.bg,
                        color: DISCOUNT_HEX.cyan.fg,
                      }}
                    >
                      {t('进行中')}
                    </Tag>
                  )}
                </span>
              ),
            },
            {
              title: t('折后 / 目录价'),
              dataIndex: 'discount',
              render: (d, record) => {
                // 详情页才需要摊开计费规则：这里同时给「折后价」与「目录价」，
                // 广场卡片只显示折后价（当前时段的成交价）。
                const mRatio = Number(modelData?.model_ratio) || 0;
                const cRatio = Number(modelData?.completion_ratio) || 1;
                const inDir = Number(record.inDir) || 0;
                const outDir = Number(record.outDir) || 0;
                const disc = Number(record.fixedDiscount) || 0;
                const inFinal = Number(record.ratio) || 0;
                const cEff =
                  inDir > 0 && outDir > 0 ? cRatio * (outDir / inDir) : cRatio;
                const outFinal = Number((cEff * inFinal).toFixed(6));
                const inList = Number(
                  (disc > 0 ? inFinal / disc : inDir * 1).toFixed(6),
                );
                const outList = Number((cEff * inList).toFixed(6));
                const fmt = (v) => `¥${Number(v).toFixed(4)}`;
                const listPair =
                  disc > 0 && disc !== 1 ? (
                    <span className='text-gray-400 line-through'>
                      {fmt(mRatio * inList)} / {fmt(mRatio * outList)}
                    </span>
                  ) : null;
                // 两件事并排但不混：d.text 是**绝对折扣**（相对目录价），
                // relative 是「相对常规价」。只显示折数看不出这个时段贵还是便宜。
                const rel = Number(record.relative);
                const relTag =
                  Number.isFinite(rel) && rel !== 1 ? (
                    <Tag
                      color={rel > 1 ? 'orange' : 'cyan'}
                      size='small'
                      shape='circle'
                    >
                      {'×' + rel}
                    </Tag>
                  ) : null;
                return (
                  <span className='flex flex-col gap-0.5'>
                    <span className='flex items-center gap-1 flex-wrap'>
                      <strong>
                        {fmt(mRatio * inFinal)} / {fmt(mRatio * outFinal)}
                      </strong>
                      {listPair}
                    </span>
                    <span className='flex items-center gap-1 flex-wrap'>
                      {relTag}
                      <Tag color='white' size='small' shape='circle'>
                        {d ? d.text : `${Number(record.ratio.toFixed(4))}x`}
                      </Tag>
                    </span>
                  </span>
                );
              },
            },
          ]}
        />
      </div>
    );
  };

  return (
    <div>
      <div className='flex items-center mb-4'>
        <Avatar size='small' color='orange' className='mr-2 shadow-md'>
          <IconCoinMoneyStroked size={16} />
        </Avatar>
        <div>
          <Text className='text-lg font-medium'>{t('分组价格')}</Text>
          <div className='text-xs text-gray-600'>
            {t('不同用户分组的价格信息')}
          </div>
        </div>
      </div>
      {autoChain.length > 0 && (
        <div className='flex flex-wrap items-center gap-1 mb-4'>
          <span className='text-sm text-gray-600'>{t('auto分组调用链路')}</span>
          <span className='text-sm'>→</span>
          {autoChain.map((g, idx) => (
            <React.Fragment key={g}>
              <Tag color='white' size='small' shape='circle'>
                {g}
                {t('分组')}
              </Tag>
              {idx < autoChain.length - 1 && <span className='text-sm'>→</span>}
            </React.Fragment>
          ))}
        </div>
      )}
      {renderGroupPriceTable()}
      {renderTimeWindowTable()}
    </div>
  );
};

export default ModelPricingTable;
