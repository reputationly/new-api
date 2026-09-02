import React, { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Banner,
  Button,
  Card,
  Collapse,
  Empty,
  Input,
  InputNumber,
  Select,
  Switch,
  Tag,
  TextArea,
  Typography,
} from '@douyinfe/semi-ui';
import { Trash2 } from 'lucide-react';
import {
  MUSIC_ENGINE_MINIMAX_MUSIC3,
  PLAYGROUND_MODEL_LEVEL_FIELDS,
  normalizeModelOptimizePrompt,
  getTabDisplay,
  getTabFieldLock,
  getTabPromptGuide,
  getTabPromptOptimize,
  getTabStoreKey,
  getPromptOptimizeGlobal,
} from '../../constants/playgroundAdmin.constants';
import { getSizesForVideoModel } from '../../constants/videoPlayground.constants';
import {
  getImageShapePreset,
  getImageShapeConfig,
} from '../../constants/imagePlayground.constants';
import { defaultOptimizeSystemPrompt } from '../../constants/promptOptimize.constants';
import { defaultPromptGuide } from '../../constants/promptGuide.constants';
import FieldInput from './FieldInput';
import UpscaleField from './UpscaleField';

const { Text, Title } = Typography;

// 单个 tab 的专用配置面板：这个 tab 显不显示、用哪些模型、每个模型在这个 tab 下的参数。
//
// 「专用」是重点：只渲染该 tab 在中央元数据里声明的 fields。同一个模型挂多个玩法时，
// 文生视频那格填的时长不会连带限制图生视频，数字人也不会再出现一个点了不生效的尺寸框。

// 图像画幅的两条提示，抽出来只是为了让上面那段 JSX 不再长一截：
//   ① 有实测过的推荐档就给一键填入（内容见 IMAGE_SHAPE_PRESETS 的注释，逐条实测）；
//   ② 同时配了「尺寸列表」与「宽高比+分辨率档」时告警 —— 体验区按 area 走，
//      那份尺寸列表一个字都不会被用到（判据见 getImageShapeConfig）。
const ImageShapeHints = ({ modelName, model, tabKey, entry, onApply }) => {
  const { t } = useTranslation();
  const preset = getImageShapePreset(modelName);

  // 「配了但不生效」的告警**直接问运行时那支函数**，不在这里重写一遍判据。
  //
  // ⚠️ 之前这里手写成「三样都配了才告警」，与 getImageShapeConfig 的实际口径对不上，
  // 漏掉两种同样静默的组合：
  //   - 老配置 sizes 里混填了比例词，再加上分辨率档 → 比例词被当宽高比用、整份精确
  //     像素失效，而 aspectRatios 是空的，那条判据永远不触发；
  //   - 配了像素又配了宽高比但没配档位 → 像素优先，宽高比一个字都不生效。
  // 两处判据分开写就一定会分叉，而这个告警存在的全部意义正是"别让人以为它管用"。
  const shape = getImageShapeConfig(
    { models: { [modelName]: model } },
    modelName,
    tabKey,
  );
  // 配了但不生效的两种情况，判据都来自上面那支运行时函数，不在这里重写：
  //   - 画幅走「比例 × 档位」时，本 tab 的尺寸列表一个值都不会被用到；
  //   - 宽高比与分辨率档**必须成对**，只配一半等于没配。
  const pixelsIgnored = (entry.sizes || []).length > 0 && shape.mode === 'area';
  const shapeIncomplete =
    (entry.aspectRatios || []).length > 0 !==
    (entry.sizeTiers || []).length > 0;

  if (!preset && !pixelsIgnored && !shapeIncomplete) return null;
  return (
    <div className='mt-2'>
      {preset && (
        <Button size='small' theme='borderless' onClick={() => onApply(preset)}>
          {t('填入推荐档位')}：{t(preset.label)}
        </Button>
      )}
      {pixelsIgnored && (
        <Text type='warning' size='small' className='block mt-1'>
          {t(
            '⚠️ 本 tab 配了「尺寸 / 分辨率」，但画幅当前由「宽高比 × 分辨率档」算出，那份列表一个值都不会生效（比例词也不例外——它只在没配「宽高比 + 分辨率档」时才作为尺寸下发）。要改用那份列表，请清空「分辨率档」。',
          )}
        </Text>
      )}
      {shapeIncomplete && (
        <Text type='warning' size='small' className='block mt-1'>
          {t(
            '⚠️ 「宽高比」与「分辨率档」必须成对配置，只配一半不生效（画幅仍由上面的「尺寸 / 分辨率」列表决定）。两个都填上才会按「面积档 × 比例」算出精确像素。',
          )}
        </Text>
      )}
    </div>
  );
};

