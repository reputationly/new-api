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
import { Tag, Space, Tooltip } from '@douyinfe/semi-ui';
import {
  renderModelTag,
  stringToColor,
  calculateModelPrice,
  getModelPriceItems,
  formatVideoMatrixSummary,
  getLobeHubIcon,
  getGroupDiscountInfo,
  getNormalRatio,
  getTimePriceDelta,
} from '../../../../../helpers';
import {
  renderLimitedItems,
  renderDescription,
} from '../../../../common/ui/RenderUtils';
import { useIsMobile } from '../../../../../hooks/common/useIsMobile';
import TimeRulesTooltip from '../../TimeRulesTooltip';
import {
  getModelCoverage,
  buildEntitlementBadges,
  buildCoverageTooltip,
} from '../../../../../helpers/entitlementPricing';

// record 可选：视频计费矩阵不是 quota_type 的取值，得看 record 本身。
function renderQuotaType(type, t, record) {
  if (record?.video_pricing?.mode) {
    return (
      <Tag color='orange' shape='circle'>
        {t('场景计费')}
      </Tag>
    );
  }
  switch (type) {
    case 1:
      return (
        <Tag color='teal' shape='circle'>
          {t('按次计费')}
        </Tag>
      );
    case 0:
      return (
        <Tag color='violet' shape='circle'>
          {t('按量计费')}
        </Tag>
      );
    default:
      return t('未知');
  }
}

// Render vendor name
const renderVendor = (vendorName, vendorIcon, t) => {
  if (!vendorName) return '-';
  return (
    <Tag
      color='white'
      shape='circle'
      prefixIcon={getLobeHubIcon(vendorIcon || 'Layers', 14)}
    >
      {vendorName}
    </Tag>
  );
};

// Render tags list using RenderUtils
// capabilityTags：能力标签（区别配色，翻译展示）；t：i18n（缺省不翻译）
const renderTags = (text, capabilityTags = [], t = (x) => x) => {
  const capItems = (Array.isArray(capabilityTags) ? capabilityTags : [])
    .map((c) => String(c).trim())
    .filter(Boolean);
  // Tags 现含能力词，剔除已作为能力标签展示的词，避免同词重复。
  const capSet = new Set(capItems.map((c) => c.toLowerCase()));
  const tagsArr = text
    ? text
        .split(/[,;|]+/)
        .map((tag) => tag.trim())
        .filter((tag) => tag && !capSet.has(tag.toLowerCase()))
    : [];
  if (capItems.length === 0 && tagsArr.length === 0) return '-';
  return (
    <div className='flex items-center gap-1 flex-wrap'>
      {capItems.map((cap, idx) => (
        <Tag key={`cap-${idx}`} color='light-blue' shape='circle' size='small'>
          {t(cap)}
        </Tag>
      ))}
      {tagsArr.length > 0 &&
        renderLimitedItems({
          items: tagsArr,
          renderItem: (tag, idx) => (
            <Tag
              key={idx}
              color={stringToColor(tag.trim())}
              shape='circle'
              size='small'
            >
              {tag.trim()}
            </Tag>
          ),
          maxDisplay: 3,
        })}
    </div>
  );
};

function renderSupportedEndpoints(endpoints) {
  if (!endpoints || endpoints.length === 0) {
    return null;
  }
  return (
    <Space wrap>
      {endpoints.map((endpoint, idx) => (
        <Tag key={endpoint} color={stringToColor(endpoint)} shape='circle'>
          {endpoint}
        </Tag>
      ))}
    </Space>
  );
}

