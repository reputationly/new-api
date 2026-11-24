import React, { useMemo, useState } from 'react';
import {
  Banner,
  Button,
  Empty,
  Popconfirm,
  Select,
  Space,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { IconDelete, IconPlus } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import { describeDays } from './TimeSlotEditor';
import PriceInput from './PriceInput';
import { PRICE_SUFFIX } from '../hooks/useModelPricingEditorState';

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

// 与后端 pickRuleFrom / matchedTimeRulesLocked 同口径的匹配：
// 权重 精确(2) > 尾部通配(1)，同权重模式串更长者优先。
// 只用于「界面编辑的位置 ≠ 计费命中的位置」的告警，不参与任何价格计算。
const effectivePattern = (scopeRules, modelName) => {
  let best = null;
  let bestWeight = -1;
  Object.keys(scopeRules || {}).forEach((pattern) => {
    let weight = 0;
    if (pattern === modelName) {
      weight = 2;
    } else if (
      typeof pattern === 'string' &&
      pattern.endsWith('*') &&
      modelName.startsWith(pattern.slice(0, -1))
    ) {
      weight = 1;
    }
    if (
      weight > 0 &&
      (weight > bestWeight ||
        (weight === bestWeight && pattern.length > (best || '').length))
    ) {
      best = pattern;
      bestWeight = weight;
    }
  });
  return best;
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
  allRules,
  // 非空 = 按美元录入（见 ModelPricingEditor 的开关）：只影响输入框显示单位，
  // state 与存储仍是人民币。
  sourceCurrency = 'CNY',
}) => {
  const { t } = useTranslation();
  const [expanded, setExpanded] = useState({});

  // 「界面编辑的位置 ≠ 计费命中的位置」告警。
  //
  // 计费侧 matchedTimeRulesLocked 是「先线路 scope、后全网 *」的整组取代，
  // 且 scope 内只取最具体的一个模式串。这里只编辑 rules['*'][模型名]，
  // 以下两种存量配置会让这里显示的内容与实际计费脱节：
  //   1. 非 "*" scope（按线路存的旧规则）里有模式串命中本模型——
  //      该分组用户整组走旧规则，此处编辑对他们不生效；
  //   2. "*" 内没有精确条目、但通配模式串命中——计费生效中，界面却显示 0 档。
  const shadowWarnings = useMemo(() => {
    if (!modelName || !allRules) return [];
    const warnings = [];
    Object.keys(allRules).forEach((scope) => {
      if (scope === '*') return;
      const pattern = effectivePattern(allRules[scope], modelName);
      if (pattern) {
        warnings.push(
          t(
            '分组「{{scope}}」下配置了匹配「{{pattern}}」的分时规则，该分组用户不受此处编辑影响',
            {
              scope,
              pattern,
            },
          ),
        );
      }
    });
    const starPattern = effectivePattern(allRules['*'], modelName);
    if (starPattern && starPattern !== modelName) {
      warnings.push(
        t(
          '计费实际命中通配规则「{{pattern}}」（本模型没有精确条目），此处显示为空但计费生效中',
          {
            pattern: starPattern,
          },
        ),
      );
    }
    return warnings;
  }, [allRules, modelName, t]);

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
        // 10 位：分时价同样要能装下极低单价（4 位会把 1e-7 量级抹成 0）。
        // 精度收敛只做一次，与基础价 PriceInput 的 12 位存储口径不冲突。
        prices[comp] = Number(Number(value).toFixed(10));
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
    // 与 resetRule 同口径：把所有有常规价的分量都落成绝对价目。
    // 只播 input 的话——按次模型会得到空 prices（保存被后端「至少配一个分量」
    // 拒掉），双价模型加「高峰档」则只改输入价、其余分量悄悄不变，
    // 都不是运营要的「这个时段的完整价目」。
    const prices = {};
    Object.entries(base || {}).forEach(([k, v]) => {
      if (Number(v) > 0) prices[k] = Number(Number(v).toFixed(4));
    });
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

      {shadowWarnings.length > 0 ? (
        <Banner
          type='warning'
          bordered
          fullMode={false}
          closeIcon={null}
          style={{ marginBottom: 8 }}
          description={shadowWarnings.join('；')}
        />
      ) : null}

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
                  {/* 与「基础价格」同一套 PriceInput 行式：标签在上、输入通栏、
                      单位后缀、×系数挂在标签右侧。按次价单位是 ¥/次不是
                      ¥/1M tokens，单独给后缀；留空 = 该分量不随时段变。 */}
                  <div className='grid grid-cols-1 md:grid-cols-2 gap-x-6'>
                    {componentList.map((c) => {
                      const value = Number((rule.prices || {})[c.key]);
                      const baseValue = Number(base[c.key]);
                      const factor =
                        value > 0 && baseValue > 0 ? value / baseValue : null;
                      return (
                        <PriceInput
                          key={c.key}
                          label={t(c.label)}
                          value={value > 0 ? value : ''}
                          placeholder={t('不变')}
                          sourceCurrency={sourceCurrency}
                          suffix={c.key === 'per_call' ? '¥/次' : PRICE_SUFFIX}
                          headerAction={
                            factor ? (
                              <Tag
                                color={factor > 1 ? 'orange' : 'cyan'}
                                shape='circle'
                                size='small'
                              >
                                ×{Number(factor.toFixed(3))}
                              </Tag>
                            ) : null
                          }
                          onChange={(v) => {
                            const s = String(v ?? '').trim();
                            if (s === '') {
                              setPrice(idx, c.key, null);
                              return;
                            }
                            const n = Number(s);
                            if (Number.isFinite(n)) {
                              setPrice(idx, c.key, n);
                            }
                          }}
                        />
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
