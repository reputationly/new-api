import React, { useMemo } from 'react';
import { Empty } from '@douyinfe/semi-ui';
import {
  IllustrationNoResult,
  IllustrationNoResultDark,
} from '@douyinfe/semi-illustrations';
import CardTable from '../../common/ui/CardTable';
import { getModerationLogsColumns } from './ModerationLogsColumnDefs';

const ModerationLogsTable = ({
  logs,
  loading,
  activePage,
  pageSize,
  logCount,
  handlePageChange,
  handlePageSizeChange,
  openContentModal,
  compactMode,
  t,
}) => {
  const columns = useMemo(
    () => getModerationLogsColumns({ t, openContentModal }),
    [t, openContentModal],
  );

  return (
    <CardTable
      columns={columns}
      dataSource={logs}
      rowKey='key'
      loading={loading}
      scroll={compactMode ? undefined : { x: 'max-content' }}
      className='rounded-xl overflow-hidden'
      size='middle'
      empty={
        <Empty
          image={<IllustrationNoResult style={{ width: 150, height: 150 }} />}
          darkModeImage={
            <IllustrationNoResultDark style={{ width: 150, height: 150 }} />
          }
          description={t('该时间范围内没有审核记录')}
          style={{ padding: 30 }}
        />
      }
      pagination={{
        currentPage: activePage,
        pageSize: pageSize,
        total: logCount,
        pageSizeOptions: [10, 20, 50, 100],
        showSizeChanger: true,
        onPageSizeChange: handlePageSizeChange,
        onPageChange: handlePageChange,
      }}
      hidePagination={true}
    />
  );
};

export default ModerationLogsTable;
