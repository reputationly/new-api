import React from 'react';
import CardPro from '../../common/ui/CardPro';
import ModerationLogsTable from './ModerationLogsTable';
import ModerationLogsFilters from './ModerationLogsFilters';
import ModerationContentModal from './modals/ModerationContentModal';
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

      <CardPro
        type='type2'
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
