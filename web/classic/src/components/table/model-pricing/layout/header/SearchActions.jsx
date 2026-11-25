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

import React, { memo, useCallback } from 'react';
import { Input, Button, Switch, Divider } from '@douyinfe/semi-ui';
import { IconSearch, IconCopy, IconFilter } from '@douyinfe/semi-icons';

const SearchActions = memo(
  ({
    selectedRowKeys = [],
    copyText,
    handleChange,
    handleCompositionStart,
    handleCompositionEnd,
    isMobile = false,
    searchValue = '',
    setShowFilterModal,

    filterPointsOnly,
    setFilterPointsOnly,
    pointsEnabled = false,
    filterEntitlementOnly = false,
    setFilterEntitlementOnly,
    // 有活跃套餐覆盖才出「仅看套餐可用」开关
    entitlementConfig,

    viewMode,
    setViewMode,
    t,
  }) => {
    const handleCopyClick = useCallback(() => {
      if (copyText && selectedRowKeys.length > 0) {
        copyText(selectedRowKeys);
      }
    }, [copyText, selectedRowKeys]);

    const handleFilterClick = useCallback(() => {
      setShowFilterModal?.(true);
    }, [setShowFilterModal]);

    const handleViewModeToggle = useCallback(() => {
      setViewMode?.(viewMode === 'table' ? 'card' : 'table');
    }, [viewMode, setViewMode]);

    return (
      <div className='flex items-center gap-2 w-full'>
        <div className='flex-1'>
          <Input
            prefix={<IconSearch />}
            placeholder={t('模糊搜索模型名称')}
            value={searchValue}
            onCompositionStart={handleCompositionStart}
            onCompositionEnd={handleCompositionEnd}
            onChange={handleChange}
            showClear
          />
        </div>

        <Button
          theme='outline'
          type='primary'
          icon={<IconCopy />}
          onClick={handleCopyClick}
          disabled={selectedRowKeys.length === 0}
          className='!bg-blue-500 hover:!bg-blue-600 !text-white disabled:!bg-gray-300 disabled:!text-gray-500'
        >
          {t('复制')}
        </Button>

        {!isMobile && (
          <>
            <Divider layout='vertical' margin='8px' />

            {/* 仅看可积分抵扣。积分没开的站点不出这一项——开了也筛不出任何模型。 */}
            {pointsEnabled && (
              <div className='flex items-center gap-2'>
                <span className='text-sm text-gray-600'>
                  {t('仅看可积分抵扣')}
                </span>
                <Switch
                  checked={filterPointsOnly}
                  onChange={setFilterPointsOnly}
                />
              </div>
            )}

            {/*
              仅看套餐可用。没有活跃套餐的用户不出这一项——理由同积分那条：
              开了也筛不出任何模型，白占一行还让人以为自己该有套餐。
            */}
            {!!entitlementConfig?.coverage && (
              <div className='flex items-center gap-2'>
                <span className='text-sm text-gray-600'>
                  {t('仅看套餐可用')}
                </span>
                <Switch
                  checked={filterEntitlementOnly}
                  onChange={setFilterEntitlementOnly}
                />
              </div>
            )}

            {/* 视图模式切换按钮 */}
            <Button
              theme={viewMode === 'table' ? 'solid' : 'outline'}
              type={viewMode === 'table' ? 'primary' : 'tertiary'}
              onClick={handleViewModeToggle}
            >
              {t('表格视图')}
            </Button>
          </>
        )}

        {isMobile && (
          <Button
            theme='outline'
            type='tertiary'
            icon={<IconFilter />}
            onClick={handleFilterClick}
          >
            {t('筛选')}
          </Button>
        )}
      </div>
    );
  },
);

SearchActions.displayName = 'SearchActions';

export default SearchActions;
