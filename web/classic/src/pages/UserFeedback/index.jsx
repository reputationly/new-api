import React, { useEffect, useState } from 'react';
import {
  Button,
  Input,
  Select,
  SideSheet,
  Space,
  Spin,
  Table,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { IconComment } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess } from '../../helpers';
import { copy, createCardProPagination } from '../../helpers/utils';
import { useIsMobile } from '../../hooks/common/useIsMobile';
import CardPro from '../../components/common/ui/CardPro';

const { Text } = Typography;

const FEEDBACK_BASE = '/api/user/feedback/admin/assistant';

const CHANNELS = {
  rating: { label: '对话评价', color: 'blue' },
  general: { label: '意见反馈', color: 'purple' },
};

const RATINGS = {
  like: { label: '好评', color: 'green' },
  dislike: { label: '差评', color: 'red' },
};

// 与 DSH 的 FEEDBACK_CATEGORIES 对齐；未知分类原样展示，避免后台丢信息。
const CATEGORIES = {
  'task-result': '任务结果',
  'instruction-following': '指令理解与遵循',
  'product-interaction': '产品功能与交互',
  'service-stability': '稳定性和速度',
  'resource-cost': '资源使用与费用',
  'security-privacy-permission': '安全隐私与权限',
  other: '其他',
};

const CHANNEL_OPTIONS = [
  { value: 'all', label: '全部类型' },
  { value: 'rating', label: '对话评价' },
  { value: 'general', label: '意见反馈' },
];

const RATING_OPTIONS = [
  { value: 'all', label: '全部评价' },
  { value: 'like', label: '好评' },
  { value: 'dislike', label: '差评' },
];

function DetailRow({ label, value, copyable = false }) {
  const onCopy = async () => {
    if (!value) return;
    if (await copy(value)) {
      showSuccess('已复制');
    }
  };
  return (
    <div className='flex items-center gap-3 py-1.5 text-sm'>
      <Text type='tertiary' style={{ width: 88, flexShrink: 0 }}>
        {label}
      </Text>
      <Text className='break-all'>{value || '—'}</Text>
      {copyable && value && (
        <Button size='small' theme='borderless' onClick={onCopy}>
          复制
        </Button>
      )}
    </div>
  );
}

function ContextBlock({ title, value }) {
  if (value === undefined || value === null || value === '') return null;
  const text =
    typeof value === 'string' ? value : JSON.stringify(value, null, 2);
  return (
    <div>
      <Text type='tertiary' className='text-sm'>
        {title}
      </Text>
      <pre className='mt-1 max-h-64 overflow-auto rounded-lg bg-semi-color-fill-0 p-3 text-xs whitespace-pre-wrap break-words'>
        {text}
      </pre>
    </div>
  );
}

