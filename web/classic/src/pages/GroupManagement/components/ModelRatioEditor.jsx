import React, {
  useState,
  useCallback,
  useMemo,
  useRef,
  useEffect,
} from 'react';
import {
  Banner,
  Button,
  Checkbox,
  Empty,
  Input,
  InputNumber,
  Popconfirm,
  Select,
  Tag,
  Typography,
} from '@douyinfe/semi-ui';
import { IconDelete, IconPlus, IconSearch } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import { API } from '../../../helpers';
import CardTable from '../../../components/common/ui/CardTable';

const { Text } = Typography;

let _idCounter = 0;
const uid = () => `gmr_${++_idCounter}`;

const MODE_MULTIPLY = 'multiply';
const MODE_OVERRIDE = 'override';

function parseJSON(str) {
  if (!str || !str.trim()) return {};
  try {
    return JSON.parse(str);
  } catch {
    return {};
  }
}

const isWildcard = (pattern) => (pattern || '').endsWith('*');

/**
 * 模式下拉的选项。
 *
 * 档位折扣禁用 override（设计 §4）：它的语义是「打折」，一旦允许绝对值就会吃掉
 * Layer 0/1/2 承载的全部成本信息，在成本高的模型上直接亏损且日志上看不出来。
 * 下拉里直接不给这个选项，比保存时才报错早一步。
 *
 * 导出成纯函数而不是内联进组件：隔着 Semi 的 portal 断言下拉选项极易写出恒真的
 * 假测试（本文件配套用例的第一版就是——点击没真正展开，queryByText 恒为 null，
 * 变异验证时把 allowOverride 整个忽略掉测试依然全绿）。
 */
export function buildModeOptions(allowOverride, t) {
  const multiply = { label: t('折扣 ×'), value: MODE_MULTIPLY };
  if (!allowOverride) {
    return [multiply];
  }
  return [multiply, { label: t('定价 ='), value: MODE_OVERRIDE }];
}

/**
 * 把 GroupModelRatio 的嵌套 JSON 摊平成某个分组下的规则行。
 *
 * 兼容裸数字写法（{"GLM-5": 0.5}）——后端也认它，编辑器不能把手工写的配置吃掉。
 */
function flattenRules(nested, group) {
  const rules = nested[group];
  if (!rules || typeof rules !== 'object') return [];
  return Object.entries(rules).map(([pattern, rule]) => {
    if (typeof rule === 'number') {
      return {
        _id: uid(),
        pattern,
        mode: MODE_MULTIPLY,
        value: rule,
        remark: '',
      };
    }
    return {
      _id: uid(),
      pattern,
      mode: rule?.mode === MODE_OVERRIDE ? MODE_OVERRIDE : MODE_MULTIPLY,
      value: typeof rule?.value === 'number' ? rule.value : 1,
      remark: rule?.remark || '',
    };
  });
}

/** 把某分组的规则行写回整份 GroupModelRatio JSON，其他分组原样保留。 */
function serializeRules(nested, group, rows) {
  const next = { ...nested };
  const groupRules = {};
  rows.forEach((row) => {
    const pattern = (row.pattern || '').trim();
    if (!pattern) return;
    groupRules[pattern] = {
      mode: row.mode,
      value: row.value,
      ...(row.remark ? { remark: row.remark } : {}),
    };
  });
  if (Object.keys(groupRules).length === 0) {
    delete next[group];
  } else {
    next[group] = groupRules;
  }
  return JSON.stringify(next, null, 2);
}

/**
 * 分组内按模型的折扣编辑器。
 *
 * 数据是一次性全量拉到前端的（option JSON），所以搜索、筛选都在内存里做，
 * 不需要服务端分页接口。
 */
