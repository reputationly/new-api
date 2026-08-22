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
import SelectableButtonGroup from '../../../common/ui/SelectableButtonGroup';

/**
 * 分组筛选组件
 * @param {string} filterGroup 当前选中的分组，'all' 表示不过滤
 * @param {Function} setFilterGroup 设置选中分组
 * @param {Record<string, any>} usableGroup 后端返回的可用分组对象
 * @param {Record<string, number>} groupRatio 分组基础倍率对象
 * @param {Record<string, Record<string, number>>} groupModelRatio 分组内按模型的倍率
 * @param {Array} models 模型列表
 * @param {boolean} loading 是否加载中
 * @param {Function} t i18n
 */
const PricingGroups = ({
  filterGroup,
  setFilterGroup,
  usableGroup = {},
  groupRatio = {},
  groupModelRatio = {},
  models = [],
  loading = false,
  t,
}) => {
  const groups = [
    'all',
    ...Object.keys(usableGroup).filter((key) => key !== ''),
  ];

  const items = groups.map((g) => {
    const modelCount =
      g === 'all'
        ? models.length
        : models.filter((m) => m.enable_groups && m.enable_groups.includes(g))
            .length;
    let ratioDisplay = '';
    if (g === 'all') {
      // ratioDisplay = t('全部');
    } else {
      const ratio = groupRatio[g];
      ratioDisplay = ratio !== undefined && ratio !== null ? `${ratio}x` : '1x';
      // 该分组配了模型折扣时，倍率不再是整组一个数，标注它只是基准值，
      // 否则用户会拿这个标签当准数去反算每个模型的价格。
      //
      // 用「基准」而不是「起」：折扣（multiply < 1）会让实际倍率**低于**基准，
      // 「起」是下界断言，在最常见的打折场景下方向正好是反的。准确的逐模型
      // 倍率在模型详情的分组价格表里。
      if (Object.keys(groupModelRatio[g] || {}).length > 0) {
        ratioDisplay = `${ratioDisplay}${t('基准')}`;
      }
    }
    return {
      value: g,
      label: g === 'all' ? t('全部分组') : g,
      tagCount: ratioDisplay,
    };
  });

  return (
    <SelectableButtonGroup
      title={t('可用令牌分组')}
      items={items}
      activeValue={filterGroup}
      onChange={setFilterGroup}
      loading={loading}
      variant='teal'
      t={t}
    />
  );
};

export default PricingGroups;
