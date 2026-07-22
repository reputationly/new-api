import React, { useState, useEffect } from 'react';
import { Table, Tag, Typography, Empty } from '@douyinfe/semi-ui';
import {
  IllustrationNoResult,
  IllustrationNoResultDark,
} from '@douyinfe/semi-illustrations';
import { API, showError, timestamp2string, renderQuota } from '../../helpers';
import { quotaToPoints, isPointsEnabled } from '../../helpers/quota';

const { Text } = Typography;

// 「我邀请的用户」列表：分页展示被当前用户邀请注册的下线，按注册时间由近到远。
// 数据来自 GET /api/user/aff/invitees（{ items, total, verified_total }）。
const InvitedUsersTable = ({ t, onStats }) => {
  const [loading, setLoading] = useState(false);
  const [items, setItems] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);
  const pointsOn = isPointsEnabled();

  const load = async (currentPage, currentPageSize) => {
    setLoading(true);
    try {
      const res = await API.get(
        `/api/user/aff/invitees?p=${currentPage}&page_size=${currentPageSize}`,
      );
      const { success, message, data } = res.data;
      if (success) {
        setItems(data.items || []);
        setTotal(data.total || 0);
        onStats?.({
          total: data.total || 0,
          verifiedTotal: data.verified_total || 0,
        });
      } else {
        showError(message);
      }
    } catch (e) {
      showError(t('加载邀请用户列表失败'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    load(page, pageSize);
  }, [page, pageSize]);

  const renderTime = (v) => (v > 0 ? timestamp2string(v) : '—');

  const columns = [
    {
      title: t('用户名'),
      dataIndex: 'username',
      render: (v) => <Text>{v}</Text>,
    },
    {
      title: t('是否实名'),
      dataIndex: 'verified',
      render: (v) => (
        <Tag color={v ? 'green' : 'grey'} shape='circle' size='small'>
          {v ? t('已实名') : t('未实名')}
        </Tag>
      ),
    },
    {
      title: t('注册时间'),
      dataIndex: 'created_at',
      render: renderTime,
    },
    {
      title: t('最后使用时间'),
      dataIndex: 'last_used',
      render: renderTime,
    },
    ...(pointsOn
      ? [
          {
            title: t('积分余额'),
            dataIndex: 'points_balance',
            render: (v) => quotaToPoints(v),
          },
        ]
      : []),
    {
      title: t('账户余额'),
      dataIndex: 'quota',
      render: (v) => renderQuota(v),
    },
    ...(pointsOn
      ? [
          {
            title: t('积分消耗'),
            dataIndex: 'points_used',
            render: (v) => quotaToPoints(v),
          },
        ]
      : []),
    {
      title: t('余额消耗'),
      dataIndex: 'used_quota',
      render: (v) => renderQuota(v),
    },
  ];

  return (
    <Table
      columns={columns}
      dataSource={items}
      loading={loading}
      rowKey='id'
      size='small'
      scroll={{ x: 'max-content' }}
      pagination={{
        currentPage: page,
        pageSize: pageSize,
        total: total,
        showSizeChanger: true,
        pageSizeOpts: [10, 20, 50, 100],
        onPageChange: (p) => setPage(p),
        onPageSizeChange: (ps) => {
          setPageSize(ps);
          setPage(1);
        },
      }}
      empty={
        <Empty
          image={<IllustrationNoResult style={{ width: 120, height: 120 }} />}
          darkModeImage={
            <IllustrationNoResultDark style={{ width: 120, height: 120 }} />
          }
          description={t('暂无邀请用户')}
          style={{ padding: 24 }}
        />
      }
    />
  );
};

export default InvitedUsersTable;
