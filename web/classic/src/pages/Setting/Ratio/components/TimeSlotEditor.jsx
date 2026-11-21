import React, { useCallback, useMemo } from 'react';
import {
  Button,
  Card,
  Input,
  Select,
  Space,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { IconDelete, IconPlus } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';

const { Text } = Typography;

// 时段（slot）编辑器：一个时段可以含**多个不连续区间**，也可以是「其余时间」（补集）。
//
// 这套形状是为了对上供应商的口径：官方只有两个时段——
//   高峰时段 = 工作日 09-12 + 14-18（两段不连续）
//   空闲时段 = 其余全部（补集）
// 旧版「一个模板只能一段」迫使运营把空闲拆成夜间/午休/周末三条，既繁琐又容易漏。
//
// 数据形状（与 setting/ratio_setting/group_time_ratio.go 一致）：
//   windows: { key: { label, tz, segments:[{start,end,days}], exclude_of } }
// 历史单段格式（start/end/days）仍可读入，编辑后统一写成 segments。

export const DAY_PRESETS = [
  { key: 'everyday', label: '每天', days: [] },
  { key: 'weekday', label: '工作日', days: [1, 2, 3, 4, 5] },
  { key: 'weekend', label: '周末', days: [0, 6] },
];

export const RANGE_PRESETS = [
  { key: 'am', label: '09:00-12:00', start: '09:00', end: '12:00' },
  { key: 'pm', label: '14:00-18:00', start: '14:00', end: '18:00' },
  { key: 'day', label: '09:00-18:00', start: '09:00', end: '18:00' },
  {
    key: 'night',
    label: '18:00-09:00（跨午夜）',
    start: '18:00',
    end: '09:00',
  },
];

const WEEKDAY_LABELS = ['日', '一', '二', '三', '四', '五', '六'];

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
  if (key === '' || key === '0,1,2,3,4,5,6') return 'everyday';
  if (key === '1,2,3,4,5') return 'weekday';
  if (key === '0,6') return 'weekend';
  return 'custom';
};

const segmentsOf = (win) => {
  if (Array.isArray(win?.segments) && win.segments.length > 0) {
    return win.segments;
  }
  if (win?.start && win?.end) {
    return [{ start: win.start, end: win.end, days: win.days || [] }];
  }
  return [];
};

