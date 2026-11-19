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
import { Banner, Tooltip } from '@douyinfe/semi-ui';
import { IconAlertTriangle } from '@douyinfe/semi-icons';
import CardPro from '../../common/ui/CardPro';
import ChannelsTable from './ChannelsTable';
import ChannelsActions from './ChannelsActions';
import ChannelsFilters from './ChannelsFilters';
import ChannelsTabs from './ChannelsTabs';
import { useChannelsData } from '../../../hooks/channels/useChannelsData';
import { useChannelVisibilitySummary } from '../../../hooks/channels/useChannelVisibilitySummary';
import { useIsMobile } from '../../../hooks/common/useIsMobile';
import BatchTagModal from './modals/BatchTagModal';
import ModelTestModal from './modals/ModelTestModal';
import ColumnSelectorModal from './modals/ColumnSelectorModal';
import EditChannelModal from './modals/EditChannelModal';
import EditTagModal from './modals/EditTagModal';
import MultiKeyManageModal from './modals/MultiKeyManageModal';
import ChannelCostModal from './modals/ChannelCostModal';
import ChannelUpstreamUpdateModal from './modals/ChannelUpstreamUpdateModal';
import { createCardProPagination } from '../../../helpers/utils';

const ChannelsPage = () => {
  const channelsData = useChannelsData();
  const isMobile = useIsMobile();
  // 「哪些模型因渠道停用而不可见」的汇总：渠道页上"禁用"只是一个红点，
  // 看不出它带走了多少模型（真实盘点：二十多条渠道长期禁用，几十个模型悄悄不可用）。
  const visibility = useChannelVisibilitySummary({
    channelCount: channelsData.channelCount,
  });

  return (
    <>
      {/* Modals */}
      <ColumnSelectorModal {...channelsData} />
      <EditTagModal
        visible={channelsData.showEditTag}
        tag={channelsData.editingTag}
        handleClose={() => channelsData.setShowEditTag(false)}
        refresh={channelsData.refresh}
      />
      <EditChannelModal
        refresh={channelsData.refresh}
        visible={channelsData.showEdit}
        handleClose={channelsData.closeEdit}
        editingChannel={channelsData.editingChannel}
      />
      <BatchTagModal {...channelsData} />
      <ModelTestModal {...channelsData} />
      <ChannelCostModal
        visible={channelsData.showCostModal}
        channel={channelsData.costChannel}
        onClose={(saved) => {
          channelsData.setShowCostModal(false);
          if (saved) channelsData.refresh?.();
        }}
      />
      <MultiKeyManageModal
        visible={channelsData.showMultiKeyManageModal}
        onCancel={() => channelsData.setShowMultiKeyManageModal(false)}
        channel={channelsData.currentMultiKeyChannel}
        onRefresh={channelsData.refresh}
      />
      <ChannelUpstreamUpdateModal
        visible={channelsData.showUpstreamUpdateModal}
        addModels={channelsData.upstreamUpdateAddModels}
        removeModels={channelsData.upstreamUpdateRemoveModels}
        preferredTab={channelsData.upstreamUpdatePreferredTab}
        confirmLoading={channelsData.upstreamApplyLoading}
        onConfirm={channelsData.applyUpstreamUpdates}
        onCancel={channelsData.closeUpstreamUpdateModal}
      />

      {/* Main Content */}
      {visibility.summary && visibility.summary.total > 0 ? (
        <Banner
          type='warning'
          closeIcon={null}
          icon={
            <IconAlertTriangle
              size='large'
              style={{ color: 'var(--semi-color-warning)' }}
            />
          }
          description={
            <span>
              {channelsData.t(
                '当前有 {{count}} 个模型因渠道停用而对用户不可见：模型广场、体验区与令牌白名单都不会返回它们。',
                { count: visibility.summary.total },
              )}{' '}
              <Tooltip
                content={visibility.summary.names.slice(0, 40).join('、')}
              >
                <span className='cursor-pointer underline'>
                  {channelsData.t('查看清单')}
                </span>
              </Tooltip>{' '}
              <span
                role='button'
                tabIndex={0}
                className='cursor-pointer underline text-blue-600'
                onClick={visibility.reload}
              >
                {visibility.loading
                  ? channelsData.t('统计中…')
                  : channelsData.t('重新统计')}
              </span>
            </span>
          }
          style={{ marginBottom: 12 }}
        />
      ) : null}
      {channelsData.globalPassThroughEnabled ? (
        <Banner
          type='warning'
          closeIcon={null}
          icon={
            <IconAlertTriangle
              size='large'
              style={{ color: 'var(--semi-color-warning)' }}
            />
          }
          description={channelsData.t(
            '已开启全局请求透传：参数覆写、模型重定向、渠道适配等 NewAPI 内置功能将失效，非最佳实践；如因此产生问题，请勿提交 issue 反馈。',
          )}
          style={{ marginBottom: 12 }}
        />
      ) : null}
      <CardPro
        type='type3'
        tabsArea={<ChannelsTabs {...channelsData} />}
        actionsArea={<ChannelsActions {...channelsData} />}
        searchArea={<ChannelsFilters {...channelsData} />}
        paginationArea={createCardProPagination({
          currentPage: channelsData.activePage,
          pageSize: channelsData.pageSize,
          total: channelsData.channelCount,
          onPageChange: channelsData.handlePageChange,
          onPageSizeChange: channelsData.handlePageSizeChange,
          isMobile: isMobile,
          t: channelsData.t,
        })}
        t={channelsData.t}
      >
        <ChannelsTable
          {...channelsData}
          invisibleByChannel={visibility.byChannel}
        />
      </CardPro>
    </>
  );
};

export default ChannelsPage;
