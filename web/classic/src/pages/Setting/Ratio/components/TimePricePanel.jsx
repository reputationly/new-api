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
  Table,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { IconDelete, IconPlus, IconSave } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess, showWarning } from '../../../../helpers';
import TimeWindowEditor from '../../../GroupManagement/components/TimeWindowEditor';

const { Text } = Typography;

// 全网作用域键。与后端 setting/ratio_setting 的保留键一致：
// 规则挂在这个 key 下 = 对所有线路生效；按线路名存的是存量配置，解析时优先于全网。
const GLOBAL_SCOPE = '*';

const MAX_FACTOR = 20;

const parseConfig = (raw) => {
  try {
    const parsed = JSON.parse(raw || '{}') || {};
    return { windows: parsed.windows || {}, rules: parsed.rules || {} };
  } catch (e) {
    return { windows: {}, rules: {} };
  }
};

const clockMinutes = (s) => {
  const m = /^(\d{1,2}):(\d{2})$/.exec(String(s == null ? '' : s).trim());
  return m ? Number(m[1]) * 60 + Number(m[2]) : null;
};

/** 窗口此刻是否生效（按浏览器时区，仅供管理端预览；计费侧一律由后端判定）。 */
const windowActiveNow = (win, now) => {
  if (!win) return false;
  const start = clockMinutes(win.start);
  const end = clockMinutes(win.end);
  if (start === null || end === null || start === end) return false;
  const days = Array.isArray(win.days) && win.days.length ? win.days : null;
  const cur = now.getHours() * 60 + now.getMinutes();
  const day = now.getDay();
  if (start < end) {
    return days
      ? days.includes(day) && cur >= start && cur < end
      : cur >= start && cur < end;
  }
  // 跨午夜按起始日判定，与后端 ActiveAt 同一读法
  if (cur >= start) return days ? days.includes(day) : true;
  if (cur < end) return days ? days.includes((day + 6) % 7) : true;
  return false;
};

/**
 * 分时价编辑器（模型定价页）。
 *
 * 时段价是**价目**的一部分：系数乘在常规价（目录价 × 线路基准 × 模型定价）上，
 * 小于 1 是空闲时段更便宜、大于 1 是紧缺时段更贵；对全网线路生效，与客户折扣无关
 * （折扣只看用户档，永远不随时段变）。设计见 docs/time-price-axis-design.md §6。
 */
