import React from 'react';
import { Button, Space, Tag, Tooltip, Typography } from '@douyinfe/semi-ui';
import {
  MODERATION_WORDS_SEP,
  moderationActionColor,
  moderationActionLabel,
  moderationCategoryLabel,
  moderationSourceLabel,
} from '../../../constants/moderation.constants';

const { Text } = Typography;

const renderTimestamp = (seconds) => {
  if (!seconds) return '-';
  const d = new Date(seconds * 1000);
  const p = (n) => ('0' + n).slice(-2);
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(
    d.getHours(),
  )}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
};

const splitList = (raw, sep) =>
  (raw || '')
    .split(sep)
    .map((s) => s.trim())
    .filter(Boolean);

export const getModerationLogsColumns = ({ t, openContentModal }) => [
  {
    title: t('时间'),
    dataIndex: 'created_at',
    render: (v) => <Text>{renderTimestamp(v)}</Text>,
  },
  {
    title: t('处置'),
    dataIndex: 'action',
    render: (action, record) => (
      <Space spacing={4}>
        <Tag color={moderationActionColor(action)} shape='circle'>
          {t(moderationActionLabel(action))}
        </Tag>
        {/* 判了但没执行的必须标出来：仅观察模式下 action 仍是 block，
            只看处置列会把观察期的误杀全算成真拦截，灰度期的数字直接是错的。 */}
        {action === 'block' && !record.enforced && (
          <Tooltip content={t('仅观察模式：已判定但请求未被拒绝')}>
            <Tag color='grey' shape='circle'>
              {t('仅观察')}
            </Tag>
          </Tooltip>
        )}
      </Space>
    ),
  },
  {
    title: t('命中词'),
    dataIndex: 'words',
    render: (words) => {
      const list = splitList(words, MODERATION_WORDS_SEP);
      if (!list.length) return <Text type='tertiary'>-</Text>;
      return (
        <Space spacing={4} wrap>
          {list.map((w) => (
            <Tag key={w} color='red' shape='circle'>
              {w}
            </Tag>
          ))}
        </Space>
      );
    },
  },
  {
    title: t('类别'),
    dataIndex: 'categories',
    render: (categories) => {
      const list = splitList(categories, ',');
      if (!list.length) return <Text type='tertiary'>-</Text>;
      return (
        <Space spacing={4} wrap>
          {list.map((c) => (
            <Tag key={c} color='orange' shape='circle'>
              {t(moderationCategoryLabel(c))}
            </Tag>
          ))}
        </Space>
      );
    },
  },
  {
    title: t('内容预览'),
    dataIndex: 'preview',
    render: (preview) =>
      preview ? (
        <Text
          ellipsis={{ showTooltip: true }}
          style={{ maxWidth: 260, display: 'inline-block' }}
        >
          {preview}
        </Text>
      ) : (
        <Text type='tertiary'>-</Text>
      ),
  },
  {
    title: t('用户'),
    dataIndex: 'username',
    render: (username, record) => (
      <Text>{username || record.user_id || '-'}</Text>
    ),
  },
  {
    title: t('模型'),
    dataIndex: 'model_name',
    render: (v) => (v ? <Tag shape='circle'>{v}</Tag> : <Text>-</Text>),
  },
  {
    title: t('分组'),
    dataIndex: 'group',
    render: (v) => <Text>{v || '-'}</Text>,
  },
  {
    title: t('来源'),
    dataIndex: 'source',
    render: (v) => (
      <Tag color={v === 'upstream' ? 'purple' : 'blue'} shape='circle'>
        {t(moderationSourceLabel(v))}
      </Tag>
    ),
  },
  {
    title: t('判定层'),
    dataIndex: 'provider',
    render: (v) => <Text>{v || '-'}</Text>,
  },
  {
    title: t('请求 ID'),
    dataIndex: 'request_id',
    render: (v) =>
      v ? (
        <Text ellipsis={{ showTooltip: true }} style={{ maxWidth: 160 }}>
          {v}
        </Text>
      ) : (
        <Text type='tertiary'>-</Text>
      ),
  },
  {
    title: t('操作'),
    dataIndex: 'operate',
    fixed: 'right',
    render: (_, record) =>
      // has_content 由后端算：只有真拦下来、且写入时配了加密密钥的记录才有原文。
      record.has_content ? (
        <Button
          size='small'
          type='tertiary'
          onClick={() => openContentModal(record)}
        >
          {t('查看原文')}
        </Button>
      ) : (
        <Text type='tertiary'>-</Text>
      ),
  },
];
