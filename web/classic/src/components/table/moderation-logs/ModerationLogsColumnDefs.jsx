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
    // 「查看原文」并在这一列里，而不是单开一列操作列：
    // 独立操作列要 fixed 才够得着，而 Semi 的固定列在这张表的宽度下会浮到「来源」
    // 上面把 Tag 压成「上游拒…」；不 fixed 又会滚出可视区。放在预览旁边一并解决，
    // 语义上也更直白——它展开的就是这一格截断掉的那段内容。
    title: t('内容预览'),
    dataIndex: 'preview',
    render: (preview, record) => (
      <Space spacing={4}>
        {preview ? (
          <Text
            ellipsis={{ showTooltip: true }}
            style={{ maxWidth: 200, display: 'inline-block' }}
          >
            {preview}
          </Text>
        ) : (
          <Text type='tertiary'>-</Text>
        )}
        {/* has_content 由后端算：只有真拦下来、且写入时配了加密密钥的记录才有原文。 */}
        {record.has_content && (
          <Button
            size='small'
            theme='borderless'
            type='primary'
            onClick={() => openContentModal(record)}
          >
            {t('查看原文')}
          </Button>
        )}
      </Space>
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
        <Text ellipsis={{ showTooltip: true }} style={{ maxWidth: 120 }}>
          {v}
        </Text>
      ) : (
        <Text type='tertiary'>-</Text>
      ),
  },
];
