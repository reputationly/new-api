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
  Dropdown,
  Empty,
  Input,
  InputNumber,
  Popconfirm,
  Select,
  Tag,
  TextArea,
  Typography,
} from '@douyinfe/semi-ui';
import { IconDelete, IconPlus, IconSearch } from '@douyinfe/semi-icons';
import { useTranslation } from 'react-i18next';
import { API, showError, showSuccess } from '../../../helpers';
import CardTable from '../../../components/common/ui/CardTable';

const { Text } = Typography;

let _idCounter = 0;
const uid = () => `gmr_${++_idCounter}`;

const MODE_MULTIPLY = 'multiply';
const MODE_OVERRIDE = 'override';

// 与 Semi Table 的默认每页条数一致：分页改受控后这个值要自己给，写错会让页码
// 与实际切片对不上（翻到第 2 页显示的还是第 1 页的行）。
const PAGE_SIZE = 10;

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
  return rulesToRows(nested[group]);
}

/**
 * 单个分组的规则对象 → 表格行。与 rowsToRules 互为逆运算。
 *
 * 抽出来是为了让 JSON 视图和表格视图共用同一对转换：两边各写一份的话，
 * 「切到 JSON 改一个字再切回来，某一列悄悄变了」这种问题永远查不清。
 */
function rulesToRows(rules) {
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

/**
 * 表格行 → 单个分组的规则对象。
 *
 * 两处有损，是 JSON 对象本身的约束，不是实现取巧：模型名为空的行无法成为 key
 * （那是还没填完的输入），重复模型名只保留最后一条。切到 JSON 视图前会就这两点
 * 给出提示，不让它们静默消失。
 */
function rowsToRules(rows) {
  const out = {};
  rows.forEach((row) => {
    const pattern = (row.pattern || '').trim();
    if (!pattern) return;
    out[pattern] = {
      mode: row.mode,
      value: row.value,
      ...(row.remark ? { remark: row.remark } : {}),
    };
  });
  return out;
}

/** 把某分组的规则行写回整份 GroupModelRatio JSON，其他分组原样保留。 */
function serializeRules(nested, group, rows) {
  const next = { ...nested };
  const groupRules = rowsToRules(rows);
  if (Object.keys(groupRules).length === 0) {
    delete next[group];
  } else {
    next[group] = groupRules;
  }
  return JSON.stringify(next, null, 2);
}

/**
 * 校验 JSON 视图里贴进来的规则，口径与后端 CheckGroupModelRatio 对齐。
 *
 * 前端拦一道不是为了替代后端，是为了让错误在**当前这一屏**暴露：等到点保存
 * 才报错的话，人已经切走分组、上下文全丢了，而 option 是全量覆盖保存的，
 * 一次失败要重贴整份。
 *
 * 返回错误信息字符串，没问题返回 ''。
 */
function validateRules(parsed, allowOverride, t) {
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return t('顶层必须是一个对象：{ "模型名": { "mode": ..., "value": ... } }');
  }
  for (const [pattern, rule] of Object.entries(parsed)) {
    if (!pattern.trim()) {
      return t('存在空的模型名');
    }
    const star = pattern.indexOf('*');
    if (star !== -1 && star !== pattern.length - 1) {
      return `${pattern}：${t('「*」只能放在结尾作前缀通配')}`;
    }
    if (typeof rule === 'number') {
      if (!(rule >= 0)) return `${pattern}：${t('倍率不能小于 0')}`;
      continue;
    }
    if (!rule || typeof rule !== 'object' || Array.isArray(rule)) {
      return `${pattern}：${t('规则必须是对象或数字')}`;
    }
    // mode 非字符串时后端 unmarshal 直接失败（Mode 是 string 字段），这里同样拒绝，
    // 免得前端放行、保存时才被后端打回。
    if (rule.mode != null && typeof rule.mode !== 'string') {
      return `${pattern}：${t('mode 必须是字符串')}`;
    }
    // 用 || 而不是 ??：后端把空串也归一成 multiply（UnmarshalJSON 与
    // CheckGroupModelRatio 的 case "" 分支都是这么写的），?? 只兜 null/undefined，
    // 会把 {"mode": ""} 这种后端认可的写法拦在前端，与本函数声称的对齐相矛盾。
    const mode = rule.mode || MODE_MULTIPLY;
    if (mode !== MODE_MULTIPLY && mode !== MODE_OVERRIDE) {
      return `${pattern}：${t('未知的 mode')} "${mode}"`;
    }
    if (mode === MODE_OVERRIDE && !allowOverride) {
      return `${pattern}：${t('此处不允许 override，请改用 multiply')}`;
    }
    if (typeof rule.value !== 'number' || !(rule.value >= 0)) {
      return `${pattern}：${t('value 必须是不小于 0 的数字')}`;
    }
  }
  return '';
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
  // 「同步到其他分组」的候选。不传就不显示那个按钮——调用方没给候选时，
  // 与其渲染一个空下拉，不如整个藏掉。
  syncTargets = [],
}) {
  const { t } = useTranslation();

  const nested = useMemo(() => parseJSON(value), [value]);
  const [rows, setRows] = useState(() => flattenRules(nested, group));
  const [keyword, setKeyword] = useState('');
  const [selected, setSelected] = useState([]);
  const [groupModels, setGroupModels] = useState([]);
  const [batchMode, setBatchMode] = useState(MODE_MULTIPLY);
  const [batchValue, setBatchValue] = useState(0.8);
  // 精确规则表的分页必须受控。Semi Table 的内置分页是非受控的，dataSource 换引用
  // 就回到第一页——而 rows 每敲一个键都会重建（emitAndSet），结果是在第二页改折扣
  // 值，刚输入就被弹回第一页，改到一半的那行看不见了。
  const [page, setPage] = useState(1);

  // JSON 视图。逐个下拉添加模型在配置几十个模型时是纯体力活，JSON 可以整段粘贴。
  //
  // jsonText 是本地缓冲而不是从 rows 实时算出来的：JSON 打到一半必然是非法的，
  // 每敲一个字就回写父组件会把配置打成碎片。只有解析并校验通过才提交。
  const [viewMode, setViewMode] = useState('table');
  const [jsonText, setJsonText] = useState('');
  const [jsonError, setJsonError] = useState('');

  // 切分组时整体重建：规则集是按分组隔离的，沿用上一个分组的行会写串
  const prevGroupRef = useRef(group);
  useEffect(() => {
    if (prevGroupRef.current !== group) {
      prevGroupRef.current = group;
      setRows(flattenRules(parseJSON(value), group));
      setSelected([]);
      setKeyword('');
      setPage(1);
      // JSON 缓冲是上一个分组的内容，留着会让人对着 A 分组的文本改 B 分组
      setJsonText('');
      setJsonError('');
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
  // 精确规则表是分页的，追加到末尾时新行会落在最后一页——点了「添加」却什么都没
  // 发生，人会以为没加上，再点几次，于是多出好几条空规则。逐条录入十几个模型时
  // 这个问题每次都撞上。
  //
  // 分页受控之后表格不再自动跳回第一页，所以这里必须显式跳：否则在第三页点添加，
  // 新行安静地待在第一页，症状和当初一模一样。
  //
  // 顺序只影响显示：匹配走 pickRuleFrom 的具体度优先（精确名 > 前缀 > "*"），
  // 与数组顺序无关。
  const addRow = useCallback(
    (pattern = '') => {
      emitAndSet((prev) => [
        { _id: uid(), pattern, mode: MODE_MULTIPLY, value: 1, remark: '' },
        ...prev,
      ]);
      setPage(1);
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

  // 切到 JSON 视图：用 rows 现算一份文本。不能沿用上次的缓冲——表格那边可能已经
  // 改过了，拿旧文本会让人以为改动丢了，一保存又把表格的改动覆盖回去。
  const enterJsonView = useCallback(() => {
    setJsonText(JSON.stringify(rowsToRules(rows), null, 2));
    setJsonError('');
    setViewMode('json');
  }, [rows]);

  // 切回表格：把当前 JSON 落到 rows。解析失败就拦住，否则切过去看到的是旧数据，
  // 而人以为自己刚写的已经生效了。
  const leaveJsonView = useCallback(() => {
    const text = jsonText.trim();
    if (!text) {
      emitAndSet([]);
      setJsonError('');
      setViewMode('table');
      setPage(1);
      return;
    }
    let parsed;
    try {
      parsed = JSON.parse(text);
    } catch (e) {
      setJsonError(e.message);
      return;
    }
    const invalid = validateRules(parsed, allowOverride, t);
    if (invalid) {
      setJsonError(invalid);
      return;
    }
    emitAndSet(rulesToRows(parsed));
    setJsonError('');
    setSelected([]);
    setViewMode('table');
    setPage(1);
  }, [jsonText, allowOverride, emitAndSet, t]);

  // JSON 文本变更：能解析就即时提交，不能解析只记错误。
  //
  // 即时提交是必要的——否则在 JSON 视图里改完直接点右上角保存，改动根本没进过
  // 父组件，页面提示「你似乎并没有修改什么」，人会以为编辑器坏了。
  const handleJsonChange = useCallback(
    (text) => {
      setJsonText(text);
      if (!text.trim()) {
        setJsonError('');
        emitAndSet([]);
        return;
      }
      let parsed;
      try {
        parsed = JSON.parse(text);
      } catch (e) {
        setJsonError(e.message);
        return;
      }
      const invalid = validateRules(parsed, allowOverride, t);
      if (invalid) {
        setJsonError(invalid);
        return;
      }
      setJsonError('');
      emitAndSet(rulesToRows(parsed));
    },
    [allowOverride, emitAndSet, t],
  );

  const copyJson = useCallback(async () => {
    const text = JSON.stringify(rowsToRules(rows), null, 2);
    try {
      await navigator.clipboard.writeText(text);
      showSuccess(t('已复制当前分组的规则 JSON'));
    } catch {
      // 非 https 或浏览器不给权限时剪贴板不可用，退回到让人自己选中复制
      setJsonText(text);
      setViewMode('json');
      showError(t('复制失败，已切到 JSON 视图，请手动选中复制'));
    }
  }, [rows, t]);

  // 把当前分组的规则整份复制到另一个分组。
  //
  // 存在的理由：default 与 premium 合并成同一个池子之后，同一份折扣必须在两个
  // 分组各配一遍（GroupModelRatio 按分组索引），手工配两遍必然有一天配漏一条，
  // 而漏配的表现只是某个模型贵了一点，没人会立刻发现。
  const syncToGroup = useCallback(
    (target) => {
      if (!target || target === group) return;
      const nextNested = { ...nestedRef.current };
      const groupRules = rowsToRules(rows);
      if (Object.keys(groupRules).length === 0) {
        delete nextNested[target];
      } else {
        nextNested[target] = groupRules;
      }
      onChangeRef.current?.(JSON.stringify(nextNested, null, 2));
      showSuccess(
        t('已把 {{n}} 条规则同步到 {{g}}', {
          n: Object.keys(groupRules).length,
          g: target,
        }),
      );
    },
    [group, rows, t],
  );

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

  // 分页受控之后越界要自己收：删行或搜索把结果缩短时，当前页可能已经不存在，
  // 表格会渲染成一片空白——看起来和「规则全没了」无法区分。
  //
  // 夹取要在**渲染期**做，不能只靠下面那个 effect：effect 是 passive 的，跑在
  // commit 之后，结果集缩短的那一次渲染仍然拿着越界的 page，切片出空数组，
  // 于是空表会先画出来一帧再被纠正——正是这段注释要避免的那个症状本身。
  const totalPages = Math.max(1, Math.ceil(exactRows.length / PAGE_SIZE));
  const safePage = Math.min(page, totalPages);

  // effect 仍然要留：safePage 只夹住当下这一帧，page 本身还是越界值。等结果集
  // 恢复（清空搜索）时 safePage 会解夹，把人弹到一个他没主动翻过去的页面上。
  // 这里把 state 收敛到用户实际看到的那一页。
  useEffect(() => {
    if (page > totalPages) setPage(totalPages);
  }, [page, totalPages]);

  // 受控分页下 Semi Table 不再自己切片——它把 dataSource 当成「已经是当前页的数据」。
  // 不切的话每页都渲染全量行，翻页按钮点了没反应。
  const pagedExactRows = useMemo(
    () => exactRows.slice((safePage - 1) * PAGE_SIZE, safePage * PAGE_SIZE),
    [exactRows, safePage],
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
        {viewMode === 'table' && (
          <>
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
            <Button
              icon={<IconPlus />}
              theme='outline'
              onClick={() => addRow('')}
            >
              {t('自定义/通配规则')}
            </Button>
          </>
        )}

        <div className='ml-auto flex items-center gap-2'>
          {/*
            用 Dropdown 而不是 Select：这里要的是「执行一次同步」这个动作，不是
            选一个值。受控 Select（value={null}）点选项根本不触发 onChange，而且
            它选完还会显示成「已选中 premium」，与实际语义不符。
          */}
          {syncTargets.filter((g) => g !== group).length > 0 && (
            <Dropdown
              trigger='click'
              position='bottomRight'
              render={
                <Dropdown.Menu>
                  {syncTargets
                    .filter((g) => g !== group)
                    .map((g) => (
                      <Dropdown.Item key={g} onClick={() => syncToGroup(g)}>
                        {g}
                      </Dropdown.Item>
                    ))}
                </Dropdown.Menu>
              }
            >
              <Button size='small' theme='borderless'>
                {t('同步到其他分组')}
              </Button>
            </Dropdown>
          )}
          <Button size='small' theme='borderless' onClick={copyJson}>
            {t('复制 JSON')}
          </Button>
          <Button
            size='small'
            theme='outline'
            onClick={viewMode === 'table' ? enterJsonView : leaveJsonView}
          >
            {viewMode === 'table' ? t('切换到 JSON') : t('切换到表格')}
          </Button>
        </div>
      </div>

      {viewMode === 'json' && (
        <div className='mt-3'>
          <Text type='tertiary' size='small' className='mb-1 block'>
            {t(
              '这里是「{{g}}」这一个分组的规则，与上方表格逐条对应。整段粘贴即可批量配置；改完可用「同步到其他分组」把同一份规则复制过去。',
              { g: group },
            )}
          </Text>
          <TextArea
            value={jsonText}
            onChange={handleJsonChange}
            autosize={{ minRows: 12, maxRows: 30 }}
            style={{ fontFamily: 'monospace' }}
            placeholder={
              '{\n  "GLM-5.2": { "mode": "multiply", "value": 0.9 }\n}'
            }
            validateStatus={jsonError ? 'error' : 'default'}
          />
          {jsonError ? (
            <Text type='danger' size='small' className='mt-1 block'>
              {t('JSON 无效，改动尚未生效：')}
              {jsonError}
            </Text>
          ) : (
            <Text type='tertiary' size='small' className='mt-1 block'>
              {t('格式正确，改动已同步到表格；仍需点右上角保存才会落库。')}
            </Text>
          )}
        </div>
      )}

      {viewMode === 'table' && selected.length > 0 && (
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

      {viewMode === 'table' && wildcardRows.length > 0 && (
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

      {viewMode === 'table' && (
        <div className='mt-4'>
          {wildcardRows.length > 0 && (
            <Text type='tertiary' size='small' className='mb-1 block'>
              {t('精确规则')}
            </Text>
          )}
          <CardTable
            columns={columns}
            dataSource={pagedExactRows}
            rowKey='_id'
            size='small'
            pagination={{
              currentPage: safePage,
              pageSize: PAGE_SIZE,
              total: exactRows.length,
              onPageChange: setPage,
            }}
            empty={
              <Text type='tertiary'>
                {t('该分组暂无模型折扣，所有模型按分组基础倍率计费')}
              </Text>
            }
          />
        </div>
      )}

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