const TimeSlotEditor = ({ value, onChange }) => {
  const { t } = useTranslation();

  const config = useMemo(() => {
    if (!value) return { windows: {}, rules: {} };
    try {
      const parsed = typeof value === 'string' ? JSON.parse(value) : value;
      return { windows: parsed?.windows || {}, rules: parsed?.rules || {} };
    } catch (e) {
      return { windows: {}, rules: {} };
    }
  }, [value]);

  const emit = useCallback(
    (nextWindows, nextRules) => {
      onChange(
        JSON.stringify(
          {
            windows: nextWindows,
            rules: nextRules === undefined ? config.rules : nextRules,
          },
          null,
          2,
        ),
      );
    },
    [onChange, config.rules],
  );

  const slots = useMemo(
    () =>
      Object.entries(config.windows).map(([key, win]) => ({
        key,
        ...win,
        segments: segmentsOf(win),
      })),
    [config.windows],
  );

  const updateSlot = (key, patch) => {
    const next = {
      ...config.windows,
      [key]: { ...config.windows[key], ...patch },
    };
    // 写成 segments 之后清掉历史单段字段，避免一份配置里两套写法并存
    if (patch.segments || patch.exclude_of !== undefined) {
      delete next[key].start;
      delete next[key].end;
      delete next[key].days;
    }
    emit(next);
  };

  const addSlot = () => {
    const n = Object.keys(config.windows).length + 1;
    let key = `slot${n}`;
    while (config.windows[key]) key = `${key}x`;
    const next = {
      ...config.windows,
      [key]: {
        label: t('新时段'),
        segments: [{ start: '09:00', end: '12:00', days: [] }],
      },
    };
    emit(next);
  };

  // 删时段要连带删掉引用它的规则：否则后端会以「引用了不存在的时段」拒绝保存，
  // 而界面上看不出是哪条规则在引用。
  const removeSlot = (key) => {
    const nextWindows = { ...config.windows };
    delete nextWindows[key];
    const nextRules = {};
    Object.entries(config.rules).forEach(([scope, byModel]) => {
      const cleaned = {};
      Object.entries(byModel).forEach(([model, list]) => {
        const kept = (list || []).filter((r) => r.window !== key);
        if (kept.length > 0) cleaned[model] = kept;
      });
      if (Object.keys(cleaned).length > 0) nextRules[scope] = cleaned;
    });
    emit(nextWindows, nextRules);
  };

  const updateSegment = (key, idx, patch) => {
    const win = config.windows[key];
    const segs = segmentsOf(win).map((s, i) =>
      i === idx ? { ...s, ...patch } : s,
    );
    updateSlot(key, { segments: segs });
  };

  const addSegment = (key) => {
    const segs = segmentsOf(config.windows[key]);
    updateSlot(key, {
      segments: [...segs, { start: '14:00', end: '18:00', days: [] }],
    });
  };

  const removeSegment = (key, idx) => {
    const segs = segmentsOf(config.windows[key]).filter((_, i) => i !== idx);
    updateSlot(key, { segments: segs });
  };

  return (
    <div>
      <div className='flex items-center justify-between mb-2'>
        <Space>
          <Text strong>{t('时段')}</Text>
          <Text type='tertiary' size='small'>
            {t('（{{n}} 个）', { n: slots.length })}
          </Text>
          <Text type='tertiary' size='small'>
            {t(
              '一个时段可以含多个不连续区间；「其余时间」表示除另一时段之外的全部时间',
            )}
          </Text>
        </Space>
        <Button size='small' icon={<IconPlus />} onClick={addSlot}>
          {t('新建时段')}
        </Button>
      </div>

      <div className='grid grid-cols-1 xl:grid-cols-2 gap-3'>
        {slots.map((slot) => {
          const isExclude = Boolean(slot.exclude_of);
          return (
            <Card
              key={slot.key}
              className='!rounded-xl'
              bodyStyle={{ padding: 12 }}
            >
              <div className='flex items-center justify-between mb-2'>
                <Space>
                  <Tag color='blue' shape='circle' size='small'>
                    {slot.key}
                  </Tag>
                  <Input
                    size='small'
                    style={{ width: 150 }}
                    value={slot.label || ''}
                    placeholder={t('显示名，如 高峰时段')}
                    onChange={(v) => updateSlot(slot.key, { label: v })}
                  />
                </Space>
                <Space>
                  {/* 顶层快捷开关：一次把本时段的**所有段**设成同一种生效日。
                      官方口径的"工作日 09-12 + 14-18"就是这种形状，先选工作日再填两段，
                      比每段各点一次省事，也不会出现两段生效日不一致的低级错。 */}
                  <Select
                    size='small'
                    style={{ width: 110 }}
                    placeholder={t('生效日')}
                    value={undefined}
                    disabled={isExclude}
                    optionList={DAY_PRESETS.map((d) => ({
                      value: d.key,
                      label: d.label,
                    }))}
                    onChange={(v) => {
                      const p = DAY_PRESETS.find((x) => x.key === v);
                      if (!p) return;
                      updateSlot(slot.key, {
                        segments: slot.segments.map((seg) => ({
                          ...seg,
                          days: p.days,
                        })),
                      });
                    }}
                  />
                  <Button
                    size='small'
                    type='danger'
                    theme='borderless'
                    icon={<IconDelete />}
                    onClick={() => removeSlot(slot.key)}
                  />
                </Space>
              </div>

              <Space
                vertical
                align='start'
                spacing={8}
                style={{ width: '100%' }}
              >
                <Space>
                  <Text size='small' type='tertiary'>
                    {t('类型')}
                  </Text>
                  <Select
                    size='small'
                    style={{ width: 150 }}
                    value={isExclude ? 'exclude' : 'segments'}
                    optionList={[
                      { value: 'segments', label: t('指定时间段') },
                      { value: 'exclude', label: t('其余时间') },
                    ]}
                    onChange={(v) => {
                      if (v === 'exclude') {
                        const other = slots.find((s) => s.key !== slot.key);
                        updateSlot(slot.key, {
                          exclude_of: other ? other.key : '',
                          segments: undefined,
                        });
                        return;
                      }
                      updateSlot(slot.key, {
                        exclude_of: undefined,
                        segments:
                          slot.segments.length > 0
                            ? slot.segments
                            : [{ start: '09:00', end: '12:00', days: [] }],
                      });
                    }}
                  />
                  {isExclude ? (
                    <Space>
                      <Text size='small' type='tertiary'>
                        {t('除')}
                      </Text>
                      <Select
                        size='small'
                        style={{ width: 140 }}
                        value={slot.exclude_of || undefined}
                        placeholder={t('选择时段')}
                        optionList={slots
                          .filter((s) => s.key !== slot.key && !s.exclude_of)
                          .map((s) => ({
                            value: s.key,
                            label: s.label || s.key,
                          }))}
                        onChange={(v) =>
                          updateSlot(slot.key, { exclude_of: v })
                        }
                      />
                      <Text size='small' type='tertiary'>
                        {t('以外的全部时间')}
                      </Text>
                    </Space>
                  ) : null}
                </Space>

                {isExclude ? null : (
                  <div style={{ width: '100%' }}>
                    {slot.segments.map((seg, idx) => (
                      <Space key={idx} className='mb-1'>
                        <Select
                          size='small'
                          style={{ width: 170 }}
                          placeholder={t('预设')}
                          value={undefined}
                          optionList={RANGE_PRESETS.map((p) => ({
                            value: p.key,
                            label: p.label,
                          }))}
                          onChange={(v) => {
                            const p = RANGE_PRESETS.find((x) => x.key === v);
                            if (p) {
                              updateSegment(slot.key, idx, {
                                start: p.start,
                                end: p.end,
                              });
                            }
                          }}
                        />
                        <Input
                          size='small'
                          style={{ width: 72 }}
                          value={seg.start}
                          placeholder='09:00'
                          onChange={(v) =>
                            updateSegment(slot.key, idx, { start: v })
                          }
                        />
                        <Text size='small'>→</Text>
                        <Input
                          size='small'
                          style={{ width: 72 }}
                          value={seg.end}
                          placeholder='12:00'
                          onChange={(v) =>
                            updateSegment(slot.key, idx, { end: v })
                          }
                        />
                        <Select
                          size='small'
                          style={{ width: 110 }}
                          value={matchDayPreset(seg.days)}
                          optionList={[
                            ...DAY_PRESETS.map((d) => ({
                              value: d.key,
                              label: d.label,
                            })),
                            { value: 'custom', label: describeDays(seg.days) },
                          ]}
                          onChange={(v) => {
                            const p = DAY_PRESETS.find((d) => d.key === v);
                            if (p) {
                              updateSegment(slot.key, idx, { days: p.days });
                            }
                          }}
                        />
                        {slot.segments.length > 1 ? (
                          <Button
                            size='small'
                            type='danger'
                            theme='borderless'
                            icon={<IconDelete />}
                            onClick={() => removeSegment(slot.key, idx)}
                          />
                        ) : null}
                      </Space>
                    ))}
                    <Button
                      size='small'
                      theme='borderless'
                      icon={<IconPlus />}
                      onClick={() => addSegment(slot.key)}
                    >
                      {t('添加时间段')}
                    </Button>
                  </div>
                )}
              </Space>
            </Card>
          );
        })}
      </div>
      {slots.length === 0 ? (
        <Text type='tertiary' size='small'>
          {t(
            '还没有时段。先建「高峰时段」，再建一个「其余时间」的空闲时段即可覆盖全天。',
          )}
        </Text>
      ) : null}
    </div>
  );
};

export default TimeSlotEditor;
