import React, { useMemo, useState } from 'react';
import {
  Button,
  Empty,
  InputNumber,
  Popconfirm,
  Select,
  Space,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { IconDelete, IconPlus } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import { describeDays } from './TimeSlotEditor';

const { Text } = Typography;

// 按分量排的输入顺序：最常改的在前。后端白名单见
// setting/ratio_setting/group_time_ratio.go: timeFactorComponents。
const COMPONENTS = [
  { key: 'input', label: '输入' },
  { key: 'output', label: '输出' },
  { key: 'cache', label: '缓存命中' },
  { key: 'create_cache', label: '缓存创建' },
  { key: 'image', label: '图片输入' },
  { key: 'audio_input', label: '音频输入' },
  { key: 'audio_output', label: '音频输出' },
  { key: 'per_call', label: '按次价' },
];

const slotSummary = (win) => {
  if (!win) return '';
  if (win.exclude_of) return 'exclude';
  const segs =
    Array.isArray(win.segments) && win.segments.length > 0
      ? win.segments
      : win.start && win.end
        ? [{ start: win.start, end: win.end, days: win.days }]
        : [];
  return segs
    .map((s) => describeDays(s.days) + ' ' + s.start + '-' + s.end)
    .join('、');
};

// 某个模型的「分时价」区块。**内嵌在模型计费编辑器里**——时段是定价的一部分，
// 不是另一张页面：左边选中模型，这里就是它每个时段的价目。
//
// 只渲染该模型**有常规价的分量**（没配缓存价的模型不会出现缓存框）。
const ModelTimePriceSection = ({
  modelName,
  windows,
  rules,
  base,
  onChange,
}) => {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState({});

  const componentList = useMemo(
    () => COMPONENTS.filter((c) => Number(base && base[c.key]) > 0),
    [base],
  );
  const usedWindows = useMemo(
    () => new Set((rules || []).map((r) => r.window)),
    [rules],
  );
  const availableWindows = useMemo(
    () => Object.keys(windows || {}).filter((k) => !usedWindows.has(k)),
    [windows, usedWindows],
  );

  const patch = (idx, updater) => {
    const next = (rules || []).map((r, i) => (i === idx ? updater(r) : r));
    onChange(next);
  };

  const setPrice = (idx, comp, value) => {
    patch(idx, (rule) => {
      const prices = { ...(rule.prices || {}) };
      if (value === null || !(value > 0)) {
        delete prices[comp];
      } else {
        prices[comp] = Number(Number(value).toFixed(4));
      }
      const next = { ...rule, prices };
      delete next.value;
      delete next.output_factor;
      delete next.factors;
      return next;
    });
  };

  const scaleRule = (idx, factor) => {
    patch(idx, (rule) => {
      const prices = {};
      Object.entries(rule.prices || {}).forEach(([k, v]) => {
        const n = Number(v) * factor;
        if (n > 0) prices[k] = Number(n.toFixed(4));
      });
      return { ...rule, prices };
    });
  };

  const resetRule = (idx) => {
    patch(idx, (rule) => {
      const prices = {};
      Object.entries(base || {}).forEach(([k, v]) => {
        if (Number(v) > 0) prices[k] = Number(Number(v).toFixed(4));
      });
      return { ...rule, prices };
    });
  };

  const addRule = (windowKey) => {
    if (!windowKey) return;
    const prices = {};
    if (Number(base && base.input) > 0) {
      prices.input = Number(Number(base.input).toFixed(4));
    }
    onChange([...(rules || []), { window: windowKey, prices }]);
  };

  const summaryOf = (rule) =>
    COMPONENTS.filter((c) => Number((rule.prices || {})[c.key]) > 0)
      .map((c) => t(c.label) + ' ¥' + Number((rule.prices || {})[c.key]))
      .join('  ');

  return (
    <div style={{ marginTop: 8 }}>
      <div className='flex items-center justify-between mb-2'>
        <Space>
          <Text strong>{t('分时价')}</Text>
          <Text type='tertiary' size='small'>
            {t('（{{n}} 档，可选）', { n: (rules || []).length })}
          </Text>
        </Space>
        <Space>
          {availableWindows.length > 0 ? (
            <Select
              size='small'
              style={{ width: 160 }}
              placeholder={t('+ 绑定时段')}
              value={undefined}
              optionList={availableWindows.map((k) => ({
                value: k,
                label: (windows[k] && windows[k].label) || k,
              }))}
              onChange={addRule}
            />
          ) : (
            <Text type='tertiary' size='small'>
              {Object.keys(windows || {}).length === 0
                ? t('还没有时段模板，先在页面顶部「时段模板」里建一个')
                : t('所有时段都绑过了')}
            </Text>
          )}
        </Space>
      </div>

      {(rules || []).length === 0 ? (
        <Empty
          title={t('这个模型没有分时价')}
          description={t('不分时段时按上面的基础价格计费')}
        />
      ) : (
        (rules || []).map((rule, idx) => {
          const win = (windows || {})[rule.window];
          const isOpen = Boolean(expanded[rule.window + idx]);
          return (
            <div
              key={rule.window + '-' + idx}
              className='rounded-xl'
              style={{
                border: '1px solid var(--semi-color-border)',
                marginBottom: 8,
                overflow: 'hidden',
              }}
            >
              <div
                className='flex items-center justify-between px-3 py-2'
                style={{ background: 'var(--semi-color-fill-0)' }}
              >
                <Space wrap>
                  <Tag color='cyan' shape='circle' size='small'>
                    {(win && win.label) || rule.window}
                  </Tag>
                  <Text type='tertiary' size='small'>
                    {win && win.exclude_of
                      ? t('其余时间（除「{{name}}」以外）', {
                          name:
                            (windows[win.exclude_of] &&
                              windows[win.exclude_of].label) ||
                            win.exclude_of,
                        })
                      : slotSummary(win)}
                  </Text>
                  <Text size='small' type='secondary'>
                    {summaryOf(rule)}
                  </Text>
                </Space>
                <Space>
                  <Button
                    size='small'
                    theme='borderless'
                    onClick={() =>
                      setExpanded((prev) => ({
                        ...prev,
                        [rule.window + idx]: !isOpen,
                      }))
                    }
                  >
                    {isOpen ? t('收起') : t('展开')}
                  </Button>
                  <Popconfirm
                    title={t('确认删除该时段价？')}
                    onConfirm={() =>
                      onChange((rules || []).filter((_, i) => i !== idx))
                    }
                  >
                    <Button
                      size='small'
                      type='danger'
                      theme='borderless'
                      icon={<IconDelete />}
                    />
                  </Popconfirm>
                </Space>
              </div>

              {isOpen ? (
                <div className='px-3 py-2'>
                  <div className='flex items-center gap-2 mb-2'>
                    <Button size='small' onClick={() => scaleRule(idx, 0.5)}>
                      ×0.5
                    </Button>
                    <Button size='small' onClick={() => scaleRule(idx, 1.2)}>
                      ×1.2
                    </Button>
                    <Button size='small' onClick={() => resetRule(idx)}>
                      {t('重置为常规价')}
                    </Button>
                    <Text type='tertiary' size='small'>
                      {t('留空 = 该分量不随时段变')}
                    </Text>
                  </div>
                  <div className='grid grid-cols-2 xl:grid-cols-4 gap-x-4 gap-y-2'>
                    {componentList.map((c) => {
                      const value = Number((rule.prices || {})[c.key]);
                      const baseValue = Number(base[c.key]);
                      const factor =
                        value > 0 && baseValue > 0 ? value / baseValue : null;
                      return (
                        <div key={c.key}>
                          <Text size='small' type='tertiary'>
                            {t(c.label)}
                          </Text>
                          <Space>
                            <InputNumber
                              size='small'
                              min={0}
                              step={0.1}
                              precision={4}
                              style={{ width: 110 }}
                              value={value > 0 ? value : undefined}
                              placeholder={t('不变')}
                              onChange={(v) =>
                                setPrice(
                                  idx,
                                  c.key,
                                  typeof v === 'number' ? v : null,
                                )
                              }
                            />
                            {factor ? (
                              <Tag
                                color={factor > 1 ? 'orange' : 'cyan'}
                                shape='circle'
                                size='small'
                              >
                                ×{Number(factor.toFixed(3))}
                              </Tag>
                            ) : null}
                          </Space>
                        </div>
                      );
                    })}
                  </div>
                </div>
              ) : null}
            </div>
          );
        })
      )}
      {modelName ? null : null}
    </div>
  );
};

export default ModelTimePriceSection;
