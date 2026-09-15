/*
Copyright (C) 2025 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/

import React, { useEffect, useMemo, useRef, useState } from 'react';
import {
  Banner,
  Button,
  Card,
  Empty,
  Modal,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  TextArea,
  Tooltip,
  Typography,
} from '@douyinfe/semi-ui';
import { useTranslation } from 'react-i18next';
import CreatableSelect from '../../../components/common/CreatableSelect';
import { API, showError, showSuccess } from '../../../helpers';

const { Text } = Typography;

/** 官方 backendSchema 的全部枚举值。写一个不在里面的，客户端会拒掉**整份目录**。 */
const BACKENDS = [
  'nano_banana', 'kling', 'kontext', 'openai', 'midjourney', 'seedream',
  'qwen', 'minimax', 'minimax_v3', 'veo3', 'wan_i2v', 'minimax_tts',
  'seedaudio', 'minimax_music', 'minimax_music_cover', 'elevenlabs_music',
  'seedance', 'kling_avatar', 'kling_motion_control', 'jimeng_motion_control',
  'text_anthropic', 'text_openai', 'text_gemini', 'mediakit_enhance',
  'mediakit_erase_subtitle',
];

const GROUPS = [
  { key: 'image', label: '图片' },
  { key: 'video', label: '视频' },
  { key: 'audio', label: '音频' },
  // **对话模型必须渲染出来。** 早先它只靠 passthrough 保住不丢，页面上
  // 完全不可见 —— 管理员看到的是"共 5 个模型"而客户端上有 9 个，
  // 想调一条对话模型只能去「编辑原文 JSON」里手写，而那正是这个表格要
  // 替代的事。不可见还让「回到表格」那个静默回滚没有任何征兆。
  { key: 'text', label: '对话' },
];

/**
 * 从一份完整配置里取出各段，缺的补空数组。
 *
 * **只能有这一份。** 早先是四处各写一遍
 * `{ image: parsed.image ?? [], video: …, audio: … }`（首次载入、载入出厂
 * 目录、回到表格、清空），加一个新段就要记得四处都改 —— 而 `text` 加进来时
 * 恰好没有任何一处改到，它在页面上整整缺席了一轮。
 */
const catalogFromParsed = (parsed) =>
  Object.fromEntries(GROUPS.map((g) => [g.key, parsed?.[g.key] ?? []]));

/** 是不是对话模型那一组（字段形态与媒体模型不同，见 columns）。 */
const isText = (group) => group === 'text';

/** 各组都空 = 一份"明确的空目录"，和"空配置（用出厂值）"是两回事。 */
const isEmptyCatalog = (c) =>
  GROUPS.every((g) => !Array.isArray(c?.[g.key]) || c[g.key].length === 0);

const emptyCatalogHint = (t) =>
  t(
    '这是一份空目录，存下去客户端上会一个模型都没有。' +
      '想恢复出厂目录请用「恢复出厂目录」按钮（那存的是空配置，不是空目录）。',
  );

/**
 * 蒜狸小助手（MiniMax Design 客户端）的模型目录。
 *
 * 这份配置决定客户端**能选哪些模型、每个模型有哪些参数、参数之间怎么互斥**，
 * 由 `GET /api/v1/models/config` 下发，客户端重启后生效。
 *
 * ## 为什么是表格 + JSON 兜底，而不是纯 JSON
 *
 * 最初做成一个裸 JSON 框。功能上没问题，但**和真实用法对不上**：常干的事是
 * "把某个模型上架/下架"，而那在两百多行里意味着找到对应那段、连括号一起删
 * 干净。所以常用操作走表格，`params` / `paramConstraints` 这类嵌套深、改得少
 * 的留在单条的 JSON 里编辑。
 *
 * ## 平台模型为什么是下拉框而不是输入框
 *
 * 后端按 `abilities` 表过滤：**填了一个本站没有的模型名，那条会静默消失**
 * ——不报错，只是客户端上少一个模型。手写极容易错（`minimax-h3-fl2va` 和
 * `minimax-h3-ref2va` 只差三个字母），所以候选从 `/api/models/pricing` 拉。
 */