const TabPanel = ({ category, tab, draft }) => {
  const { t } = useTranslation();
  const [pending, setPending] = useState('');
  const storeKey = getTabStoreKey(category, tab.key);
  const store = draft.stores[storeKey];
  const display = getTabDisplay(draft.tabConfig, category, tab.key);
  const optimize = getTabPromptOptimize(draft.tabConfig, category, tab.key);
  const promptGuide = getTabPromptGuide(draft.tabConfig, category, tab.key);
  const globalOptimize = getPromptOptimizeGlobal(draft.tabConfig);
  const modelLevelFields = PLAYGROUND_MODEL_LEVEL_FIELDS[storeKey] || [];
  // 引擎族的展示名直接取「引擎族」下拉的 label —— 提示文案与运营刚刚选中的那一项
  // 用同一份来源,不另写一张映射表(那种表迟早和下拉分叉)。
  const engineLabel = (v) =>
    modelLevelFields
      .find((f) => f.key === 'engine')
      ?.options?.find((o) => o.value === v)?.label || v;

  // 挂在本 tab 下的模型 = 在这份 ModelConfig 里显式声明了 tabs[tab.key] 的那些。
  const rows = useMemo(
    () =>
      Object.entries(store?.models || {})
        .filter(([, m]) => m.tabs?.[tab.key] !== undefined)
        .map(([name, m]) => ({ name, model: m, entry: m.tabs[tab.key] || {} }))
        .sort((a, b) => a.name.localeCompare(b.name)),
    [store, tab.key],
  );

  // 默认系统提示词与提示词建议都要按引擎族取:H3 要的是带字段名的分段结构、LTX-2.5 要
  // 的是长段单段落视听描述,两者与通用模板的形状都不同(且彼此相反)。
  // **占位符与「填入默认内容」两处都必须传 engine** —— 不传的话,挂着专版引擎的 tab 上
  // 展示的是通用散文模板,按钮还会把这份错的直接填进输入框、邀请运营保存。那不是
  // 「运营可能配错」,是界面主动把错的递过去。
  const tabEngines = useMemo(
    () => new Set(rows.map((r) => r.model?.engine || '')),
    [rows],
  );
  // 本 tab 下唯一的专版引擎族。**只有专版引擎多于一个时**才取不到唯一值、回落空串
  // (通用模板);专版引擎恰好一个时就用它,即使同 tab 还挂着 wan(engine 留空)——
  // 这与本机制上线时的行为一致(旧代码是 hasH3 ? H3 : ''),不在本次改动范围内。
  //
  // 混挂场景没有一个「对所有模型都对」的答案,真正的解是**留空**:留空时每个模型在
  // 运行时按自己的引擎族取内置默认(usePromptOptimize 传的是选中模型的 engine)。
  // 这里摆出的只是给运营改写用的起点,由下面的 mixedEngines 告警负责让他知情。
  const specialEngines = useMemo(
    () => [...tabEngines].filter((e) => e && e !== ''),
    [tabEngines],
  );
  const tabEngine = specialEngines.length === 1 ? specialEngines[0] : '';
  const defaultSystemPrompt = defaultOptimizeSystemPrompt(tab.key, tabEngine);
  const defaultGuide = defaultPromptGuide(tab.key, tabEngine);
  // 系统提示词是 tab 级的、只有一份,混挂两个引擎族时必然有一半模型拿到形状不对的
  // 模板 —— 这个只能提示,没法在这里替运营决定。
  const mixedEngines = specialEngines.length > 0 && tabEngines.size > 1;
  // 告警里「现在摆出来的到底是哪一份」必须由 tabEngine 推出,不能写死。
  //
  // ⚠️ 写死过一次就是个真 bug:文案写"给的是通用模板",而 1 个专版引擎 + wan 时
  // tabEngine 非空、摆出来的其实是专版模板 —— 运营照着文案判断必然判错,且不报错。
  // 文案与取值同源之后,这一类分叉就不可能再发生。
  const mixedShownTemplate = tabEngine
    ? t('{{engine}} 的专用模板', { engine: engineLabel(tabEngine) })
    : t('通用模板');

  // 某个模型「不定制的话实际会用哪一份」:tab 级改写 → 该模型自己引擎族的内置默认。
  // 与体验区运行时的取值链同源(usePromptOptimize:模型级 → tab 级 → 内置默认),模型卡
  // 片里的占位符与「填入当前生效的内容」都用它 —— 各写一份判断,迟早出现「管理端展示
  // 的是 A、用户点按钮用的是 B」,而这种分叉不报错。
  //
  // 这里按**模型自己的 engine** 取,而不是上面那个 tabEngine 猜出来的:模型卡片这一层
  // 本来就没有歧义,拿全 tab 的猜测覆盖掉确定的事实只会把错的递给运营。
  const effectivePrompt = (m) =>
    (optimize.systemPrompt || '').trim() ||
    defaultOptimizeSystemPrompt(tab.key, m?.engine || '');

  // 「这个模型到底定制过没有」:判据必须与运行时同一口径(getModelOptimizePrompt 先
  // trim 再判空),否则敲两个空格就会出现「标签写着已定制、实际跟随 tab」——管理端
  // 说谎比配错更难查。两边共用 normalizeModelOptimizePrompt。
  const customizedPrompt = (e) =>
    normalizeModelOptimizePrompt(e?.optimizePrompt);

  // 文生音乐下的 ACE-Step:体验区那个按钮走的是 draftPlan 分支(一次产出描述/歌词/
  // BPM/调式/时长并回填左侧控件),**根本不是**「AI 优化提示词」,故这里配的模板一个字
  // 都不会被用到。不隐藏输入框而是给一条告警:隐藏看起来像 bug,而且判断本身是复制来的。
  //
  // ⚠️ 这条判据抄自 useMusicGeneration 的 draftAvailable(t2m + 引擎非 Music3),是**第二
  // 份实现**。之所以接受:把整支音乐 hook 拖进管理页只为拿一个布尔值代价更大,且这里
  // 只影响一句提示文案、没有任何行为依赖它。draftAvailable 的引擎规则若改,这句话会过时
  // (不会出错),改那边时请顺手搜一下本文件。
  const draftPlanOnly = (m) =>
    category === 'music' &&
    tab.key === 't2m' &&
    (m?.engine || '') !== MUSIC_ENGINE_MINIMAX_MUSIC3;

  const candidates = useMemo(() => {
    const taken = new Set(rows.map((r) => r.name));
    return (draft.allModels || [])
      .map((m) => m.model_name)
      .filter((n) => n && !taken.has(n))
      .sort();
  }, [draft.allModels, rows]);

  return (
    <div className='flex flex-col gap-3'>
      <Card>
        <Title heading={5}>{t(tab.label)}</Title>
        <Text type='tertiary' size='small'>
          {t('模型配置存放于')} <Text code>{storeKey}</Text>
          {tab.storeIn && (
            <>
              {' — '}
              {t('入口在本分类下，产物与模型属于另一分类，故沿用其配置')}
            </>
          )}
        </Text>
        {tab.hint && (
          <Banner
            type='info'
            closeIcon={null}
            className='mt-3'
            description={t(tab.hint)}
          />
        )}
      </Card>

      <Card title={t('显示')}>
        <div className='flex flex-wrap items-end gap-6'>
          <div className='flex items-center gap-2'>
            <Switch
              size='small'
              checked={display.enabled}
              onChange={(v) =>
                draft.patchTabConfig(category, tab.key, { enabled: v })
              }
            />
            <Text>{t('网页端')}</Text>
          </div>
          <div className='flex items-center gap-2'>
            <Switch
              size='small'
              checked={display.mobile}
              disabled={!display.enabled}
              onChange={(v) =>
                draft.patchTabConfig(category, tab.key, { mobile: v })
              }
            />
            <Text>{t('移动端')}</Text>
          </div>
          <div>
            <Text size='small'>{t('排序')}</Text>
            <InputNumber
              min={0}
              value={display.order == null ? undefined : display.order}
              onChange={(v) =>
                draft.patchTabConfig(category, tab.key, {
                  order: v === '' || v == null ? null : Number(v),
                })
              }
              placeholder={t('留空=默认顺序')}
              style={{ width: 140 }}
            />
          </div>
          <div>
            <Text size='small'>{t('显示名')}</Text>
            <Input
              value={display.label}
              onChange={(v) =>
                draft.patchTabConfig(category, tab.key, { label: v })
              }
              placeholder={t(tab.label)}
              style={{ width: 180 }}
            />
          </div>
        </div>
        <Text type='tertiary' size='small' className='block mt-3'>
          {t(
            '网页端关闭时移动端一并隐藏。移动端单独关闭的玩法会出现在移动端页面的「请前往网页端使用」提示里。',
          )}
        </Text>
      </Card>

      {/* 提示词写作建议:体验区提示词框上方那个「怎么写提示词」问号的内容。
          所有 tab 都有,不跟 tab.promptOptimize 走 —— 语音那四个玩法没有 AI 优化按钮,
          但「声线描述该写哪些维度」同样要有地方讲。 */}
      <Card title={t('提示词写作建议')}>
        <TextArea
          rows={8}
          value={promptGuide}
          onChange={(v) =>
            draft.patchTabConfig(category, tab.key, { promptGuide: v })
          }
          placeholder={
            defaultGuide ||
            t(
              '如：主体 + 动作 + 镜头 + 光线 + 风格，一句话一个要素；少用「唯美」「大片感」这类抽象形容词，多写看得见的东西',
            )
          }
        />
        {defaultGuide && (
          <div className='flex items-center gap-3 mt-2'>
            <Button
              size='small'
              theme='borderless'
              disabled={!promptGuide}
              onClick={() =>
                draft.patchTabConfig(category, tab.key, { promptGuide: '' })
              }
            >
              {t('恢复默认')}
            </Button>
            <Button
              size='small'
              theme='borderless'
              onClick={() =>
                draft.patchTabConfig(category, tab.key, {
                  promptGuide: defaultGuide,
                })
              }
            >
              {t('填入默认内容以便修改')}
            </Button>
          </div>
        )}
        <Text type='tertiary' size='small' className='block mt-2'>
          {defaultGuide
            ? t(
                '本玩法有内置建议（占位符里就是它），留空即用它，后续版本调优会自动跟随；改写后则以此为准。展示在提示词输入框上方的问号里，鼠标移上去展开，换行原样保留。',
              )
            : t(
                '本玩法没有内置建议，留空=体验区不显示这个问号。填了以后展示在提示词输入框上方，鼠标移上去展开，换行原样保留，可以分条写。',
              )}
        </Text>
      </Card>

      {tab.promptOptimize && (
        <Card title={t('AI 优化提示词')}>
          {!globalOptimize.enabled || !globalOptimize.model ? (
            <Banner
              type='warning'
              closeIcon={null}
              description={t(
                '尚未在「通用设置」里开启并指定优化模型，本 tab 的按钮不会出现。',
              )}
              className='mb-3'
            />
          ) : null}
          <div className='flex items-center gap-2 mb-3'>
            <Switch
              size='small'
              checked={optimize.enabled}
              onChange={(v) =>
                draft.patchTabConfig(category, tab.key, {
                  promptOptimize: { ...optimize, enabled: v },
                })
              }
            />
            <Text>{t('在本 tab 的提示词框上显示「AI 优化提示词」按钮')}</Text>
          </div>
          <Text size='small'>{t('优化系统提示词（本 tab 通用方案）')}</Text>
          <TextArea
            rows={10}
            value={optimize.systemPrompt}
            onChange={(v) =>
              draft.patchTabConfig(category, tab.key, {
                promptOptimize: { ...optimize, systemPrompt: v },
              })
            }
            placeholder={defaultSystemPrompt}
            className='mt-1'
          />
          <div className='flex items-center gap-3 mt-2'>
            <Button
              size='small'
              theme='borderless'
              disabled={!optimize.systemPrompt}
              onClick={() =>
                draft.patchTabConfig(category, tab.key, {
                  promptOptimize: { ...optimize, systemPrompt: '' },
                })
              }
            >
              {t('恢复默认')}
            </Button>
            <Button
              size='small'
              theme='borderless'
              onClick={() =>
                draft.patchTabConfig(category, tab.key, {
                  promptOptimize: {
                    ...optimize,
                    systemPrompt: defaultSystemPrompt,
                  },
                })
              }
            >
              {t('填入默认内容以便修改')}
            </Button>
          </div>
          <Text type='tertiary' size='small' className='block mt-2'>
            {t(
              '这是本 tab 的通用方案：留空即使用内置默认（占位符里就是它），后续版本调优默认值时会自动跟随；改写后则以此为准。下方每个模型还可以各自定制一份（模型级优先于这里），不定制就用这份通用的。用哪个语言模型在「通用设置」里配，用户端不出模型选择器。',
            )}
          </Text>
          {tabEngine && !mixedEngines && (
            <Text type='tertiary' size='small' className='block mt-1'>
              {t(
                '本 tab 下都是 {{engine}} 模型，占位符与「填入默认内容」给的已经是该引擎族的专用模板。',
                { engine: engineLabel(tabEngine) },
              )}
            </Text>
          )}
          {mixedEngines && (
            <Text type='warning' size='small' className='block mt-1'>
              {t(
                '⚠️ 本 tab 同时挂着多个引擎族的模型，而这份系统提示词是 tab 级的、对它们一视同仁。占位符与「填入默认内容」给的是{{shown}}，一旦写进去，其余引擎族的模型也会被迫用它——各家模板形状彼此相反（MiniMax H3 要带字段名的分段结构、LTX-2.5 要长段单段落视听描述、通用版要一句话镜头描述），混用不报错、只是效果变差。两条路：这里留空（各模型按自己的引擎族取内置默认），或者到下方模型卡片里给需要特殊对待的模型单独定制一份（模型级优先于这里）。',
                { shown: mixedShownTemplate },
              )}
            </Text>
          )}
        </Card>
      )}

      <Card
        title={t('模型与参数')}
        headerExtraContent={
          <Text type='tertiary' size='small'>
            {t('共 {{count}} 个模型', { count: rows.length })}
          </Text>
        }
      >
        {display.enabled && rows.length === 0 && (
          <Banner
            type='warning'
            closeIcon={null}
            description={t(
              '这个 tab 开着但没有任何模型，用户进去会看到空的模型下拉框。',
            )}
            className='mb-3'
          />
        )}
        <div className='flex items-center gap-2 mb-4'>
          <Select
            filter
            allowCreate
            value={pending || undefined}
            optionList={candidates.map((n) => ({ label: n, value: n }))}
            onChange={(v) => setPending(v)}
            placeholder={t('选择或输入模型名')}
            style={{ width: 320 }}
          />
          <Button
            theme='solid'
            disabled={!pending}
            onClick={() => {
              draft.addModelToTab(storeKey, tab.key, pending);
              setPending('');
            }}
          >
            {t('添加到本 tab')}
          </Button>
        </div>

        {rows.length === 0 ? (
          <Empty
            description={
              <Text type='tertiary'>{t('还没有模型，先添加一个')}</Text>
            }
            style={{ padding: '16px 0' }}
          />
        ) : (
          rows.map(({ name, model, entry }) => (
            <div
              key={name}
              className='border border-gray-200 rounded-lg p-3 mb-3'
            >
              <div className='flex items-center justify-between mb-3'>
                <Text strong>{name}</Text>
                <Button
                  type='danger'
                  theme='borderless'
                  icon={<Trash2 size={16} />}
                  onClick={() =>
                    draft.removeModelFromTab(storeKey, tab.key, name)
                  }
                >
                  {t('从本 tab 移除')}
                </Button>
              </div>
              {/* 模型备注:这个模型在**本 tab 下**适合什么场景,直接显示在体验区的模型
                  下拉里。tab 级而非模型级 —— 同一模型在文生视频与图生视频下的适用场景
                  本就不同,合成一条只会写成放之四海皆准的废话。 */}
              <div className='mb-3'>
                <Text size='small'>{t('模型备注')}</Text>
                <Input
                  value={entry.note || ''}
                  onChange={(v) =>
                    draft.setTabField(storeKey, tab.key, name, 'note', v)
                  }
                  placeholder={t('如：写实人像效果好，出图快，适合批量试稿')}
                  className='mt-1'
                />
                <Text type='tertiary' size='small' className='block mt-1'>
                  {t(
                    '留空=不展示。填了以后展示在体验区的模型下拉选项与选中项下方，帮用户判断该用哪个模型。',
                  )}
                </Text>
              </div>
              {/* 模型级的优化系统提示词:不定制就跟随上面那份 tab 通用方案。
                  折叠起来是因为它是长文本、且多数模型不需要定制 —— 展开与否用
                  「已定制 / 跟随本 tab」的标签一眼可辨,不必逐个点开确认。
                  占位符按**这个模型自己的引擎族**取默认,与 tab 那份按全 tab 猜一个
                  引擎不同:模型卡片这一层本来就没有歧义。 */}
              {tab.promptOptimize && (
                <div className='mb-3'>
                  {draftPlanOnly(model) && (
                    <Text type='warning' size='small' className='block mb-1'>
                      {t(
                        '⚠️ 本模型走「一键生成方案」分支（ACE-Step：一次产出描述、歌词、BPM、调式与时长并回填左侧控件），体验区不会给它「AI 优化提示词」按钮——这里配的模板不会被用到。要用模型级模板，请把模型的引擎族声明为 MiniMax-Music3。',
                      )}
                    </Text>
                  )}
                  <Collapse>
                    <Collapse.Panel
                      itemKey='optimizePrompt'
                      header={
                        <span className='flex items-center gap-2'>
                          <Text size='small'>{t('AI 优化提示词')}</Text>
                          <Tag
                            size='small'
                            color={customizedPrompt(entry) ? 'blue' : 'grey'}
                          >
                            {customizedPrompt(entry)
                              ? t('已定制')
                              : t('跟随本 tab')}
                          </Tag>
                        </span>
                      }
                    >
                      <TextArea
                        rows={10}
                        value={entry.optimizePrompt || ''}
                        onChange={(v) =>
                          draft.setTabField(
                            storeKey,
                            tab.key,
                            name,
                            'optimizePrompt',
                            v,
                          )
                        }
                        placeholder={effectivePrompt(model)}
                      />
                      <div className='flex items-center gap-3 mt-2'>
                        <Button
                          size='small'
                          theme='borderless'
                          disabled={!customizedPrompt(entry)}
                          onClick={() =>
                            draft.setTabField(
                              storeKey,
                              tab.key,
                              name,
                              'optimizePrompt',
                              '',
                            )
                          }
                        >
                          {t('跟随本 tab')}
                        </Button>
                        <Button
                          size='small'
                          theme='borderless'
                          onClick={() =>
                            draft.setTabField(
                              storeKey,
                              tab.key,
                              name,
                              'optimizePrompt',
                              effectivePrompt(model),
                            )
                          }
                        >
                          {t('填入当前生效的内容以便修改')}
                        </Button>
                      </div>
                      <Text type='tertiary' size='small' className='block mt-2'>
                        {t(
                          '留空=用上面那份 tab 通用方案（占位符里就是当前对本模型实际生效的内容）。只在这个模型需要跟别人不一样时才填——典型是同一玩法下挂了不同引擎族的模型，各家要的模板形状本就相反。',
                        )}
                      </Text>
                    </Collapse.Panel>
                  </Collapse>
                </div>
              )}
              {/* 玩法声明:本 tab 覆盖多个门面 task_type 时,由运营指明这个模型是哪一个。
                  不走 fields —— 它不是参数,不该被 recomputeModelLevel 反推到模型级。 */}
              {tab.taskTypeChoices && (
                <div className='mb-3'>
                  <Text size='small'>{t('玩法')}</Text>
                  <Select
                    value={entry.taskType || ''}
                    optionList={[
                      { label: t('自动（按模型名判断）'), value: '' },
                      ...tab.taskTypeChoices.map((c) => ({
                        label: t(c.label),
                        value: c.value,
                      })),
                    ]}
                    onChange={(v) =>
                      draft.setTabField(storeKey, tab.key, name, 'taskType', v)
                    }
                    style={{ width: 260, display: 'block' }}
                    className='mt-1'
                  />
                </div>
              )}
              {tab.fields.length === 0 ? (
                <Text type='tertiary' size='small'>
                  {t('本玩法没有可调参数，加进来即可用。')}
                </Text>
              ) : (
                <div className='grid grid-cols-1 md:grid-cols-2 gap-4'>
                  {tab.fields.map((f) => (
                    <FieldInput
                      key={f}
                      field={f}
                      value={entry[f]}
                      lock={getTabFieldLock(
                        category,
                        tab.key,
                        f,
                        model?.engine,
                      )}
                      onChange={(v) =>
                        draft.setTabField(storeKey, tab.key, name, f, v)
                      }
                    />
                  ))}
                </div>
              )}
              {/* 画幅：一键填入实测过的推荐档 + 同时配了两套时的告警。
                  只对图像分类有意义（视频那边画幅走另一套字段）。 */}
              {category === 'image' && (
                <ImageShapeHints
                  modelName={name}
                  model={model}
                  tabKey={tab.key}
                  entry={entry}
                  onApply={(preset) => {
                    // 两套互斥：填 area 就清掉 sizes，反之亦然 —— 否则一键填完
                    // 立刻触发下面那条"配了两套"的告警，等于自己造矛盾。
                    draft.setTabField(
                      storeKey,
                      tab.key,
                      name,
                      'aspectRatios',
                      preset.aspectRatios,
                    );
                    draft.setTabField(
                      storeKey,
                      tab.key,
                      name,
                      'sizeTiers',
                      preset.sizeTiers,
                    );
                    draft.setTabField(
                      storeKey,
                      tab.key,
                      name,
                      'sizes',
                      preset.sizes,
                    );
                    draft.setModelField(
                      storeKey,
                      name,
                      'sizeAlign',
                      preset.sizeAlign ?? null,
                    );
                  }}
                />
              )}
              {modelLevelFields.length > 0 && (
                <div className='mt-3 pt-3 border-t border-gray-100 flex flex-wrap items-center gap-6'>
                  {modelLevelFields.map((f) =>
                    // 超分档位是复合结构（多行、每行三个联动下拉），塞不进这排
                    // 单控件的 flex 行，独占一行渲染。
                    f.type === 'upscale' ? (
                      <div key={f.key} className='w-full'>
                        <div className='flex items-center gap-2 mb-1'>
                          <Text size='small'>{t(f.label)}</Text>
                          <Text type='tertiary' size='small'>
                            {t('（模型级，对该模型的所有玩法生效）')}
                          </Text>
                        </div>
                        <UpscaleField
                          value={model[f.key]}
                          onChange={(v) =>
                            draft.setModelField(storeKey, name, f.key, v)
                          }
                          models={store?.models}
                          defaults={store?.defaults}
                          // 勾了「高分辨率档用纯放大」时，超分模型那一格不再被调用
                          // （放大由引擎在出片前做）。置灰并标注，但**不能清空**：
                          // 档位本身是由这些规则定义的，清了 1080P / 2K 会一起消失。
                          srModelUnused={!!model.nativeDelivery}
                          // 起步档候选必须与体验区完全同口径（tab 级 → 模型级 →
                          // 分类默认值），所以直接用体验区那支取值函数。手写
                          // entry.sizes || model.sizes 会漏掉分类默认值那一层：
                          // 管理端显示「无可用起步档」而体验区照样推得出来。
                          nativeSizes={getSizesForVideoModel(
                            { models: store?.models, default: store?.defaults },
                            name,
                            tab.key,
                          )}
                        />
                        {f.help && (
                          <Text
                            type='tertiary'
                            size='small'
                            className='block mt-1'
                          >
                            {t(f.help)}
                          </Text>
                        )}
                      </div>
                    ) : (
                      <div key={f.key} className='flex items-center gap-2'>
                        {/* 按 f.type 渲染。原来这里硬编码 Switch、完全忽略 type，
                          加一个非布尔字段（如引擎族 select）会渲染成一个永远
                          不勾选的开关，且写回 true/false 把配置写坏。 */}
                        {f.type === 'select' ? (
                          <Select
                            size='small'
                            style={{ minWidth: 260 }}
                            value={model[f.key] || ''}
                            optionList={f.options || []}
                            onChange={(v) =>
                              draft.setModelField(
                                storeKey,
                                name,
                                f.key,
                                v || '',
                              )
                            }
                          />
                        ) : f.type === 'int' ? (
                          <InputNumber
                            size='small'
                            min={1}
                            style={{ width: 150 }}
                            value={
                              model[f.key] == null ? undefined : model[f.key]
                            }
                            placeholder={t(f.placeholder || '留空=默认')}
                            onChange={(v) =>
                              draft.setModelField(
                                storeKey,
                                name,
                                f.key,
                                v === '' || v == null ? null : Number(v),
                              )
                            }
                          />
                        ) : (
                          <Switch
                            size='small'
                            checked={model[f.key] === true}
                            onChange={(v) =>
                              draft.setModelField(storeKey, name, f.key, v)
                            }
                          />
                        )}
                        <Text size='small'>{t(f.label)}</Text>
                        <Text type='tertiary' size='small'>
                          {t('（模型级，对该模型的所有玩法生效）')}
                        </Text>
                      </div>
                    ),
                  )}
                </div>
              )}
            </div>
          ))
        )}
      </Card>
    </div>
  );
};

export default TabPanel;
