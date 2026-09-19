import React, { useCallback, useMemo, useState } from 'react';
import {
  Button,
  Collapsible,
  Input,
  Select,
  Table,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { IconChevronDown, IconChevronRight } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import { crossesMidnight } from '../../../helpers/discount';

const { Text } = Typography;

// 时段模板编辑器。模板是全局共用的，规则只存模板名——改一次时间，所有引用它的
// 规则同步生效。这也是「批量」的第三条路径（另两条是多选模型、前缀通配）。
//
// 数据形状见 setting/ratio_setting/group_time_ratio.go：
//   { windows: { <key>: {label,start,end,days,tz} }, rules: {...} }
// 本组件只编辑 windows 那一半，rules 由 ModelRatioEditor 的时段列负责。

// 生效日预设。**存的是展开后的数组**，不存 'weekday' 这种语义标签——
// 后端不需要认识任何预设名，回显时反向识别即可。这也让手工编辑 JSON 的人
// 看到的就是最终生效的那份数据。
export const DAY_PRESETS = [
  { key: 'everyday', label: '每天', days: [] },
  { key: 'weekday', label: '工作日（周一~周五）', days: [1, 2, 3, 4, 5] },
  { key: 'weekend', label: '周末', days: [0, 6] },
];

// 时间段预设，纯粹是少打几个字。选完仍可改。
export const RANGE_PRESETS = [
  {
    key: 'deep_night',
    label: '深夜 00:00-08:00',
    start: '00:00',
    end: '08:00',
  },
  { key: 'night', label: '夜间 22:00-06:00', start: '22:00', end: '06:00' },
  {
    key: 'worktime',
    label: '工作时间 09:00-18:00',
    start: '09:00',
    end: '18:00',
  },
];

const WEEKDAY_LABELS = ['日', '一', '二', '三', '四', '五', '六'];

// 生效日的回显。与 helpers/discount.js 的 formatWindowDays 是同一套口径——
// 那边是用户端，这边是管理端，两边显示不一致会让人以为配的不是同一个东西。
export const describeDays = (days) => {
  if (!Array.isArray(days) || days.length === 0) return '每天';
  const set = [...new Set(days)].sort((a, b) => a - b);
  const key = set.join(',');
  if (key === '1,2,3,4,5') return '工作日';
  if (key === '0,6') return '周末';
  if (key === '0,1,2,3,4,5,6') return '每天';
  return set.map((d) => `周${WEEKDAY_LABELS[d] ?? d}`).join('、');
};

const matchDayPreset = (days) => {
  const key = (Array.isArray(days) ? [...new Set(days)] : [])
    .sort((a, b) => a - b)
    .join(',');
  if (key === '') return 'everyday';
  if (key === '1,2,3,4,5') return 'weekday';
  if (key === '0,6') return 'weekend';
  return 'custom';
};

const CLOCK_RE = /^([01]\d|2[0-3]):([0-5]\d)$/;

const TimeWindowEditor = ({ value, onChange, usage = {} }) => {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState(false);

  const config = useMemo(() => {
    if (!value) return { windows: {}, rules: {} };
    try {
      const parsed = typeof value === 'string' ? JSON.parse(value) : value;
      return {
        windows: parsed?.windows || {},
        rules: parsed?.rules || {},
      };
    } catch (e) {
      return { windows: {}, rules: {} };
    }
  }, [value]);

  const emit = useCallback(
    (nextWindows) => {
      // rules 原样带回：本组件只负责 windows 那一半，整段覆盖会把规则清空。
      onChange(
        JSON.stringify({ windows: nextWindows, rules: config.rules }, null, 2),
      );
    },
    [onChange, config.rules],
  );

  const rows = useMemo(
    () =>
      Object.entries(config.windows).map(([key, win]) => ({
        key,
        ...win,
        // 引用数：删模板前必须看得见影响面，同 GroupTable 删分组前查引用的做法
        refCount: usage[key] || 0,
      })),
    [config.windows, usage],
  );

  const patch = useCallback(
    (key, field, v) => {
      const next = { ...config.windows };
      next[key] = { ...(next[key] || {}), [field]: v };
      emit(next);
    },
    [config.windows, emit],
  );

  const addWindow = useCallback(() => {
    const next = { ...config.windows };
    // 键要稳定且不与已有冲突：规则引用的是键，重命名等于让所有引用悬空。
    let i = 1;
    let key = `window_${i}`;
    while (next[key]) {
      i += 1;
      key = `window_${i}`;
    }
    next[key] = { label: '', start: '00:00', end: '08:00', days: [] };
    emit(next);
    setExpanded(true);
  }, [config.windows, emit]);

  const removeWindow = useCallback(
    (key) => {
      const next = { ...config.windows };
      delete next[key];
      emit(next);
    },
    [config.windows, emit],
  );

  const columns = [
    {
      title: t('模板键'),
      dataIndex: 'key',
      width: 130,
      render: (text) => (
        <Tag size='small' shape='circle' color='white'>
          {text}
        </Tag>
      ),
    },
    {
      title: t('显示名'),
      dataIndex: 'label',
      width: 140,
      render: (text, record) => (
        <Input
          size='small'
          value={text || ''}
          placeholder={t('如：深夜档')}
          onChange={(v) => patch(record.key, 'label', v)}
        />
      ),
    },
    {
      title: t('时间段'),
      dataIndex: 'start',
      width: 260,
      render: (text, record) => {
        const startOk = CLOCK_RE.test(record.start || '');
        const endOk = CLOCK_RE.test(record.end || '');
        // 跨午夜判定复用展示侧那份（helpers/discount.js）：本地再写一份字符串比较
        // 必然与它分叉，而分叉的表现是管理员在编辑器里看到「跨午夜」提示、
        // 用户在模型广场看到的区间却不跨（或反过来）。
        const isCrossMidnight = crossesMidnight(record);
        return (
          <div className='flex flex-col gap-1'>
            <div className='flex items-center gap-1'>
              <Input
                size='small'
                style={{ width: 70 }}
                value={record.start || ''}
                validateStatus={startOk ? 'default' : 'error'}
                onChange={(v) => patch(record.key, 'start', v)}
              />
              <span>-</span>
              <Input
                size='small'
                style={{ width: 70 }}
                value={record.end || ''}
                validateStatus={endOk ? 'default' : 'error'}
                onChange={(v) => patch(record.key, 'end', v)}
              />
              <Select
                size='small'
                style={{ width: 60 }}
                placeholder={t('预设')}
                value={null}
                optionList={RANGE_PRESETS.map((p) => ({
                  label: t(p.label),
                  value: p.key,
                }))}
                onChange={(k) => {
                  const p = RANGE_PRESETS.find((x) => x.key === k);
                  if (!p) return;
                  const next = { ...config.windows };
                  next[record.key] = {
                    ...(next[record.key] || {}),
                    start: p.start,
                    end: p.end,
                  };
                  emit(next);
                }}
              />
            </div>
            {isCrossMidnight && (
              <Text type='warning' size='small'>
                {t('跨午夜，按起始日判定生效日')}
              </Text>
            )}
          </div>
        );
      },
    },
    {
      title: t('生效日'),
      dataIndex: 'days',
      width: 230,
      render: (text, record) => {
        const preset = matchDayPreset(record.days);
        return (
          <div className='flex items-center gap-1'>
            <Select
              size='small'
              style={{ width: 110 }}
              value={preset}
              optionList={[
                ...DAY_PRESETS.map((p) => ({
                  label: t(p.label),
                  value: p.key,
                })),
                { label: t('自定义'), value: 'custom' },
              ]}
              onChange={(k) => {
                const p = DAY_PRESETS.find((x) => x.key === k);
                // 切到「自定义」不动数据：让人接着在右边的多选里改，
                // 清空会把刚选好的几天抹掉
                if (!p) return;
                patch(record.key, 'days', p.days);
              }}
            />
            {preset === 'custom' && (
              <Select
                size='small'
                multiple
                style={{ width: 110 }}
                value={record.days || []}
                optionList={WEEKDAY_LABELS.map((d, i) => ({
                  label: `周${d}`,
                  value: i,
                }))}
                onChange={(v) => patch(record.key, 'days', v)}
              />
            )}
          </div>
        );
      },
    },
    {
      title: t('时区'),
      dataIndex: 'tz',
      width: 140,
      render: (text, record) => (
        <Input
          size='small'
          value={text || ''}
          placeholder='Asia/Shanghai'
          onChange={(v) => patch(record.key, 'tz', v)}
        />
      ),
    },
    {
      title: t('引用'),
      dataIndex: 'refCount',
      width: 130,
      render: (count, record) => (
        <div className='flex items-center gap-2'>
          <Text type={count > 0 ? 'primary' : 'tertiary'} size='small'>
            {t('{{count}} 条规则', { count })}
          </Text>
          <Button
            size='small'
            theme='borderless'
            type='danger'
            disabled={count > 0}
            onClick={() => removeWindow(record.key)}
          >
            {t('删除')}
          </Button>
        </div>
      ),
    },
  ];

  return (
    <div className='mb-4 rounded-lg border border-[var(--semi-color-border)] p-3'>
      <div className='flex items-center justify-between'>
        <div
          className='flex cursor-pointer items-center gap-1'
          onClick={() => setExpanded((v) => !v)}
        >
          {expanded ? <IconChevronDown /> : <IconChevronRight />}
          <Text strong>{t('时段模板')}</Text>
          <Text type='tertiary' size='small'>
            {t('（{{count}} 个）', { count: rows.length })}
          </Text>
        </div>
        <Button size='small' onClick={addWindow}>
          {t('新建模板')}
        </Button>
      </div>

      <Collapsible isOpen={expanded}>
        <div className='mt-3'>
          <Text type='tertiary' size='small' className='mb-2 block'>
            {t(
              '模板定义「什么时候」，折扣力度在下方规则表里按模型配。改模板时间会让所有引用它的规则同步生效。',
            )}
          </Text>
          {rows.length === 0 ? (
            <Text type='tertiary' size='small'>
              {t(
                '还没有时段模板。新建一个，再到下方规则表里给模型配时段折扣。',
              )}
            </Text>
          ) : (
            <Table
              columns={columns}
              dataSource={rows}
              rowKey='key'
              pagination={false}
              size='small'
            />
          )}
        </div>
      </Collapsible>
    </div>
  );
};

export default TimeWindowEditor;