export const getPricingTableColumns = ({
  t,
  selectedGroup,
  groupRatio,
  groupModelRatio,
  groupTimeRatio,
  groupModelCompletionRatio,
  copyText,
  currency,
  siteDisplayType,
  displayPrice,
  pointsConfig,
  entitlementConfig,
}) => {
  const isMobile = useIsMobile();
  const priceDataCache = new WeakMap();

  const getPriceData = (record) => {
    let cache = priceDataCache.get(record);
    if (!cache) {
      cache = calculateModelPrice({
        record,
        selectedGroup,
        groupRatio,
        groupModelRatio,
        displayPrice,
        currency,
        quotaDisplayType: siteDisplayType,
        // 此刻生效的补全倍率（分时段价目可不等比例变化），算输出价必须用
        completionRatioMap: groupModelCompletionRatio,
        pointsEnabled: pointsConfig?.enabled,
        quotaPerPoint: pointsConfig?.quotaPerPoint,
        pointsEnabledGroups: pointsConfig?.enabledGroups,
        pointsEnabledModels: pointsConfig?.enabledModels,
        // 套餐权益：只传该模型自己的覆盖，未命中时为 null，价格展示与加这个功能之前一致
        entitlementCoverage: getModelCoverage(
          entitlementConfig?.coverage,
          record.model_name,
        ),
        quotaPerComputePoint: entitlementConfig?.quotaPerComputePoint,
      });
      priceDataCache.set(record, cache);
    }
    return cache;
  };

  // 常规价（不含时段系数）的价格数据：优惠时段给价格列画删除线用。
  //
  // 复用 calculateModelPrice 而不是自己按倍率折算字符串——它已经处理了按次/按量、
  // 币种、积分价、套餐价、向上取整等一堆口径，抄一遍必然与之分叉。
  // 把 normal_ratio 当成该分组的倍率、模型折扣清空，得到的正好是「没有时段价时的价」。
  const normalPriceCache = new Map();
  const getNormalPriceData = (record, group, normalRatio) => {
    const cacheKey = group + '|' + normalRatio;
    if (normalPriceCache.has(cacheKey)) return normalPriceCache.get(cacheKey);
    const data = calculateModelPrice({
      record,
      selectedGroup: group,
      groupRatio: { [group]: normalRatio },
      groupModelRatio: {},
      displayPrice,
      currency,
      quotaDisplayType: siteDisplayType,
      // 积分/套餐口径必须与主计算保持一致：否则「积分计价」的模型上，
      // 删除线画的是金额、当前价显示的是积分，两个单位对不上。
      pointsEnabled: pointsConfig?.enabled,
      quotaPerPoint: pointsConfig?.quotaPerPoint,
      pointsEnabledGroups: pointsConfig?.enabledGroups,
      pointsEnabledModels: pointsConfig?.enabledModels,
      entitlementCoverage: getModelCoverage(
        entitlementConfig?.coverage,
        record.model_name,
      ),
      quotaPerComputePoint: entitlementConfig?.quotaPerComputePoint,
    });
    normalPriceCache.set(cacheKey, data);
    return data;
  };

  const endpointColumn = {
    title: t('可用端点类型'),
    dataIndex: 'supported_endpoint_types',
    render: (text, record, index) => {
      return renderSupportedEndpoints(text);
    },
  };

  // 套餐权益角标。未登录 / 无套餐 / 该模型未被覆盖时返回 null——
  // 这三种情况的展示与加这个功能之前完全一致（设计文档 §8.2）。
  const renderEntitlementBadges = (record) => {
    const coverage = getModelCoverage(
      entitlementConfig?.coverage,
      record.model_name,
    );
    if (!coverage) return null;
    const badges = buildEntitlementBadges(coverage);
    if (badges.length === 0) return null;
    // 多套餐覆盖时才给说明：单套餐没什么要解释的，多一层 tooltip 只是噪音
    const tip = buildCoverageTooltip(coverage);
    const nodes = badges.map((b) => (
      <Tag key={b.key} color={b.color} shape='circle' size='small'>
        {t(b.text)}
      </Tag>
    ));
    if (!tip) return nodes;
    return (
      <Tooltip
        style={{ maxWidth: 'none' }}
        content={<div style={{ whiteSpace: 'pre-line' }}>{tip}</div>}
      >
        <span className='flex items-center gap-1'>{nodes}</span>
      </Tooltip>
    );
  };

  const modelNameColumn = {
    title: t('模型名称'),
    dataIndex: 'model_name',
    render: (text, record, index) => {
      // 折扣标签跟在模型名后面。这里没有 truncate，长名字会自然换行，
      // 不会像卡片视图那样把标签挤掉。
      // 角标用**常规价**（不含时段系数）：折扣率必须与时钟无关，这是本次改造对外的承诺。
      const priceData = getPriceData(record);
      const d = getGroupDiscountInfo(
        getNormalRatio(
          groupTimeRatio,
          priceData?.usedGroup,
          record.model_name,
          priceData?.usedGroupRatio,
        ),
      );
      return (
        <div className='flex items-center gap-1 flex-wrap'>
          {renderModelTag(text, {
            onClick: () => {
              copyText(text);
            },
          })}
          {/*
            已知限制（有意接受）：分时规则挂在折扣角标的 tooltip 上，而角标只在此刻
            有折扣时出现——「平时原价、只在夜里打折」的模型白天没有分时标识。
            不为此新增角标，详见 PricingCardView 同处注释；归宿是详情弹窗的分时定价表。
          */}
          {d && (
            <Tooltip
              // 解除 Semi 默认的 240px 上限，理由同卡片视图
              style={{ maxWidth: 'none' }}
              content={
                <div style={{ whiteSpace: 'nowrap' }}>
                  <div>
                    {t('{{group}} 分组，已按 {{text}} 计价', {
                      group: getPriceData(record).usedGroup,
                      text: d.text,
                    })}
                  </div>
                  {/* 分时规则挂在已有角标上，不另起角标——理由同卡片视图 */}
                  <TimeRulesTooltip
                    group={getPriceData(record).usedGroup}
                    modelName={record.model_name}
                    groupTimeRatio={groupTimeRatio}
                    t={t}
                  />
                </div>
              }
            >
              <Tag color={d.color} shape='circle' size='small'>
                {d.text}
              </Tag>
            </Tooltip>
          )}
          {(() => {
            // 时段价标签：与折扣角标并列但不合并——角标是合同折扣（永随时段不变），
            // 这个是「此刻价目 ≠ 常规价」。合并回一个数就退回「折扣随时钟漂移」的老问题。
            const delta = getTimePriceDelta(
              groupTimeRatio,
              priceData?.usedGroup,
              record.model_name,
              priceData?.usedGroupRatio,
            );
            if (!delta) return null;
            return (
              <Tooltip
                style={{ maxWidth: 'none' }}
                content={
                  <div style={{ whiteSpace: 'nowrap' }}>
                    {t('常规价')} {Number(delta.normal.toFixed(4))}x{' · '}
                    {t('此刻价')} {Number(delta.current.toFixed(4))}x
                  </div>
                }
              >
                <Tag
                  color={delta.cheaper ? 'cyan' : 'orange'}
                  shape='circle'
                  size='small'
                >
                  {delta.label || (delta.cheaper ? t('闲时') : t('高峰'))}
                </Tag>
              </Tooltip>
            );
          })()}
          {renderEntitlementBadges(record)}
        </div>
      );
    },
    onFilter: (value, record) =>
      record.model_name.toLowerCase().includes(value.toLowerCase()),
  };

  const quotaColumn = {
    title: t('计费类型'),
    dataIndex: 'quota_type',
    render: (text, record, index) => {
      return renderQuotaType(parseInt(text), t, record);
    },
    sorter: (a, b) => a.quota_type - b.quota_type,
  };

  const descriptionColumn = {
    title: t('描述'),
    dataIndex: 'description',
    render: (text) => renderDescription(text, 200),
  };

  const tagsColumn = {
    title: t('标签'),
    dataIndex: 'tags',
    render: (text, record) => renderTags(text, record.capability_tags, t),
  };

  const vendorColumn = {
    title: t('供应商'),
    dataIndex: 'vendor_name',
    render: (text, record) => renderVendor(text, record.vendor_icon, t),
  };

  const baseColumns = [
    modelNameColumn,
    vendorColumn,
    descriptionColumn,
    tagsColumn,
    quotaColumn,
  ];

  const priceColumn = {
    title: siteDisplayType === 'TOKENS' ? t('计费摘要') : t('模型价格'),
    dataIndex: 'model_price',
    ...(isMobile ? {} : { fixed: 'right' }),
    render: (text, record, index) => {
      const priceData = getPriceData(record);
      // 矩阵有多格，列表里放不下，给价格区间；逐格价目在详情弹窗里。
      if (priceData.isVideoMatrix) {
        return (
          <div className='text-gray-700'>
            {formatVideoMatrixSummary(priceData, t)}
          </div>
        );
      }
      const priceItems = getModelPriceItems(priceData, t, siteDisplayType);

      // 此刻比常规价便宜时，价格列画的是**常规价**的删除线；没配分时的模型保持
      // 原来的「折前价」删除线（那是折扣的故事，两者不同时出现，避免一行两个删除线）。
      const delta = getTimePriceDelta(
        groupTimeRatio,
        priceData?.usedGroup,
        record.model_name,
        priceData?.usedGroupRatio,
      );
      const normalMap =
        delta && delta.cheaper
          ? Object.fromEntries(
              getModelPriceItems(
                getNormalPriceData(record, priceData?.usedGroup, delta.normal),
                t,
                siteDisplayType,
              ).map((it) => [it.key, it.value]),
            )
          : null;

      return (
        <div className='space-y-1'>
          {priceItems.map((item) => {
            const strike =
              (normalMap && normalMap[item.key]) || item.originalValue;
            return (
              <div key={item.key} className='text-gray-700'>
                {item.label}{' '}
                {strike && (
                  <span className='mr-1 text-gray-400 line-through'>
                    {strike}
                  </span>
                )}
                {item.value}
                {item.suffix}
              </div>
            );
          })}
        </div>
      );
    },
  };

  const columns = [...baseColumns];
  columns.push(endpointColumn);
  columns.push(priceColumn);
  return columns;
};
