import React, { useCallback, useEffect, useMemo, useState } from 'react';
import {
  Banner,
  Button,
  Card,
  Empty,
  Input,
  InputNumber,
  Popconfirm,
  Select,
  Space,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { IconDelete, IconPlus, IconSave } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess, showWarning } from '../../../../helpers';
import TimeSlotEditor, { describeDays } from './TimeSlotEditor';

const { Text } = Typography;

// 可以按时段变价的分量，顺序按"最常改"排。
// 后端白名单见 setting/ratio_setting/group_time_ratio.go: timeFactorComponents。
const COMPONENTS = [
  { key: 'input', label: '输入', unit: '¥/1M' },
  { key: 'output', label: '输出', unit: '¥/1M' },
  { key: 'cache', label: '缓存命中', unit: '¥/1M' },
  { key: 'create_cache', label: '缓存创建', unit: '¥/1M' },
  { key: 'image', label: '图片输入', unit: '¥/1M' },
  { key: 'audio_input', label: '音频输入', unit: '¥/1M' },
  { key: 'audio_output', label: '音频输出', unit: '¥/1M' },
  { key: 'per_call', label: '按次价', unit: '¥/次' },
];

const MAX_FACTOR = 20;
const GLOBAL_SCOPE = '*';

// 分时价面板：以**模型**为主轴。
//
// 原来是一张平铺规则表（模型 + 时段 + 两个价格），一个模型的配置散在多行，
// 补缓存还得再点弹窗。现在：左边选模型，右边是它每个时段的全部价目，一次配完。
//
// 存储语义：每个时段存**绝对价目**（prices），不是系数。目录价是对外公布的价目表，
// 填 1 就是 1；常规目录价变了不会偷偷改这里（要跟就点「按比例调整」）。
const TimePricePanel = ({ options, refresh }) => {
  const { t } = useTranslation();

  const raw = (options && options.GroupTimeRatio) || '{}';
  const [draft, setDraft] = useState(raw);
  const [saving, setSaving] = useState(false);
  const [selectedModel, setSelectedModel] = useState('');
  const [newModel, setNewModel] = useState('');

  useEffect(() => {
    setDraft(raw);
  }, [raw]);

  const cfg = useMemo(() => {
    try {
      const parsed = JSON.parse(draft || '{}') || {};
      return { windows: parsed.windows || {}, rules: parsed.rules || {} };
    } catch (e) {
      return { windows: {}, rules: {} };
    }
  }, [draft]);

  const ratioOption = (key) => {
    try {
      return JSON.parse((options && options[key]) || '{}') || {};
    } catch (e) {
      return {};
    }
  };

  const modelRatioMap = useMemo(
    () => ratioOption('ModelRatio'),
    [options && options.ModelRatio],
  );
  const completionRatioMap = useMemo(
    () => ratioOption('CompletionRatio'),
    [options && options.CompletionRatio],
  );
  const cacheRatioMap = useMemo(
    () => ratioOption('CacheRatio'),
    [options && options.CacheRatio],
  );
  const createCacheRatioMap = useMemo(
    () => ratioOption('CreateCacheRatio'),
    [options && options.CreateCacheRatio],
  );
  const imageRatioMap = useMemo(
    () => ratioOption('ImageRatio'),
    [options && options.ImageRatio],
  );
  const audioRatioMap = useMemo(
    () => ratioOption('AudioRatio'),
    [options && options.AudioRatio],
  );
  const audioCompletionRatioMap = useMemo(
    () => ratioOption('AudioCompletionRatio'),
    [options && options.AudioCompletionRatio],
  );
  const modelPriceMap = useMemo(
    () => ratioOption('ModelPrice'),
    [options && options.ModelPrice],
  );

  // 某模型的**常规目录价**（¥）：既是展示基准，也是折算系数与「重置」的依据。
  const basePriceOf = useCallback(
    (pattern) => {
      const m = Number(modelRatioMap[pattern]) || 0;
      const rel = (map) => {
        const v = Number(map[pattern]);
        return Number.isFinite(v) && v > 0 ? v : null;
      };
      const c = rel(completionRatioMap);
      const a = rel(audioRatioMap);
      const ac = rel(audioCompletionRatioMap);
      const price = Number(modelPriceMap[pattern]) || 0;
      return {
        input: m > 0 ? m : null,
        output: m > 0 && c ? m * c : null,
        cache: m > 0 && rel(cacheRatioMap) ? m * rel(cacheRatioMap) : null,
        create_cache:
          m > 0 && rel(createCacheRatioMap)
            ? m * rel(createCacheRatioMap)
            : null,
        image: m > 0 && rel(imageRatioMap) ? m * rel(imageRatioMap) : null,
        audio_input: m > 0 && a ? m * a : null,
        audio_output: m > 0 && a && ac ? m * a * ac : null,
        per_call: price > 0 ? price : null,
      };
    },
    [
      modelRatioMap,
      completionRatioMap,
      cacheRatioMap,
      createCacheRatioMap,
      imageRatioMap,
      audioRatioMap,
      audioCompletionRatioMap,
      modelPriceMap,
    ],
  );

  const globalRules = useMemo(() => cfg.rules[GLOBAL_SCOPE] || {}, [cfg]);
  const models = useMemo(
    () => Object.keys(globalRules).sort((a, b) => a.localeCompare(b)),
    [globalRules],
  );

  useEffect(() => {
    if (models.length === 0) {
      setSelectedModel('');
      return;
    }
    if (!selectedModel || !globalRules[selectedModel]) {
      setSelectedModel(models[0]);
    }
  }, [models, globalRules, selectedModel]);

  const legacyScopes = useMemo(
    () =>
      Object.keys(cfg.rules).filter(
        (scope) =>
          scope !== GLOBAL_SCOPE &&
          Object.keys(cfg.rules[scope] || {}).length > 0,
      ),
    [cfg],
  );

  const emitRules = (nextGlobalRules) => {
    const nextRules = { ...cfg.rules };
    if (Object.keys(nextGlobalRules).length === 0) {
      delete nextRules[GLOBAL_SCOPE];
    } else {
      nextRules[GLOBAL_SCOPE] = nextGlobalRules;
    }
    setDraft(
      JSON.stringify({ windows: cfg.windows, rules: nextRules }, null, 2),
    );
  };

  const setModelRules = (model, list) => {
    const next = { ...globalRules };
    if (!list || list.length === 0) {
      delete next[model];
    } else {
      next[model] = list;
    }
    emitRules(next);
  };

  const patchRule = (model, idx, updater) => {
    const list = (globalRules[model] || []).map((r, i) =>
      i === idx ? updater(r) : r,
    );
    setModelRules(model, list);
  };

  // 单个分量改价：留空 = 该分量不随时段变（从 prices 里删掉这个键）
  const setPrice = (model, idx, comp, value) => {
    patchRule(model, idx, (rule) => {
      const prices = { ...(rule.prices || {}) };
      if (value === null || !(value > 0)) {
        delete prices[comp];
      } else {
        prices[comp] = Number(value.toFixed(4));
      }
      const next = { ...rule, prices };
      delete next.value;
      delete next.output_factor;
      delete next.factors;
      return next;
    });
  };

  // 按比例调整这一档（上游调价后手动跟一下），作用于**已填**的分量
  const scaleRule = (model, idx, factor) => {
    patchRule(model, idx, (rule) => {
      const prices = {};
      Object.entries(rule.prices || {}).forEach(([k, v]) => {
        const n = Number(v) * factor;
        if (n > 0) prices[k] = Number(n.toFixed(4));
      });
      return { ...rule, prices };
    });
  };

  const resetRule = (model, idx) => {
    const base = basePriceOf(model);
    patchRule(model, idx, (rule) => {
      const prices = {};
      Object.entries(base).forEach(([k, v]) => {
        if (Number(v) > 0) prices[k] = Number(Number(v).toFixed(4));
      });
      return { ...rule, prices };
    });
  };

  const removeRule = (model, idx) => {
    const list = (globalRules[model] || []).filter((_, i) => i !== idx);
    setModelRules(model, list);
  };

  const addRule = (model, windowKey) => {
    if (!windowKey) return;
    const list = [...(globalRules[model] || [])];
    if (list.some((r) => r.window === windowKey)) {
      showWarning(t('该时段已经配过了'));
      return;
    }
    const base = basePriceOf(model);
    const prices = {};
    if (Number(base.input) > 0) {
      prices.input = Number(Number(base.input).toFixed(4));
    }
    list.push({ window: windowKey, prices });
    setModelRules(model, list);
  };

  const slotSummary = (windowKey) => {
    const win = cfg.windows[windowKey];
    if (!win) return windowKey;
    if (win.exclude_of) {
      const ref = cfg.windows[win.exclude_of];
      return t('其余时间（除「{{name}}」以外）', {
        name: (ref && ref.label) || win.exclude_of,
      });
    }
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

  const onSubmit = async () => {
    if (draft === raw) {
      showWarning(t('你似乎并没有修改什么'));
      return;
    }
    setSaving(true);
    try {
      const res = await API.put('/api/option/', {
        key: 'GroupTimeRatio',
        value: draft,
      });
      if (!res || !res.data || !res.data.success) {
        showError(
          (res && res.data && res.data.message) || t('保存失败，请重试'),
        );
        return;
      }
      showSuccess(t('保存成功'));
      refresh && refresh();
    } catch (e) {
      showError(t('保存失败，请重试'));
    } finally {
      setSaving(false);
    }
  };

  const migrateLegacy = () => {
    const merged = { ...globalRules };
    legacyScopes.forEach((scope) => {
      Object.entries(cfg.rules[scope] || {}).forEach(([model, list]) => {
        merged[model] = [...(merged[model] || []), ...(list || [])];
      });
    });
    const nextRules = { [GLOBAL_SCOPE]: merged };
    setDraft(
      JSON.stringify({ windows: cfg.windows, rules: nextRules }, null, 2),
    );
  };

  const base = selectedModel ? basePriceOf(selectedModel) : {};
  const rules = selectedModel ? globalRules[selectedModel] || [] : [];
  const usedWindows = new Set(rules.map((r) => r.window));
  const availableWindows = Object.keys(cfg.windows).filter(
    (k) => !usedWindows.has(k),
  );
  const componentList = COMPONENTS.filter((c) => Number(base[c.key]) > 0);

  return (
    <Card
      className='!rounded-2xl'
      style={{ marginTop: 16 }}
      title={
        <div>
          <Text strong>{t('分时价')}</Text>
          <Text type='tertiary' size='small' style={{ display: 'block' }}>
            {t(
              '每个时段填该时段的目录价（¥）。对全网线路生效；折扣按用户档计算、不随时段变化。常规目录价变更不会自动改这里，需要时点「按比例调整」。',
            )}
          </Text>
        </div>
      }
      headerExtraContent={
        <Button
          icon={<IconSave />}
          theme='solid'
          loading={saving}
          onClick={onSubmit}
        >
          {t('保存分时价')}
        </Button>
      }
    >
      <TimeSlotEditor value={draft} onChange={setDraft} />

      {legacyScopes.length > 0 && (
        <Banner
          type='warning'
          closeIcon={null}
          className='mt-3'
          description={
            <Space vertical align='start'>
              <Text>
                {t('检测到按线路配置的旧规则（')}
                {legacyScopes.join('、')}
                {t('）。它们优先于全网规则，建议合并到全网。')}
              </Text>
              <Popconfirm
                title={t('把按线路配的时段价变成全网规则？')}
                content={t(
                  '合并后这些规则对所有线路生效。同一模型同一时段若两边都有，会变成两条重复规则，请合并后核对。',
                )}
                okText={t('确认合并')}
                onConfirm={migrateLegacy}
              >
                <Button size='small'>{t('合并到全网')}</Button>
              </Popconfirm>
            </Space>
          }
        />
      )}

      <div
        className='grid grid-cols-1 xl:grid-cols-2 gap-4 mt-4'
        style={{
          borderTop: '1px solid var(--semi-color-border)',
          paddingTop: 16,
        }}
      >
        <div>
          <Text type='tertiary' size='small'>
            {t('已配置的模型（{{n}}）', { n: models.length })}
          </Text>
          <Space vertical align='start' spacing={4} className='mt-2'>
            {models.map((model) => (
              <div
                key={model}
                className={
                  'w-full flex items-center justify-between px-3 py-2 rounded-lg cursor-pointer ' +
                  (model === selectedModel
                    ? 'bg-[var(--semi-color-primary-light-default)]'
                    : '')
                }
                onClick={() => setSelectedModel(model)}
              >
                <Text strong={model === selectedModel}>{model}</Text>
                <Text type='tertiary' size='small'>
                  {t('{{n}} 个时段', { n: (globalRules[model] || []).length })}
                </Text>
              </div>
            ))}
          </Space>
          <Space className='mt-2'>
            {/* 可搜的模型下拉：从「模型定价」里已有的模型里挑，省得手打错名字；
                也允许直接输入通配符（如 deepseek-*），所以旁边仍保留自由输入框。 */}
            <Select
              size='small'
              style={{ width: 200 }}
              filter
              placeholder={t('搜索模型')}
              value={undefined}
              optionList={Object.keys(modelRatioMap)
                .sort((a, b) => a.localeCompare(b))
                .map((m) => ({ value: m, label: m }))}
              onChange={(v) => setNewModel(String(v || ''))}
            />
            <Input
              size='small'
              style={{ width: 150 }}
              value={newModel}
              placeholder={t('模型名或 deepseek-*')}
              onChange={setNewModel}
            />
            <Button
              size='small'
              icon={<IconPlus />}
              onClick={() => {
                const name = newModel.trim();
                if (!name) {
                  showWarning(t('请填写模型名或模式串'));
                  return;
                }
                if (globalRules[name]) {
                  showWarning(t('该模型已经配过了'));
                  return;
                }
                setModelRules(name, []);
                setSelectedModel(name);
                setNewModel('');
              }}
            >
              {t('添加模型')}
            </Button>
          </Space>
        </div>

        <div>
          {!selectedModel ? (
            <Empty description={t('左边选一个模型，或先添加模型')} />
          ) : (
            <div>
              <div className='rounded-lg bg-[var(--semi-color-fill-0)] px-3 py-2 mb-3'>
                <Text size='small' type='tertiary'>
                  {t('常规目录价（模型定价页）')}
                </Text>
                <div className='mt-1'>
                  {componentList.length === 0 ? (
                    <Text type='warning' size='small'>
                      {t('该模型没配常规目录价，请先在「模型定价」页配置')}
                    </Text>
                  ) : (
                    <Space wrap>
                      {componentList.map((c) => (
                        <Text key={c.key} size='small'>
                          {t(c.label)} ¥{Number(base[c.key]).toFixed(4)}
                        </Text>
                      ))}
                    </Space>
                  )}
                </div>
              </div>

              {rules.map((rule, idx) => (
                <div
                  key={rule.window + '-' + idx}
                  className='rounded-xl border px-3 py-3 mb-3'
                  style={{ borderColor: 'var(--semi-color-border)' }}
                >
                  <div className='flex items-center justify-between mb-2'>
                    <Space wrap>
                      <Tag color='cyan' shape='circle' size='small'>
                        {(cfg.windows[rule.window] &&
                          cfg.windows[rule.window].label) ||
                          rule.window}
                      </Tag>
                      <Text type='tertiary' size='small'>
                        {slotSummary(rule.window)}
                      </Text>
                    </Space>
                    <Space>
                      <Button
                        size='small'
                        onClick={() => scaleRule(selectedModel, idx, 0.5)}
                      >
                        ×0.5
                      </Button>
                      <Button
                        size='small'
                        onClick={() => scaleRule(selectedModel, idx, 1.2)}
                      >
                        ×1.2
                      </Button>
                      <Button
                        size='small'
                        onClick={() => resetRule(selectedModel, idx)}
                      >
                        {t('重置为常规价')}
                      </Button>
                      <Popconfirm
                        title={t('确认删除该时段价？')}
                        onConfirm={() => removeRule(selectedModel, idx)}
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

                  <div className='grid grid-cols-2 xl:grid-cols-3 gap-x-4 gap-y-2'>
                    {componentList.map((c) => {
                      const value = Number((rule.prices || {})[c.key]);
                      const factor =
                        value > 0 && Number(base[c.key]) > 0
                          ? value / Number(base[c.key])
                          : null;
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
                              style={{ width: 118 }}
                              value={value > 0 ? value : undefined}
                              placeholder={t('不变')}
                              onChange={(v) =>
                                setPrice(
                                  selectedModel,
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
              ))}

              <Space>
                <Select
                  size='small'
                  style={{ width: 220 }}
                  placeholder={t('添加时段')}
                  value={undefined}
                  optionList={availableWindows.map((k) => ({
                    value: k,
                    label: (cfg.windows[k] && cfg.windows[k].label) || k,
                  }))}
                  onChange={(v) => addRule(selectedModel, v)}
                />
                <Text type='tertiary' size='small'>
                  {t('留空 = 该分量不随时段变')}
                </Text>
              </Space>
            </div>
          )}
        </div>
      </div>
    </Card>
  );
};

export default TimePricePanel;
