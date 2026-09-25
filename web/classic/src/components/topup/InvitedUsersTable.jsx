import React, { useState, useEffect } from 'react';
import { Table, Tag, Typography, Empty } from '@douyinfe/semi-ui';
import {
  IllustrationNoResult,
  IllustrationNoResultDark,
} from '@douyinfe/semi-illustrations';
import { API, showError, timestamp2string } from '../../helpers';

const { Text } = Typography;

// 后端 cash_paid_fen 以「分」存整数，这里只做展示换算，不引入浮点。
function fenToYuanText(fen) {
  const n = parseInt(fen, 10);
  if (!Number.isFinite(n)) return '0.00';
  const whole = Math.trunc(n / 100);
  const frac = String(Math.abs(n % 100)).padStart(2, '0');
  return `${whole}.${frac}`;
}

// 「我邀请的用户」列表：分页展示被当前用户邀请注册的下线，按注册时间由近到远。
// 数据来自 GET /api/user/aff/invitees（{ items, total, verified_total }）。
//
// 只展示「是不是真人、还在不在用、付了多少真钱」。不展示余额与消耗：余额是对方的
// 私人财务信息；消耗按账单原价累计，积分抵扣和套餐内调用都混在里面，会让白嫖用户
// 看起来和付费用户一样；积分字段还会暴露平台的赠送力度。
const InvitedUsersTable = ({ t, onStats }) => {
  const [loading, setLoading] = useState(false);
  const [items, setItems] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);

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
    {
      title: t('累计实付'),
      dataIndex: 'cash_paid_fen',
      render: (v) => `¥${fenToYuanText(v)}`,
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