const SettingsHiloCatalog = (props) => {
  const { t } = useTranslation();
  const [loading, setLoading] = useState(false);
  const [catalog, setCatalog] = useState(catalogFromParsed({}));
  const [platformModels, setPlatformModels] = useState([]);
  const [editing, setEditing] = useState(null); // { group, index, json }
  const [rawMode, setRawMode] = useState(false);
  // 服务端那边**空配置 = 用出厂目录**，不是"没有模型"。这两种状态在界面上
  // 必须区分开，否则见下面 onSave 里那段注释描述的事故。
  const [usingFactory, setUsingFactory] = useState(false);
  const [rawText, setRawText] = useState('');
  // **这个页面只渲染 GROUPS 里那三段，但配置里还有别的段（如 `text` 对话
  // 模型）。** 保存时写的是 `JSON.stringify(catalog)` —— 不把它们留住，
  // 管理员在这里改一个显示名就会把整段对话模型**静默抹掉**，
  // 表现是画布上突然选不到 LLM，而这里什么都没提示。
  //
  // 用 ref 而不是 state：它不参与渲染，放进 state 只会多一次无谓的重渲染，
  // 也容易被后来的人误当成"表格数据"去改。
  const passthrough = useRef({});

  // 把表格里的各段和"这个页面不渲染的段"合成一份完整配置。
  //
  // **三条写路径都必须走它**：表格保存、「编辑原文 JSON」的预填、
  // 「载入出厂目录」。漏掉任何一条，那条路上的 `text` 就会被静默抹掉 ——
  // 而这正是加 passthrough 要防的事（漏了两条，被检视抓到）。
  const withPassthrough = (base) => ({ ...passthrough.current, ...base });

  // 从一份完整配置里挑出这个页面不渲染的段。
  const pickPassthrough = (parsed) => {
    const known = new Set(GROUPS.map((g) => g.key));
    return Object.fromEntries(
      Object.entries(parsed ?? {}).filter(([k]) => !known.has(k)),
    );
  };
  // 有没有未保存的改动。**用来挡住父组件 refresh() 触发的回填** ——
  // 保存成功后 props.refresh() 会重新拉 options，那个 effect 会拿服务端
  // 的值把 catalog 重置掉。正常流程下没问题（刚存过，两边一样），但如果
  // 管理员在请求往返期间又改了一笔，那一笔会被悄悄冲掉。
  const dirty = useRef(false);

  // 服务端存的是压缩单行，展开成对象。解析不了就退回空目录并提示 ——
  // 这时候更要让人看见"配置坏了"，而不是一个看起来正常的空表格。
  useEffect(() => {
    const value = props.options?.HiloCatalog;
    if (value === undefined) return;
    // 有未保存的改动时不回填 —— 否则父组件一 refresh 就把人家正在改的
    // 内容冲掉，而界面上没有任何提示。
    if (dirty.current) return;
    if (!value.trim()) {
      setUsingFactory(true);
      passthrough.current = {};
      setCatalog(catalogFromParsed({}));
      setRawText('');
      return;
    }
    setUsingFactory(false);
    try {
      const parsed = JSON.parse(value);
      // 收起这个页面不渲染的段，保存时原样带回去。
      passthrough.current = pickPassthrough(parsed);
      setCatalog(catalogFromParsed(parsed));
      setRawText(JSON.stringify(parsed, null, 2));
    } catch (e) {
      showError(t('现有配置不是合法 JSON，已切到原文模式：') + e.message);
      setRawText(value);
      setRawMode(true);
    }
  }, [props.options]);

  useEffect(() => {
    (async () => {
      try {
        // 管理员专用的上架模型全集。失败不致命 —— 下拉退化成可输入，
        // 总比整个页面打不开好。
        const res = await API.get('/api/models/pricing');
        const data = res?.data?.data ?? [];
        setPlatformModels(
          [...new Set(data.map((m) => m.model_name ?? m.name ?? m))]
            .filter(Boolean)
            .sort(),
        );
      } catch {
        /* 拿不到就手输 */
      }
    })();
  }, []);

  // **聚合模型也要当候选。**
  //
  // `/api/models/pricing` 源自 abilities 表，而聚合模型没有渠道 ability,
  // 天然不在里面 —— 但出厂目录里视频那条填的正是 `minimax-h3-2k`（画质要
  // 追平官方只能靠那条编排流水线）。不并进来的话，默认值不在候选列表里,
  // 而管理员从界面重选只能选到裸模型，静默把 2K 编排换掉。
  const aggregateModels = useMemo(() => {
    const raw = props.options?.AggregateModelConfig;
    if (!raw) return [];
    try {
      const list = JSON.parse(raw);
      return (Array.isArray(list) ? list : [])
        .filter((m) => m?.enabled && m?.name)
        .map((m) => m.name);
    } catch {
      return [];
    }
  }, [props.options]);

  const modelOptions = useMemo(() => {
    const agg = aggregateModels.map((m) => ({
      label: `${m}（编排）`,
      value: m,
    }));
    const plain = platformModels
      .filter((m) => !aggregateModels.includes(m))
      .map((m) => ({ label: m, value: m }));
    return [...agg, ...plain];
  }, [aggregateModels, platformModels]);

  const mutate = (next) => {
    dirty.current = true;
    setCatalog(next);
  };

  const removeRow = (group, index) => {
    const next = { ...catalog, [group]: catalog[group].filter((_, i) => i !== index) };
    mutate(next);
  };

  const patchRow = (group, index, patch) => {
    const rows = [...catalog[group]];
    rows[index] = { ...rows[index], ...patch };
    mutate({ ...catalog, [group]: rows });
  };

  // 表格里的单元格编辑：合并语义（只改一个字段）。
  const patchModel = (group, index, patch) => {
    const rows = [...catalog[group]];
    rows[index] = { ...rows[index], model: { ...rows[index].model, ...patch } };
    mutate({ ...catalog, [group]: rows });
  };

  // 详情弹窗保存：**整体替换，不能合并**。
  //
  // 弹窗里展示的是完整的 model 对象，管理员在里面删掉一个键（比如过时的
  // paramConstraints、某个不再支持的 param）是正当操作。走 patchModel 的话
  // 旧对象会铺在下面，删掉的键原样回来，而界面提示「保存成功」——
  // 删不掉且不报错。
  const replaceModel = (group, index, model) => {
    const rows = [...catalog[group]];
    rows[index] = { ...rows[index], model };
    mutate({ ...catalog, [group]: rows });
  };

  const addRow = (group) => {
    // 对话模型的空模板：字段集与媒体模型不同（见 textColumns 的说明）。
    // 套媒体那套会带出一堆客户端 schema 里不存在的键（backend / params /
    // tool_names），而整份目录里只要有一条 backend 不在枚举里，
    // **整份目录都会被拒**——表现是"一个模型都没有"，不是"少了一个"。
    if (isText(group)) {
      mutate({
        ...catalog,
        text: [
          ...(catalog.text ?? []),
          { platform_model: '', model: { id: '', name: '', supportsVideo: false } },
        ],
      });
      return;
    }
    const type = group;
    const next = {
      ...catalog,
      [group]: [
        ...catalog[group],
        {
          platform_model: '',
          model: {
            id: '', name: '', backend: '', type,
            display_name: '', description: '', visibility: '', icon_url: '',
            tool_names: [`hub_generate_${group === 'audio' ? 'audio_music' : group}`],
            hot: false, max_refs: 0, params: {},
          },
        },
      ],
    };
    mutate(next);
  };

  async function save(payload) {
    setLoading(true);
    try {
      const res = await API.put('/api/option/', {
        key: 'HiloCatalog',
        value: payload,
      });
      if (res === undefined) return;
      if (res.data?.success === false) {
        // 后端的校验信息是可读中文（"xxx 的 backend 不是官方支持的值"），
        // 原样透出去比包一层"保存失败"有用得多。
        return showError(res.data.message || t('保存失败，请重试'));
      }
      dirty.current = false;
      showSuccess(t('保存成功'));
      props.refresh?.();
    } catch (e) {
      showError(e?.response?.data?.message || t('保存失败，请重试'));
    } finally {
      setLoading(false);
    }
  }

  const onSave = () => {
    // **不能把「用出厂目录」存成「空目录」。**
    //
    // 服务端的语义是：配置为空串 → 用 defaultHiloCatalog()。而表格模式存的是
    // `JSON.stringify(catalog)`，全空时是 `{"image":[],"video":[],"audio":[]}`
    // —— 那是一份合法的、明确的空目录，会被原样接受，于是所有客户端的模型
    // 一起消失。而管理员什么都没删，只是进来点了下保存。
    if (rawMode) {
      const text = rawText.trim();
      // 空串是**合法且有意义的**：它表示"用出厂目录"。
      if (text === '') return save('');
      let parsed;
      try {
        parsed = JSON.parse(text);
      } catch (e) {
        return showError(t('不是合法的 JSON：') + e.message);
      }
      // **同一道闸也要拦原文模式。**
      //
      // 「编辑原文 JSON」在出厂态下会把 `{"image":[],"video":[],"audio":[]}`
      // 预填进去 —— 那是一份合法的、明确的空目录，和空串完全不是一回事。
      // 不拦的话：进页面（出厂态）→ 点编辑原文 → 直接保存，一个字没改，
      // 所有客户端的模型就全没了。
      if (isEmptyCatalog(parsed)) return showError(emptyCatalogHint(t));
      return save(text);
    }
    if (isEmptyCatalog(catalog)) return showError(emptyCatalogHint(t));
    // 合并回未渲染的段（见 passthrough 的说明）。展开顺序让表格里的三段
    // 覆盖同名键 —— passthrough 里本来就不该有它们，这只是双保险。
    return save(JSON.stringify(withPassthrough(catalog)));
  };

  // 对话模型的列。
  //
  // **不能跟媒体模型共用一套。** 它的字段形态完全不同（见 dto.HiloTextModel）：
  // 没有 backend、没有 params、显示名用 `name` 而不是 `display_name`。
  // 硬套媒体那套的话，backend 列会渲染出一个必选却填不对的下拉，
  // 管理员一改就把一条合法的对话模型写成非法的。
  const textColumns = () => [
    {
      title: t('平台模型'),
      dataIndex: 'platform_model',
      width: 260,
      render: (v, _r, index) => (
        <CreatableSelect
          value={v || undefined}
          placeholder={t('选择本站模型')}
          style={{ width: '100%' }}
          optionList={modelOptions}
          onChange={(val) => patchRow('text', index, { platform_model: val })}
        />
      ),
    },
    {
      title: t('客户端显示名'),
      width: 200,
      render: (_v, r, index) => (
        <input
          className='semi-input'
          style={{ width: '100%', padding: '4px 8px' }}
          value={r.model?.name ?? ''}
          onChange={(e) => patchModel('text', index, { name: e.target.value })}
        />
      ),
    },
    {
      title: t('模型 ID'),
      width: 220,
      render: (_v, r) => <Text code>{r.model?.id || '—'}</Text>,
    },
    {
      title: (
        <Tooltip
          content={t(
            '报了 true 之后客户端会把视频喂给它。没实测验证过的一律留 false——' +
              '读不了视频的模型不会报错，只会编一段听起来合理的描述。',
          )}
        >
          <span>{t('能读视频')} ⓘ</span>
        </Tooltip>
      ),
      width: 110,
      render: (_v, r, index) => (
        <Switch
          size='small'
          checked={!!r.model?.supportsVideo}
          onChange={(val) => patchModel('text', index, { supportsVideo: val })}
        />
      ),
    },
    {
      title: t('操作'),
      width: 130,
      render: (_v, r, index) => (
        <Space>
          <Button
            size='small'
            theme='borderless'
            onClick={() =>
              setEditing({
                group: 'text',
                index,
                json: JSON.stringify(r.model, null, 2),
              })
            }
          >
            {t('详情')}
          </Button>
          <Button
            size='small'
            theme='borderless'
            type='danger'
            onClick={() => removeRow('text', index)}
          >
            {t('下架')}
          </Button>
        </Space>
      ),
    },
  ];

  const columns = (group) => [
    {
      title: t('平台模型'),
      dataIndex: 'platform_model',
      width: 230,
      // **必须用 CreatableSelect，不能用裸 Select。**
      //
      // 候选是异步来的（/api/models/pricing + 聚合配置），而 Semi 2.72 在
      // filter + allowCreate + 受控 value 下不重新收集挂载后才到的候选，
      // 下拉会永远「暂无数据」——见 components/common/CreatableSelect.jsx
      // 的注释。那样管理员又回到手写模型名，正是这个下拉要防的事。
      render: (v, _r, index) => (
        <CreatableSelect
          value={v || undefined}
          placeholder={t('选择本站模型')}
          style={{ width: '100%' }}
          optionList={modelOptions}
          onChange={(val) => patchRow(group, index, { platform_model: val })}
        />
      ),
    },
    {
      title: (
        <Tooltip content={t('客户端往哪条路径发、发什么字段，由它决定。不是标签。')}>
          <span>{t('backend')} ⓘ</span>
        </Tooltip>
      ),
      width: 190,
      render: (_v, r, index) => (
        <Select
          value={r.model?.backend || undefined}
          placeholder={t('必选')}
          filter
          style={{ width: '100%' }}
          optionList={BACKENDS.map((b) => ({ label: b, value: b }))}
          onChange={(val) => patchModel(group, index, { backend: val })}
        />
      ),
    },
    {
      title: t('客户端显示名'),
      width: 180,
      render: (_v, r, index) => (
        <input
          className='semi-input'
          style={{ width: '100%', padding: '4px 8px' }}
          value={r.model?.display_name ?? ''}
          onChange={(e) =>
            patchModel(group, index, { display_name: e.target.value })
          }
        />
      ),
    },
    {
      title: t('模型 ID'),
      width: 160,
      render: (_v, r) => <Text code>{r.model?.id || '—'}</Text>,
    },
    {
      title: t('参数'),
      width: 90,
      render: (_v, r) => {
        const n = Object.keys(r.model?.params ?? {}).length;
        return n ? <Tag>{n}</Tag> : <Text type='tertiary'>—</Text>;
      },
    },
    {
      title: t('操作'),
      width: 130,
      render: (_v, r, index) => (
        <Space>
          <Button
            size='small'
            theme='borderless'
            onClick={() =>
              setEditing({
                group,
                index,
                json: JSON.stringify(r.model, null, 2),
              })
            }
          >
            {t('详情')}
          </Button>
          <Button
            size='small'
            theme='borderless'
            type='danger'
            onClick={() => removeRow(group, index)}
          >
            {t('下架')}
          </Button>
        </Space>
      ),
    },
  ];

  const total = useMemo(
    () => GROUPS.reduce((n, g) => n + (catalog[g.key]?.length ?? 0), 0),
    [catalog],
  );

  return (
    <Card style={{ marginTop: '10px' }}>
      <Banner
        type='info'
        closeIcon={null}
        description={t(
          '决定蒜狸小助手客户端能选哪些模型。留空则使用出厂目录。' +
            '只会报出本站真实存在渠道的模型——平台模型填了本站没有的，那一条会静默消失。' +
            '改完客户端需要重启。',
        )}
        style={{ marginBottom: 12 }}
      />

      {usingFactory && !rawMode && (
        <Banner
          type='warning'
          closeIcon={null}
          description={t(
            '当前没有自定义配置，客户端拿到的是**出厂目录**（下表是空的，但客户端上有模型）。' +
              '想改的话先点「载入出厂目录」，改完再保存。',
          )}
          style={{ marginBottom: 12 }}
        >
          <Button
            size='small'
            style={{ marginTop: 8 }}
            onClick={async () => {
              // **走专用接口拿存储格式，不要从下发结果反推。**
              //
              // `/api/v1/models/config` 的响应里没有 `platform_model`
              // （controller/hilo.go 只 append 了 e.Model），反推只能靠
              // `model_name || id` 猜 —— 而 H3 那条恰好猜不对：存的是
              // `minimax-h3-2k`（聚合编排），model_name/id 是 `MiniMax-H3`。
              // 猜错之后一保存，那条要么丢掉 2K 编排、要么被后端过滤掉,
              // 正是这个页面要防的"静默消失"。
              try {
                const res = await API.get('/api/option/hilo_catalog_default');
                const parsed = JSON.parse(res?.data?.data ?? '{}');
                // 出厂目录里也有 `text`（见 setting/hilo_catalog.go 的
                // Text: defaultHiloTextCatalog()）。不收起来的话，
                // 「载入出厂目录 → 保存」存下去的是一份没有对话模型的目录。
                passthrough.current = pickPassthrough(parsed);
                setCatalog(catalogFromParsed(parsed));
                setUsingFactory(false);
              } catch {
                showError(t('载入失败，可以改用「编辑原文 JSON」手写'));
              }
            }}
          >
            {t('载入出厂目录')}
          </Button>
        </Banner>
      )}
      {rawMode ? (
        <>
          <TextArea
            value={rawText}
            onChange={(v) => {
              dirty.current = true;
              setRawText(v);
            }}
            autosize={{ minRows: 18, maxRows: 40 }}
            style={{ fontFamily: 'monospace', fontSize: 12 }}
            placeholder={t('留空使用出厂目录')}
          />
          <div style={{ marginTop: 12 }}>
            <Space>
              <Button type='primary' loading={loading} onClick={onSave}>
                {t('保存')}
              </Button>
              <Button
                theme='light'
                onClick={() => {
                  try {
                    const parsed = rawText.trim() ? JSON.parse(rawText) : {};
                    // **必须跟着原文重算。** 不然 passthrough 里还留着切进
                    // 原文模式之前的那份 —— 管理员在原文里刚删掉/改过的
                    // `text`，回到表格一保存又被**还原回去**，而界面上提示
                    // 「保存成功」。丢数据至少还看得出来，这个是静默回滚。
                    passthrough.current = pickPassthrough(parsed);
                    setCatalog(catalogFromParsed(parsed));
                    setRawMode(false);
                  } catch (e) {
                    showError(t('JSON 还有语法错误，改好才能切回表格：') + e.message);
                  }
                }}
              >
                {t('回到表格')}
              </Button>
            </Space>
          </div>
        </>
      ) : (
        <>
          {GROUPS.map((g) => (
            <div key={g.key} style={{ marginBottom: 20 }}>
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  justifyContent: 'space-between',
                  marginBottom: 8,
                }}
              >
                <Text strong>{t(g.label)}</Text>
                <Button size='small' theme='light' onClick={() => addRow(g.key)}>
                  {t('+ 上架模型')}
                </Button>
              </div>
              <Table
                size='small'
                pagination={false}
                columns={isText(g.key) ? textColumns() : columns(g.key)}
                // **Semi 的 rowKey 回调只传 record，不传 index**
                // （semi-foundation 的 getRecordKey 是 `rowKey(record)`）。
                // 写成 `(r, i) => ...` 的话 i 恒为 undefined，一组里所有行拿到
                // 同一个 key —— React 会告重复 key，并且在增删行时把有状态的
                // 编辑器（Select / input）reconcile 到错的那一行上。
                // 所以把下标烘进 dataSource，再用字段名当 rowKey。
                dataSource={(catalog[g.key] ?? []).map((r, i) => ({
                  ...r,
                  __rowKey: `${g.key}-${i}`,
                }))}
                rowKey='__rowKey' 
                empty={<Empty description={t('这一类还没有上架模型')} />}
              />
            </div>
          ))}
          <Space>
            <Button type='primary' loading={loading} onClick={onSave}>
              {t('保存')}
            </Button>
            <Button
              theme='light'
              onClick={() => {
                // **必须带上未渲染的段。** 只预填 catalog 的话，
                // 「进页面 → 编辑原文 JSON → 保存」会把一个字都没碰过的
                // `text`（对话模型）整段抹掉 —— 原文保存是 `save(text)`，
                // 预填成什么就存什么。
                setRawText(JSON.stringify(withPassthrough(catalog), null, 2));
                setRawMode(true);
              }}
            >
              {t('编辑原文 JSON')}
            </Button>
            {/* 存空串 = 回到后端写死的出厂目录。**这是唯一能表达"用出厂值"
                的方式** —— 表格模式本身没法表达它（全空的表格存下去是
                一份真的空目录）。

                **要确认。** 它是一次立即生效的 PUT，会把管理员攒的整份
                自定义配置丢掉，而且没有撤销；按钮又紧挨着「保存」。
                这个文件为了防"目录静默消失"做了一圈防护，这里是唯一一处
                主动清空，不该比别处随意。 */}
            <Button
              theme='light'
              onClick={() =>
                Modal.confirm({
                  title: t('恢复出厂目录'),
                  content: t(
                    '会清空当前的自定义配置，改用出厂目录，且无法撤销。继续？',
                  ),
                  onOk: () => save(''),
                })
              }
            >
              {t('恢复出厂目录')}
            </Button>
            <Text type='tertiary'>{t('共 {{n}} 个模型', { n: total })}</Text>
          </Space>
        </>
      )}

      {/* 单条模型的完整定义。`params` / `paramConstraints` / `inputMediaLimits`
          这些嵌套深、改得少，放在这里而不是铺进表格。 */}
      <Modal
        title={t('模型定义')}
        visible={!!editing}
        width={720}
        onCancel={() => setEditing(null)}
        onOk={() => {
          try {
            const model = JSON.parse(editing.json);
            replaceModel(editing.group, editing.index, model);
            setEditing(null);
          } catch (e) {
            showError(t('不是合法的 JSON：') + e.message);
          }
        }}
      >
        <TextArea
          value={editing?.json ?? ''}
          onChange={(v) => setEditing({ ...editing, json: v })}
          autosize={{ minRows: 16, maxRows: 30 }}
          style={{ fontFamily: 'monospace', fontSize: 12 }}
        />
      </Modal>
    </Card>
  );
};

export default SettingsHiloCatalog;