export default function TimePricePanel({ options, refresh }) {
  const { t } = useTranslation();
  const [raw, setRaw] = useState((options && options.GroupTimeRatio) || '{}');
  const [originRaw, setOriginRaw] = useState(
    (options && options.GroupTimeRatio) || '{}',
  );
  const [saving, setSaving] = useState(false);
  const [addPattern, setAddPattern] = useState('');
  const [addWindow, setAddWindow] = useState(null);
  const [addFactor, setAddFactor] = useState(1.1111);
  const [addOutFactor, setAddOutFactor] = useState(null); // 空 = 跟随输入系数
  // 按目录价填写时的两个输入（¥/1M）；模式串是通配时没有单一目录价，退回填系数
  const [addInPrice, setAddInPrice] = useState(null);
  const [addOutPrice, setAddOutPrice] = useState(null);

  useEffect(() => {
    const next = (options && options.GroupTimeRatio) || '{}';
    setRaw(next);
    setOriginRaw(next);
  }, [options && options.GroupTimeRatio]);

  const cfg = useMemo(() => parseConfig(raw), [raw]);
  const modelRatioMap = useMemo(() => {
    try {
      return JSON.parse((options && options.ModelRatio) || '{}') || {};
    } catch (e) {
      return {};
    }
  }, [options && options.ModelRatio]);

  // 输出目录价 = 输入价 × 补全倍率，算折算列要用
  const completionRatioMap = useMemo(() => {
    try {
      return JSON.parse((options && options.CompletionRatio) || '{}') || {};
    } catch (e) {
      return {};
    }
  }, [options && options.CompletionRatio]);

  const windowOptions = useMemo(
    () =>
      Object.entries(cfg.windows).map(([key, win]) => ({
        label: (win && win.label) || key,
        value: key,
      })),
    [cfg.windows],
  );

  const globalRules = cfg.rules[GLOBAL_SCOPE] || {};
  const legacyScopes = useMemo(
    () =>
      Object.keys(cfg.rules).filter(
        (k) =>
          k !== GLOBAL_SCOPE &&
          cfg.rules[k] &&
          Object.keys(cfg.rules[k]).length,
      ),
    [cfg.rules],
  );

  const emit = useCallback((nextCfg) => {
    setRaw(JSON.stringify(nextCfg, null, 2));
  }, []);

  const setRulesForScope = useCallback(
    (scope, rules) => {
      const nextRules = Object.assign({}, cfg.rules);
      if (!rules || Object.keys(rules).length === 0) {
        delete nextRules[scope];
      } else {
        nextRules[scope] = rules;
      }
      emit({ windows: cfg.windows, rules: nextRules });
    },
    [cfg, emit],
  );

  const setGlobalList = useCallback(
    (pattern, list) => {
      const next = Object.assign({}, globalRules);
      if (!list || list.length === 0) {
        delete next[pattern];
      } else {
        next[pattern] = list;
      }
      setRulesForScope(GLOBAL_SCOPE, next);
    },
    [globalRules, setRulesForScope],
  );

  // 把按线路存的旧规则并进全网：同模型同时段冲突时按全网为准，并在提示里点名。
  const migrateLegacy = useCallback(() => {
    const merged = Object.assign({}, globalRules);
    const conflicts = [];
    legacyScopes.forEach((scope) => {
      Object.entries(cfg.rules[scope] || {}).forEach(([pattern, list]) => {
        const base = (merged[pattern] || []).slice();
        (list || []).forEach((rule) => {
          const idx = base.findIndex((r) => r.window === rule.window);
          if (idx >= 0) {
            if (Number(base[idx].value) !== Number(rule.value)) {
              conflicts.push(pattern + '/' + rule.window);
            }
            return;
          }
          base.push(rule);
        });
        merged[pattern] = base;
      });
    });
    const nextRules = {};
    nextRules[GLOBAL_SCOPE] = merged;
    emit({ windows: cfg.windows, rules: nextRules });
    if (conflicts.length) {
      showWarning(t('已合并，冲突项按全网规则为准：') + conflicts.join('、'));
    }
  }, [cfg, emit, globalRules, legacyScopes, t]);
  const windowUsage = useMemo(() => {
    const usage = {};
    Object.values(cfg.rules).forEach((scopeRules) => {
      Object.values(scopeRules || {}).forEach((list) => {
        (list || []).forEach((r) => {
          if (r && r.window) usage[r.window] = (usage[r.window] || 0) + 1;
        });
      });
    });
    return usage;
  }, [cfg.rules]);

  const rows = useMemo(() => {
    const out = [];
    Object.entries(globalRules).forEach(([pattern, list]) => {
      const dir = Number(modelRatioMap[pattern]);
      const cr = Number(completionRatioMap[pattern]);
      (list || []).forEach((rule, idx) => {
        const inFactor = Number(rule.value);
        // output_factor 缺省 = 跟随输入（旧配置与「整价统一变」的场景都走这条）
        const outFactor =
          rule.output_factor && Number(rule.output_factor) > 0
            ? Number(rule.output_factor)
            : inFactor;
        out.push({
          key: pattern + '|' + rule.window + '|' + idx,
          pattern,
          window: rule.window,
          value: inFactor,
          outValue: outFactor,
          sameFactor: outFactor === inFactor,
          remark: rule.remark || '',
          // 基准目录价（常规时段的价格）：有它才能让运营直接填 ¥，而不是填系数
          baseIn: Number.isFinite(dir) && dir > 0 ? dir : null,
          baseOut:
            Number.isFinite(dir) && dir > 0 && Number.isFinite(cr) && cr > 0
              ? dir * cr
              : null,
          inPrice: Number.isFinite(dir) && dir > 0 ? dir * inFactor : null,
          outPrice:
            Number.isFinite(dir) && dir > 0 && Number.isFinite(cr) && cr > 0
              ? dir * cr * outFactor
              : null,
          listIndex: idx,
        });
      });
    });
    return out.sort((a, b) =>
      a.pattern === b.pattern
        ? String(a.window).localeCompare(String(b.window))
        : a.pattern.localeCompare(b.pattern),
    );
  }, [globalRules, modelRatioMap, completionRatioMap]);

  const updateRowFactor = (row, value) => {
    const list = (globalRules[row.pattern] || []).map((r, i) =>
      i === row.listIndex ? Object.assign({}, r, { value }) : r,
    );
    setGlobalList(row.pattern, list);
  };

  // 输出侧系数：null = 回到"跟随输入"（写入时删掉该字段，保持 JSON 干净）
  const updateRowOutputFactor = (row, outValue) => {
    const list = (globalRules[row.pattern] || []).map((r, i) => {
      if (i !== row.listIndex) return r;
      if (outValue === null || !(outValue > 0)) {
        const next = Object.assign({}, r);
        delete next.output_factor;
        return next;
      }
      return Object.assign({}, r, { output_factor: outValue });
    });
    setGlobalList(row.pattern, list);
  };

  // 运营按「该时段目录价（¥/1M）」填写，这里换算成系数存储：
  // 存系数而不是存绝对价，是为了让时段价随常规目录价一起变（改一次基础价，
  // 所有时段自动跟随），也避免与 ModelRatio 形成两个真值来源。
  const updateRowInPrice = (row, price) => {
    if (!(row.baseIn > 0) || !(price > 0)) return;
    updateRowFactor(row, price / row.baseIn);
  };

  const updateRowOutPrice = (row, price) => {
    if (!(row.baseOut > 0)) return;
    if (price === null || !(price > 0)) {
      updateRowOutputFactor(row, null);
      return;
    }
    const f = price / row.baseOut;
    // 与输入系数相同就不写 output_factor，保持 JSON 干净
    updateRowOutputFactor(row, Math.abs(f - row.value) < 1e-9 ? null : f);
  };

  const updateRowRemark = (row, remark) => {
    const list = (globalRules[row.pattern] || []).map((r, i) =>
      i === row.listIndex ? Object.assign({}, r, { remark }) : r,
    );
    setGlobalList(row.pattern, list);
  };

  const removeRow = (row) => {
    const list = (globalRules[row.pattern] || []).filter(
      (_, i) => i !== row.listIndex,
    );
    setGlobalList(row.pattern, list);
  };

  const addRule = () => {
    const pattern = addPattern.trim();
    if (!pattern) {
      showWarning(t('请填写模型名或模式串'));
      return;
    }
    if (!addWindow) {
      showWarning(t('请选择时段模板'));
      return;
    }
    // 该模式串若对应一个真实模型（有按量目录价），就按 ¥ 换算成系数；
    // 通配模式串没有单一目录价，只能填系数。
    const baseIn = Number(modelRatioMap[pattern]);
    const baseCR = Number(completionRatioMap[pattern]);
    const byPrice = Number.isFinite(baseIn) && baseIn > 0;
    const baseOut =
      byPrice && Number.isFinite(baseCR) && baseCR > 0 ? baseIn * baseCR : 0;

    let inFactor = addFactor;
    let outFactor = addOutFactor;
    if (byPrice) {
      if (!(addInPrice > 0)) {
        showWarning(t('请填写该时段的输入目录价'));
        return;
      }
      inFactor = addInPrice / baseIn;
      outFactor =
        addOutPrice && addOutPrice > 0 && baseOut > 0
          ? addOutPrice / baseOut
          : null;
      if (Math.abs((outFactor ?? inFactor) - inFactor) < 1e-9) outFactor = null;
    }

    if (!(inFactor > 0 && inFactor <= MAX_FACTOR)) {
      showWarning(t('折算后的输入系数超出 (0, 20]，请检查目录价'));
      return;
    }
    if (outFactor !== null && !(outFactor > 0 && outFactor <= MAX_FACTOR)) {
      showWarning(t('折算后的输出系数超出 (0, 20]，请检查目录价'));
      return;
    }
    const list = (globalRules[pattern] || []).slice();
    const idx = list.findIndex((r) => r.window === addWindow);
    // 输出侧留空（null）= 跟随输入，不写字段，保持 JSON 干净
    const extra =
      outFactor && outFactor > 0 ? { output_factor: outFactor } : {};
    if (idx >= 0) {
      const merged = Object.assign({}, list[idx], { value: inFactor }, extra);
      if (!extra.output_factor) delete merged.output_factor;
      list[idx] = merged;
    } else {
      list.push(Object.assign({ window: addWindow, value: inFactor }, extra));
    }
    setGlobalList(pattern, list);
    setAddPattern('');
    setAddWindow(null);
    setAddOutFactor(null);
    setAddInPrice(null);
    setAddOutPrice(null);
  };

  const onSubmit = async () => {
    if (raw === originRaw) {
      showWarning(t('你似乎并没有修改什么'));
      return;
    }
    setSaving(true);
    try {
      const res = await API.put('/api/option/', {
        key: 'GroupTimeRatio',
        value: raw,
      });
      if (!res || !res.data || !res.data.success) {
        showError(
          (res && res.data && res.data.message) || t('保存失败，请重试'),
        );
        return;
      }
      showSuccess(t('保存成功'));
      setOriginRaw(raw);
      if (refresh) refresh();
    } catch (e) {
      showError(t('保存失败，请重试'));
    } finally {
      setSaving(false);
    }
  };

  const columns = [
    {
      title: t('模型'),
      dataIndex: 'pattern',
      render: (text) => <Text strong>{text}</Text>,
    },
    {
      title: t('时段'),
      dataIndex: 'window',
      render: (key) => {
        const win = cfg.windows[key];
        const active = windowActiveNow(win, new Date());
        return (
          <Space>
            <Tag color='cyan' shape='circle'>
              {(win && win.label) || key}
            </Tag>
            {win && (
              <Text type='tertiary' size='small'>
                {String(win.start || '')}-{String(win.end || '')}
              </Text>
            )}
            {active && (
              <Tag color='green' shape='circle'>
                {t('此刻生效')}
              </Tag>
            )}
          </Space>
        );
      },
    },
    {
      // 运营填的是**目录价**（¥/1M），不是系数：系数由这里换算并存库，
      // 界面上只把折算结果作为参考显示在下一列。
      title: t('该时段目录价（¥/1M）'),
      dataIndex: 'inPrice',
      width: 230,
      render: (inPrice, record) => {
        if (!(record.baseIn > 0)) {
          // 该模型没有配置按量目录价（按次/未定价）：退回填系数，避免填了个 ¥ 却算不出来
          return (
            <Space>
              <InputNumber
                size='small'
                min={0.01}
                max={MAX_FACTOR}
                step={0.05}
                prefix={t('×')}
                value={record.value}
                style={{ width: 92 }}
                onChange={(v) =>
                  updateRowFactor(record, typeof v === 'number' ? v : 1)
                }
              />
              <InputNumber
                size='small'
                min={0.01}
                max={MAX_FACTOR}
                step={0.05}
                prefix={t('×')}
                value={record.outValue}
                placeholder={t('同输入')}
                style={{ width: 92 }}
                onChange={(v) =>
                  updateRowOutputFactor(
                    record,
                    typeof v === 'number' ? v : null,
                  )
                }
              />
            </Space>
          );
        }
        return (
          <Space>
            <InputNumber
              size='small'
              min={0.0001}
              max={Number((record.baseIn * MAX_FACTOR).toFixed(4))}
              step={0.1}
              precision={4}
              prefix={t('输入')}
              value={Number(Number(inPrice).toFixed(4))}
              style={{ width: 108 }}
              onChange={(v) =>
                updateRowInPrice(record, typeof v === 'number' ? v : null)
              }
            />
            <InputNumber
              size='small'
              min={0.0001}
              max={Number((record.baseOut * MAX_FACTOR).toFixed(4))}
              step={0.1}
              precision={4}
              prefix={t('输出')}
              value={
                record.outPrice === null
                  ? null
                  : Number(Number(record.outPrice).toFixed(4))
              }
              placeholder={t('同输入')}
              style={{ width: 108 }}
              onChange={(v) =>
                updateRowOutPrice(record, typeof v === 'number' ? v : null)
              }
            />
          </Space>
        );
      },
    },
    {
      title: t('折算系数'),
      dataIndex: 'value',
      width: 150,
      render: (value, record) => (
        <Space vertical align='start' spacing={2}>
          <Text size='small'>
            {t('输入')} ×{Number(Number(value).toFixed(3))}
          </Text>
          <Text size='small'>
            {t('输出')} ×{Number(Number(record.outValue).toFixed(3))}
          </Text>
          {record.sameFactor ? null : (
            <Tag color='violet' shape='circle'>
              {t('不等比例')}
            </Tag>
          )}
        </Space>
      ),
    },
    {
      title: t('备注'),
      dataIndex: 'remark',
      render: (text, record) => (
        <Input
          size='small'
          value={text}
          placeholder={t('为什么是这个价')}
          onChange={(v) => updateRowRemark(record, v)}
        />
      ),
    },
    {
      title: '',
      key: 'action',
      width: 50,
      render: (_, record) => (
        <Popconfirm
          title={t('确认删除该时段价？')}
          onConfirm={() => removeRow(record)}
        >
          <Button
            icon={<IconDelete />}
            type='danger'
            theme='borderless'
            size='small'
          />
        </Popconfirm>
      ),
    },
  ];

  return (
    <Card
      className='!rounded-2xl'
      style={{ marginTop: 16 }}
      title={
        <div>
          <Text strong>{t('分时价')}</Text>
          <Text type='tertiary' size='small' style={{ display: 'block' }}>
            {t(
              '系数乘在常规价上：小于 1 是空闲时段更便宜，大于 1 是紧缺时段更贵。对全网线路生效，与客户折扣无关（折扣按用户档计算，不随时段变化）。',
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
      <TimeWindowEditor
        value={raw}
        onChange={(v) => setRaw(v)}
        usage={windowUsage}
      />

      {legacyScopes.length > 0 && (
        <Banner
          type='warning'
          closeIcon={null}
          className='mt-2'
          style={{ marginBottom: 8 }}
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
                  '合并后这条规则对所有线路生效，原来只在那条线路上生效。值按原样搬过去，不做语义换算——若这些规则还是旧口径（取代模型折扣），请先按更新说明重配，不要直接合并。',
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

      <Space wrap className='mt-3 mb-2'>
        <Input
          size='small'
          style={{ width: 240 }}
          placeholder={t('模型名或模式串（如 deepseek-*）')}
          value={addPattern}
          onChange={setAddPattern}
        />
        <Select
          size='small'
          style={{ width: 180 }}
          placeholder={t('选择时段')}
          value={addWindow}
          optionList={windowOptions}
          onChange={setAddWindow}
        />
        {/*
          按**目录价**填写（¥/1M）。该模式串没有单一目录价时（通配符/未定价模型）
          退回填系数——否则填了 ¥ 也无从换算。
        */}
        {Number(modelRatioMap[addPattern.trim()]) > 0 ? (
          <>
            <InputNumber
              size='small'
              min={0.0001}
              step={0.1}
              precision={4}
              value={addInPrice}
              style={{ width: 128 }}
              prefix={t('输入')}
              placeholder={t('¥/1M')}
              onChange={(v) => setAddInPrice(typeof v === 'number' ? v : null)}
            />
            <InputNumber
              size='small'
              min={0.0001}
              step={0.1}
              precision={4}
              value={addOutPrice}
              style={{ width: 128 }}
              prefix={t('输出')}
              placeholder={t('同输入')}
              onChange={(v) => setAddOutPrice(typeof v === 'number' ? v : null)}
            />
          </>
        ) : (
          <>
            <InputNumber
              size='small'
              min={0.01}
              max={MAX_FACTOR}
              step={0.05}
              value={addFactor}
              style={{ width: 100 }}
              prefix={t('×')}
              onChange={(v) => setAddFactor(typeof v === 'number' ? v : 1)}
            />
            <InputNumber
              size='small'
              min={0.01}
              max={MAX_FACTOR}
              step={0.05}
              value={addOutFactor}
              style={{ width: 120 }}
              prefix={t('×')}
              placeholder={t('同输入')}
              onChange={(v) =>
                setAddOutFactor(typeof v === 'number' ? v : null)
              }
            />
          </>
        )}
        <Button
          size='small'
          theme='solid'
          icon={<IconPlus />}
          onClick={addRule}
        >
          {t('添加时段价')}
        </Button>
      </Space>

      {rows.length === 0 ? (
        <Empty description={t('还没有分时价：价格全天一个数')} />
      ) : (
        <Table
          size='small'
          bordered={false}
          pagination={false}
          dataSource={rows}
          columns={columns}
          className='!rounded-lg'
        />
      )}
    </Card>
  );
}
