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
    if (!(addFactor > 0 && addFactor <= MAX_FACTOR)) {
      showWarning(t('系数必须在 (0, 20] 之间'));
      return;
    }
    if (
      addOutFactor !== null &&
      !(addOutFactor > 0 && addOutFactor <= MAX_FACTOR)
    ) {
      showWarning(t('输出系数必须在 (0, 20] 之间'));
      return;
    }
    const list = (globalRules[pattern] || []).slice();
    const idx = list.findIndex((r) => r.window === addWindow);
    // 输出侧留空（null）= 跟随输入，不写字段，保持 JSON 干净
    const extra =
      addOutFactor && addOutFactor > 0 ? { output_factor: addOutFactor } : {};
    if (idx >= 0) {
      const merged = Object.assign({}, list[idx], { value: addFactor }, extra);
      if (!extra.output_factor) delete merged.output_factor;
      list[idx] = merged;
    } else {
      list.push(Object.assign({ window: addWindow, value: addFactor }, extra));
    }
    setGlobalList(pattern, list);
    setAddPattern('');
    setAddWindow(null);
    setAddOutFactor(null);
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
      title: t('系数（输入 / 输出）'),
      dataIndex: 'value',
      width: 200,
      render: (value, record) => (
        <Space>
          <InputNumber
            size='small'
            min={0.01}
            max={MAX_FACTOR}
            step={0.05}
            value={value}
            style={{ width: 80 }}
            onChange={(v) =>
              updateRowFactor(record, typeof v === 'number' ? v : 1)
            }
          />
          {/*
            输出侧系数：留空 = 跟随输入。分时段价目不一定等比例变化
            （忙时输入 2/输出 8、闲时输入 1/输出 7），这里必须能分开填。
          */}
          <InputNumber
            size='small'
            min={0.01}
            max={MAX_FACTOR}
            step={0.05}
            value={record.outValue}
            placeholder={t('同输入')}
            style={{ width: 80 }}
            onChange={(v) =>
              updateRowOutputFactor(record, typeof v === 'number' ? v : null)
            }
          />
        </Space>
      ),
    },
    {
      title: t('该时段目录价（¥/1M）'),
      dataIndex: 'inPrice',
      render: (inPrice, record) =>
        inPrice === null ? (
          <Text type='tertiary' size='small'>
            {t('该模型未配目录价')}
          </Text>
        ) : (
          <Space vertical align='start' spacing={2}>
            <Text strong>
              {t('输入')} {Number(inPrice.toFixed(4))}
            </Text>
            <Text strong>
              {t('输出')}{' '}
              {record.outPrice === null
                ? '—'
                : Number(record.outPrice.toFixed(4))}
            </Text>
            {record.sameFactor ? null : (
              <Tag color='violet' shape='circle'>
                {t('输入输出不同比例')}
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
        <InputNumber
          size='small'
          min={0.01}
          max={MAX_FACTOR}
          step={0.05}
          value={addFactor}
          style={{ width: 100 }}
          prefix={t('输入')}
          onChange={(v) => setAddFactor(typeof v === 'number' ? v : 1)}
        />
        {/* 输出侧系数留空 = 跟随输入；输入输出不同比例时才需要填 */}
        <InputNumber
          size='small'
          min={0.01}
          max={MAX_FACTOR}
          step={0.05}
          value={addOutFactor}
          style={{ width: 120 }}
          prefix={t('输出')}
          placeholder={t('同输入')}
          onChange={(v) => setAddOutFactor(typeof v === 'number' ? v : null)}
        />
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