export default function UserFeedbackPage() {
  const { t } = useTranslation();
  const isMobile = useIsMobile();

  const [list, setList] = useState([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(10);
  const [loading, setLoading] = useState(false);

  const [filters, setFilters] = useState({
    channel: 'all',
    rating: 'all',
    user_id: '',
    username: '',
    request_id: '',
    keyword: '',
  });

  const [detail, setDetail] = useState(null);
  const [detailLoading, setDetailLoading] = useState(false);

  const openDetail = async (id) => {
    setDetail(null);
    setDetailLoading(true);
    try {
      const res = await API.get(`${FEEDBACK_BASE}/${id}`);
      if (res.data.success) {
        setDetail(res.data.data);
      } else {
        showError(res.data.message);
      }
    } catch {
      showError(t('查询失败'));
    } finally {
      setDetailLoading(false);
    }
  };

  const buildQuery = (p, ps, f) => {
    const params = new URLSearchParams();
    params.set('page', p);
    params.set('page_size', ps);
    if (f.channel !== 'all') params.set('channel', f.channel);
    if (f.rating !== 'all') params.set('rating', f.rating);
    if (f.user_id) params.set('user_id', f.user_id);
    if (f.username) params.set('username', f.username);
    if (f.request_id) params.set('request_id', f.request_id);
    if (f.keyword) params.set('keyword', f.keyword);
    return params.toString();
  };

  // f 默认取当前 filters；重置等场景显式传入清空后的对象，避免闭包读到旧值。
  const loadList = async (p = page, ps = pageSize, f = filters) => {
    setLoading(true);
    try {
      const res = await API.get(`${FEEDBACK_BASE}?${buildQuery(p, ps, f)}`);
      if (res.data.success) {
        setList(res.data.data || []);
        setTotal(res.data.total || 0);
      } else {
        showError(res.data.message);
      }
    } catch {
      showError(t('查询失败'));
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    loadList(page, pageSize);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [page, pageSize]);

  const handleSearch = () => {
    setPage(1);
    loadList(1, pageSize);
  };

  const handleReset = () => {
    const cleared = {
      channel: 'all',
      rating: 'all',
      user_id: '',
      username: '',
      request_id: '',
      keyword: '',
    };
    setFilters(cleared);
    setPage(1);
    loadList(1, pageSize, cleared);
  };

  const columns = [
    { title: 'ID', dataIndex: 'id', width: 70 },
    {
      title: t('用户'),
      dataIndex: 'username',
      width: 140,
      render: (v, r) => v || `#${r.user_id}`,
    },
    {
      title: t('类型'),
      dataIndex: 'channel',
      width: 110,
      render: (v) => {
        const channel = CHANNELS[v] || {};
        return <Tag color={channel.color}>{t(channel.label || v)}</Tag>;
      },
    },
    {
      title: t('评价'),
      dataIndex: 'rating',
      width: 100,
      render: (v) => {
        const rating = RATINGS[v];
        return rating ? <Tag color={rating.color}>{t(rating.label)}</Tag> : '—';
      },
    },
    {
      title: t('分类'),
      dataIndex: 'category',
      width: 130,
      render: (v) => (v ? t(CATEGORIES[v] || v) : '—'),
    },
    {
      title: t('反馈内容'),
      dataIndex: 'content',
      render: (v) =>
        v ? (
          <Text ellipsis={{ showTooltip: true }} style={{ maxWidth: 280 }}>
            {v}
          </Text>
        ) : (
          '—'
        ),
    },
    {
      title: t('模型'),
      dataIndex: 'model',
      width: 150,
      render: (v) => v || '—',
    },
    {
      title: t('对话快照'),
      dataIndex: 'has_context',
      width: 90,
      render: (v) =>
        v ? (
          <Tag color='cyan' size='small'>
            {t('有')}
          </Tag>
        ) : (
          '—'
        ),
    },
    {
      title: t('创建时间'),
      dataIndex: 'created_at',
      width: 170,
      render: (v) => (v ? new Date(v).toLocaleString() : '—'),
    },
    {
      title: t('操作'),
      width: 90,
      render: (_, r) => (
        <Button theme='light' size='small' onClick={() => openDetail(r.id)}>
          {t('查看')}
        </Button>
      ),
    },
  ];

  const descriptionArea = (
    <div className='flex items-center text-blue-500'>
      <IconComment className='mr-2' />
      <Text>{t('用户反馈')}</Text>
    </div>
  );

  const actionsArea = (
    <Space wrap className='w-full'>
      <Select
        value={filters.channel}
        onChange={(v) => setFilters({ ...filters, channel: v })}
        style={{ width: 130 }}
        optionList={CHANNEL_OPTIONS.map((o) => ({
          value: o.value,
          label: t(o.label),
        }))}
      />
      <Select
        value={filters.rating}
        onChange={(v) => setFilters({ ...filters, rating: v })}
        style={{ width: 130 }}
        optionList={RATING_OPTIONS.map((o) => ({
          value: o.value,
          label: t(o.label),
        }))}
      />
      <Input
        prefix={t('用户ID')}
        value={filters.user_id}
        onChange={(v) =>
          setFilters({ ...filters, user_id: v.replace(/\D/g, '') })
        }
        style={{ width: 130 }}
      />
      <Input
        prefix={t('用户名')}
        value={filters.username}
        onChange={(v) => setFilters({ ...filters, username: v })}
        style={{ width: 160 }}
      />
      <Input
        prefix={t('Request ID')}
        value={filters.request_id}
        onChange={(v) => setFilters({ ...filters, request_id: v })}
        style={{ width: 220 }}
        onEnterPress={handleSearch}
      />
      <Input
        prefix={t('搜索内容')}
        value={filters.keyword}
        onChange={(v) => setFilters({ ...filters, keyword: v })}
        style={{ width: 180 }}
        onEnterPress={handleSearch}
      />
      <Button theme='solid' type='primary' onClick={handleSearch}>
        {t('查询')}
      </Button>
      <Button onClick={handleReset}>{t('重置')}</Button>
    </Space>
  );

  return (
    <div className='mt-[60px] px-2'>
      <CardPro
        type='type1'
        descriptionArea={descriptionArea}
        actionsArea={actionsArea}
        paginationArea={createCardProPagination({
          currentPage: page,
          pageSize,
          total,
          onPageChange: (p) => setPage(p),
          onPageSizeChange: (ps) => {
            setPageSize(ps);
            setPage(1);
          },
          isMobile,
          t,
        })}
        t={t}
      >
        <Table
          columns={columns}
          dataSource={list}
          loading={loading}
          rowKey='id'
          pagination={false}
        />
      </CardPro>

      <SideSheet
        title={detail ? `${t('反馈详情')} #${detail.id}` : t('反馈详情')}
        visible={!!detail || detailLoading}
        onCancel={() => {
          setDetail(null);
          setDetailLoading(false);
        }}
        width={640}
      >
        {detailLoading ? (
          <div className='flex justify-center py-12'>
            <Spin size='large' />
          </div>
        ) : (
          detail && (
            <div className='flex flex-col gap-1'>
              <DetailRow
                label={t('用户')}
                value={detail.username || `#${detail.user_id}`}
              />
              <DetailRow
                label={t('类型')}
                value={t(
                  (CHANNELS[detail.channel] || {}).label || detail.channel,
                )}
              />
              <DetailRow
                label={t('评价')}
                value={
                  detail.rating
                    ? t((RATINGS[detail.rating] || {}).label || detail.rating)
                    : ''
                }
              />
              <DetailRow
                label={t('分类')}
                value={
                  detail.category
                    ? t(CATEGORIES[detail.category] || detail.category)
                    : ''
                }
              />
              <DetailRow label={t('模型')} value={detail.model} />
              <DetailRow label={t('会话 ID')} value={detail.session_id} />
              <DetailRow label={t('消息 ID')} value={detail.message_id} />
              <DetailRow
                label={t('Request ID')}
                value={detail.request_id}
                copyable
              />
              <DetailRow
                label={t('客户端版本')}
                value={detail.client_version}
              />
              <DetailRow label={t('平台')} value={detail.platform} />
              <DetailRow label={t('设备信息')} value={detail.device_info} />
              <DetailRow label={t('联系方式')} value={detail.contact} />
              <DetailRow
                label={t('提交时间')}
                value={
                  detail.created_at
                    ? new Date(detail.created_at).toLocaleString()
                    : ''
                }
              />
              <div className='mt-3'>
                <Text type='tertiary' className='text-sm'>
                  {t('反馈内容')}
                </Text>
                <div className='mt-2 rounded-lg bg-semi-color-fill-0 p-3 text-sm whitespace-pre-wrap break-words'>
                  {detail.content || '—'}
                </div>
              </div>
              <div className='mt-4 border-t border-semi-color-border pt-3'>
                <Text type='tertiary' className='text-sm'>
                  {t('对话快照')}
                </Text>
                {detail.has_context && detail.context ? (
                  <div className='mt-2 flex flex-col gap-3'>
                    <div className='rounded-lg bg-amber-50 px-3 py-2 text-xs text-amber-700'>
                      {t(
                        '对话快照由客户端自动附带，可能包含对话内容与系统提示词，请勿外传。',
                      )}
                    </div>
                    <ContextBlock
                      title={t('系统提示词')}
                      value={detail.context.system_prompt}
                    />
                    <ContextBlock
                      title={t('对话上下文')}
                      value={detail.context.messages}
                    />
                    <ContextBlock
                      title={t('请求参数')}
                      value={detail.context.request_params}
                    />
                    <details>
                      <summary className='cursor-pointer text-xs text-semi-color-text-2'>
                        {t('完整快照 JSON')}
                      </summary>
                      <pre className='mt-2 max-h-64 overflow-auto rounded-lg bg-semi-color-fill-0 p-3 text-xs whitespace-pre-wrap break-words'>
                        {JSON.stringify(detail.context, null, 2)}
                      </pre>
                    </details>
                  </div>
                ) : (
                  <div className='mt-2 text-sm text-semi-color-text-2'>
                    {t('未附带对话快照')}
                  </div>
                )}
              </div>
            </div>
          )
        )}
      </SideSheet>
    </div>
  );
}
