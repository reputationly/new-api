import React from 'react';
import { Button, Space, Tag, Tooltip, Typography } from '@douyinfe/semi-ui';
import {
  MODERATION_WORDS_SEP,
  moderationActionColor,
  moderationActionLabel,
  moderationCategoryLabel,
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

export const getModerationLogsColumns = ({
  t,
  openContentModal,
  openMediaModal,
}) => [
  {
    title: t('时间'),
    width: 100,
    dataIndex: 'created_at',
    render: (v) => <Text>{renderTimestamp(v)}</Text>,
  },
  {
    title: t('处置'),
    width: 110,
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
    width: 100,
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
    width: 110,
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
    width: 380,
    dataIndex: 'preview',
    render: (preview, record) => (
      <Space spacing={4}>
        {/* 有 judged_preview 时**优先显示它**：那才是模型实际读到的文字。
            L1 为省 token 只审最新一轮用户输入，而 preview 是全量拼接文本的开头——
            一个编程助手请求的 preview 全是 system prompt，模型判的却是末尾那句提问，
            两者可以一个字都不重叠。默认显示 preview 等于让人拿模型没看过的文字去复核。
            完整请求仍可从 tooltip 看到，不丢上下文。 */}
        {record.judged_preview || preview ? (
          <Tooltip
            content={
              record.judged_preview
                ? `${t('模型判定的文本')}：${record.judged_preview}\n\n${t('完整请求')}：${preview || '-'}`
                : preview
            }
            style={{ whiteSpace: 'pre-wrap', maxWidth: 520 }}
          >
            <Text ellipsis style={{ maxWidth: 280, display: 'inline-block' }}>
              {record.judged_preview || preview}
            </Text>
          </Tooltip>
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
        {/* 媒体记录没有文本预览，这一格恒为空——「查看媒体」就是它的等价物。
            object_key 只有被判违规的图片/视频才有（通过与待复核的不留存）。 */}
        {record.object_key && (
          <Button
            size='small'
            theme='borderless'
            type='primary'
            onClick={() => openMediaModal(record)}
          >
            {t('查看媒体')}
          </Button>
        )}
      </Space>
    ),
  },
  {
    title: t('用户'),
    width: 80,
    dataIndex: 'username',
    render: (username, record) => (
      <Text>{username || record.user_id || '-'}</Text>
    ),
  },
  {
    title: t('模型'),
    width: 120,
    dataIndex: 'model_name',
    render: (v) => (v ? <Tag shape='circle'>{v}</Tag> : <Text>-</Text>),
  },
  // 「分组」与「来源」两列已移除，不是遗漏：
  //   - source 目前恒为 self（§9.3 的上游拒绝信号回收还没做），整列只有一个值；
  //   - group 在单分组部署下恒为 default，要到按分组灰度时才有信息量。
  //
  // 两者的**筛选条件仍然保留**——需要按它们查的时候筛就是了，
  // 而列表宽度是稀缺资源，得留给内容预览。等 upstream 信号接进来、
  // 或者真按分组放量了，再把对应那列加回来。
  {
    title: t('判定层'),
    width: 60,
    dataIndex: 'provider',
    render: (v) => <Text>{v || '-'}</Text>,
  },
  {
    title: t('请求 / 任务 ID'),
    width: 100,
    dataIndex: 'request_id',
    // 异步产物审核没有请求 ID：任务是几百秒前那次请求提交的，那个 RequestId
    // 早已不在上下文里。task_id 才是这类记录唯一能拿去定位的标识，所以回落显示它。
    //
    // **不加「任务」前缀**：这一格的值是拿来复制去搜的，前缀会一起被复制走，
    // 粘进筛选框就搜不到。上面的筛选框两列都匹配，所以也不需要区分是哪一种。
    render: (v, record) => {
      const shown = v || record.task_id;
      if (!shown) {
        return <Text type='tertiary'>-</Text>;
      }
      return (
        <Text ellipsis={{ showTooltip: true }} style={{ maxWidth: 85 }}>
          {shown}
        </Text>
      );
    },
  },
];