export default function ModelRatioEditor({
  group,
  groupRatio,
  value,
  staleRules = [],
  onChange,
  // 以下三个用于「档位折扣」复用（设计 §8.2）。默认值保持分组折扣的原行为，
  // 调用方不传时与参数化之前逐位相同。
  //
  // modelsEndpoint  分组折扣只列本分组有渠道的模型；档位折扣按用户档索引、
  //                 与供应链无关，必须列全站模型
  // allowOverride   档位折扣只允许 multiply：override 会吃掉 Layer 0/1/2 承载的
  //                 全部成本信息，在成本高的模型上直接亏损且日志上看不出来
  // texts           左轴语义不同（分组 / 用户档），提示文案必须跟着换，
  //                 否则页面会指着用户档说「分组基础倍率」
  modelsEndpoint,
  allowOverride = true,
  texts = {},
}) {
  const { t } = useTranslation();

  const nested = useMemo(() => parseJSON(value), [value]);
  const [rows, setRows] = useState(() => flattenRules(nested, group));
  const [keyword, setKeyword] = useState('');
  const [selected, setSelected] = useState([]);
  const [groupModels, setGroupModels] = useState([]);
  const [batchMode, setBatchMode] = useState(MODE_MULTIPLY);
  const [batchValue, setBatchValue] = useState(0.8);

  // 切分组时整体重建：规则集是按分组隔离的，沿用上一个分组的行会写串
  const prevGroupRef = useRef(group);
  useEffect(() => {
    if (prevGroupRef.current !== group) {
      prevGroupRef.current = group;
      setRows(flattenRules(parseJSON(value), group));
      setSelected([]);
      setKeyword('');
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [group]);

  // 模型下拉只列该分组**实际有渠道覆盖**的模型：给一个本分组根本没有的模型配折扣
  // 是纯废配置，不该让人先配出来、再靠失效提示去发现
  useEffect(() => {
    if (!group) return;
    let cancelled = false;
    const endpoint =
      modelsEndpoint || `/api/group/models?group=${encodeURIComponent(group)}`;
    API.get(endpoint)
      .then((res) => {
        if (cancelled) return;
        if (res.data?.success) {
          setGroupModels(res.data.data || []);
        }
      })
      .catch(() => {});
    return () => {
      cancelled = true;
    };
  }, [group, modelsEndpoint]);

  // 下面三个 ref 都是「渲染期同步、事件回调里读」，**不能**进 useCallback 依赖。
  //
  // 理由与 GroupTable.jsx 里那段注释是同一个：emitAndSet 一旦换身份，
  // updateRow / removeRow 跟着换，columns 的 useMemo 就会在每次敲键时重建，
  // Semi Table 重建单元格导致输入框光标跳到末尾。
  //
  // 而 nested 恰恰每次敲键都会变——它由 parseJSON(value) 算出，value 是父组件
  // 刚被本次 onChange 更新过的那份 JSON。把它写进依赖，等于精确地重现了
  // GroupTable 当初修掉的那个 bug。
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  const nestedRef = useRef(nested);
  nestedRef.current = nested;
  const groupNameRef = useRef(group);
  groupNameRef.current = group;

  const emitAndSet = useCallback((updater) => {
    setRows((prev) => {
      const next = typeof updater === 'function' ? updater(prev) : updater;
      onChangeRef.current?.(
        serializeRules(nestedRef.current, groupNameRef.current, next),
      );
      return next;
    });
  }, []);

  const updateRow = useCallback(
    (id, field, val) => {
      emitAndSet((prev) =>
        prev.map((r) => (r._id === id ? { ...r, [field]: val } : r)),
      );
    },
    [emitAndSet],
  );

  const removeRow = useCallback(
    (id) => {
      setSelected((prev) => prev.filter((x) => x !== id));
      emitAndSet((prev) => prev.filter((r) => r._id !== id));
    },
    [emitAndSet],
  );

  // 新行插到最前面，不是追加到末尾。
  //
  // 精确规则表是分页的，追加到末尾时：规则一超过一页，新行就落在最后一页，而表格
  // 数据一变又回到第一页——点了「添加」却什么都没发生，人会以为没加上，再点几次，
  // 于是多出好几条空规则。逐条录入十几个模型时这个问题每次都撞上。
  //
  // 顺序只影响显示：匹配走 pickRuleFrom 的具体度优先（精确名 > 前缀 > "*"），
  // 与数组顺序无关。
  const addRow = useCallback(
    (pattern = '') => {
      emitAndSet((prev) => [
        { _id: uid(), pattern, mode: MODE_MULTIPLY, value: 1, remark: '' },
        ...prev,
      ]);
    },
    [emitAndSet],
  );

  const applyBatch = useCallback(() => {
    if (!selected.length) return;
    emitAndSet((prev) =>
      prev.map((r) =>
        selected.includes(r._id)
          ? { ...r, mode: batchMode, value: batchValue }
          : r,
      ),
    );
  }, [emitAndSet, selected, batchMode, batchValue]);

  /** 已配过规则的模型不再出现在下拉里，避免同一个模型配出两行互相打架 */
  const configuredPatterns = useMemo(
    () => new Set(rows.map((r) => (r.pattern || '').trim())),
    [rows],
  );
  const modelOptions = useMemo(
    () =>
      groupModels
        .filter((m) => !configuredPatterns.has(m))
        .map((m) => ({ label: m, value: m })),
    [groupModels, configuredPatterns],
  );

  const duplicates = useMemo(() => {
    const counts = {};
    rows.forEach((r) => {
      const p = (r.pattern || '').trim();
      if (p) counts[p] = (counts[p] || 0) + 1;
    });
    return new Set(Object.keys(counts).filter((k) => counts[k] > 1));
  }, [rows]);

  const staleSet = useMemo(() => new Set(staleRules), [staleRules]);
  const staleSetRef = useRef(staleSet);
  staleSetRef.current = staleSet;
  const duplicatesRef = useRef(duplicates);
  duplicatesRef.current = duplicates;
  const groupRatioRef = useRef(groupRatio);
  groupRatioRef.current = groupRatio;

  const filtered = useMemo(() => {
    const kw = keyword.trim().toLowerCase();
    if (!kw) return rows;
    return rows.filter(
      (r) =>
        (r.pattern || '').toLowerCase().includes(kw) ||
        (r.remark || '').toLowerCase().includes(kw),
    );
  }, [rows, keyword]);

  const wildcardRows = useMemo(
    () => filtered.filter((r) => isWildcard(r.pattern)),
    [filtered],
  );
  const exactRows = useMemo(
    () => filtered.filter((r) => !isWildcard(r.pattern)),
    [filtered],
  );

  const modeOptions = useMemo(
    () => buildModeOptions(allowOverride, t),
    [allowOverride, t],
  );

  const hasOverride = useMemo(
    () => rows.some((r) => r.mode === MODE_OVERRIDE),
    [rows],
  );

  const columns = useMemo(
    () => [
      {
        title: '',
        key: 'select',
        width: 40,
        render: (_, record) => (
          <Checkbox
            checked={selected.includes(record._id)}
            onChange={(e) =>
              setSelected((prev) =>
                e.target.checked
                  ? [...prev, record._id]
                  : prev.filter((x) => x !== record._id),
              )
            }
          />
        ),
      },
      {
        title: t('模型'),
        dataIndex: 'pattern',
        key: 'pattern',
        width: 260,
        render: (_, record) => (
          <div className='flex items-center gap-1'>
            <Input
              size='small'
              value={record.pattern}
              status={
                duplicatesRef.current.has((record.pattern || '').trim())
                  ? 'warning'
                  : undefined
              }
              onChange={(v) => updateRow(record._id, 'pattern', v)}
            />
            {staleSetRef.current.has(record.pattern) && (
              <Tag size='small' color='orange' shape='circle'>
                {t('未匹配')}
              </Tag>
            )}
          </div>
        ),
      },
      {
        title: t('模式'),
        dataIndex: 'mode',
        key: 'mode',
        width: 130,
        render: (_, record) => (
          <Select
            size='small'
            style={{ width: '100%' }}
            value={record.mode}
            onChange={(v) => updateRow(record._id, 'mode', v)}
            optionList={modeOptions}
          />
        ),
      },
      {
        title: t('值'),
        dataIndex: 'value',
        key: 'value',
        width: 110,
        render: (_, record) => (
          <InputNumber
            size='small'
            min={0}
            step={0.1}
            value={record.value}
            style={{ width: '100%' }}
            onChange={(v) => updateRow(record._id, 'value', v ?? 0)}
          />
        ),
      },
      {
        title: t('实际倍率'),
        key: 'effective',
        width: 100,
        render: (_, record) => {
          const base = groupRatioRef.current ?? 1;
          const effective =
            record.mode === MODE_OVERRIDE ? record.value : base * record.value;
          return (
            <Text type={record.mode === MODE_OVERRIDE ? 'warning' : undefined}>
              {Number(effective.toFixed(4))}x
            </Text>
          );
        },
      },
      {
        title: t('备注'),
        dataIndex: 'remark',
        key: 'remark',
        render: (_, record) => (
          <Input
            size='small'
            value={record.remark}
            placeholder={t('为什么是这个价')}
            onChange={(v) => updateRow(record._id, 'remark', v)}
          />
        ),
      },
      {
        title: '',
        key: 'actions',
        width: 50,
        render: (_, record) => (
          <Popconfirm
            title={t('确认删除该规则？')}
            onConfirm={() => removeRow(record._id)}
            position='left'
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
    ],
    [t, updateRow, removeRow, selected],
  );

  if (!group) {
    return <Empty description={texts.emptyHint || t('请先选择一个分组')} />;
  }

  return (
    <div>
      <Banner
        type='info'
        closeIcon={null}
        description={
          <div className='text-xs leading-6'>
            {texts.banner || (
              <>
                <div>
                  {t(
                    '折扣 ×：在分组基础倍率上再乘，改分组倍率时所有模型的优惠自动跟随。',
                  )}
                </div>
                <div>
                  {t(
                    '定价 =：该模型就是这个倍率，与分组基础倍率、用户身份折扣全部脱钩。',
                  )}
                </div>
                <div>
                  {t('当前分组基础倍率')}：
                  <Text strong>{groupRatio ?? 1}x</Text>
                </div>
              </>
            )}
          </div>
        }
      />

      {hasOverride && (
        <Banner
          className='mt-2'
          type='warning'
          closeIcon={null}
          description={t(
            '该分组存在「定价 =」规则：改分组基础倍率时这些模型不会跟随，且它们会覆盖掉针对用户分组配置的身份折扣。可用下方试算器确认最终倍率。',
          )}
        />
      )}

      <div className='mt-3 flex flex-wrap items-center gap-2'>
        <Input
          prefix={<IconSearch />}
          placeholder={t('搜索模型或备注')}
          value={keyword}
          onChange={setKeyword}
          style={{ width: 220 }}
          showClear
        />
        <Select
          filter
          placeholder={t('从本分组可用模型中添加')}
          style={{ width: 260 }}
          optionList={modelOptions}
          value={null}
          onChange={(v) => v && addRow(v)}
          emptyContent={
            groupModels.length === 0
              ? t('该分组暂无渠道覆盖的模型')
              : t('全部模型已配置')
          }
        />
        <Button icon={<IconPlus />} theme='outline' onClick={() => addRow('')}>
          {t('自定义/通配规则')}
        </Button>
      </div>

      {selected.length > 0 && (
        <div className='mt-3 flex flex-wrap items-center gap-2 rounded-lg bg-[var(--semi-color-fill-0)] p-2'>
          <Text size='small'>
            {t('已选 {{count}} 条', { count: selected.length })}
          </Text>
          <Select
            size='small'
            style={{ width: 110 }}
            value={batchMode}
            onChange={setBatchMode}
            optionList={modeOptions}
          />
          <InputNumber
            size='small'
            min={0}
            step={0.1}
            value={batchValue}
            style={{ width: 100 }}
            onChange={(v) => setBatchValue(v ?? 0)}
          />
          <Button size='small' theme='solid' onClick={applyBatch}>
            {t('批量应用')}
          </Button>
          <Button
            size='small'
            theme='borderless'
            onClick={() => setSelected([])}
          >
            {t('取消选择')}
          </Button>
        </div>
      )}

      {wildcardRows.length > 0 && (
        <div className='mt-4'>
          <Text type='tertiary' size='small' className='mb-1 block'>
            {t('通配规则（影响面大，单独列出）')}
          </Text>
          <CardTable
            columns={columns}
            dataSource={wildcardRows}
            rowKey='_id'
            hidePagination
            size='small'
          />
        </div>
      )}

      <div className='mt-4'>
        {wildcardRows.length > 0 && (
          <Text type='tertiary' size='small' className='mb-1 block'>
            {t('精确规则')}
          </Text>
        )}
        <CardTable
          columns={columns}
          dataSource={exactRows}
          rowKey='_id'
          size='small'
          empty={
            <Text type='tertiary'>
              {t('该分组暂无模型折扣，所有模型按分组基础倍率计费')}
            </Text>
          }
        />
      </div>

      {duplicates.size > 0 && (
        <Text type='warning' size='small' className='mt-2 block'>
          {t('存在重复的模型规则：')}
          {Array.from(duplicates).join(', ')}
        </Text>
      )}
      {staleRules.length > 0 && (
        <Text type='warning' size='small' className='mt-2 block'>
          {t('以下规则在本分组匹配不到任何模型，当前不会生效：')}
          {staleRules.join(', ')}
        </Text>
      )}
    </div>
  );
}
