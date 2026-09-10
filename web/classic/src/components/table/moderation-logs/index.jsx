import React from 'react';
import { Typography } from '@douyinfe/semi-ui';
import { ShieldAlert } from 'lucide-react';
import CardPro from '../../common/ui/CardPro';
import CompactModeToggle from '../../common/ui/CompactModeToggle';
import ModerationLogsTable from './ModerationLogsTable';
import ModerationLogsFilters from './ModerationLogsFilters';
import ModerationContentModal from './modals/ModerationContentModal';
import ModerationMediaModal from './modals/ModerationMediaModal';
import { useModerationLogsData } from '../../../hooks/moderation-logs/useModerationLogsData';
import { useIsMobile } from '../../../hooks/common/useIsMobile';
import { createCardProPagination } from '../../../helpers/utils';

const ModerationLogsPage = () => {
  const data = useModerationLogsData();
  const isMobile = useIsMobile();

  return (
    <>
      <ModerationContentModal
        visible={data.contentModalOpen}
        loading={data.contentLoading}
        content={data.contentText}
        onClose={data.closeContentModal}
        t={data.t}
      />
      <ModerationMediaModal
        visible={data.mediaModalOpen}
        loading={data.mediaLoading}
        url={data.mediaUrl}
        isVideo={data.mediaIsVideo}
        onClose={data.closeMediaModal}
        t={data.t}
      />

      <CardPro
        type='type2'
        statsArea={
          <div className='flex flex-col md:flex-row justify-between items-start md:items-center gap-2 w-full'>
            <div className='flex items-center text-blue-500'>
              <ShieldAlert size={16} className='mr-2' />
              <Typography.Text>{data.t('拦截记录')}</Typography.Text>
            </div>
            <CompactModeToggle
              compactMode={data.compactMode}
              setCompactMode={data.setCompactMode}
              t={data.t}
            />
          </div>
        }
        searchArea={<ModerationLogsFilters {...data} />}
        paginationArea={createCardProPagination({
          currentPage: data.activePage,
          pageSize: data.pageSize,
          total: data.logCount,
          onPageChange: data.handlePageChange,
          onPageSizeChange: data.handlePageSizeChange,
          isMobile: isMobile,
          t: data.t,
        })}
        t={data.t}
      >
        <ModerationLogsTable {...data} />
      </CardPro>
    </>
  );
};

export default ModerationLogsPage;
