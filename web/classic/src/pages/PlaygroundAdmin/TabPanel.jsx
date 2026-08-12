import React, { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Banner,
  Button,
  Card,
  Empty,
  Input,
  InputNumber,
  Select,
  Switch,
  TextArea,
  Typography,
} from '@douyinfe/semi-ui';
import { Trash2 } from 'lucide-react';
import {
  PLAYGROUND_MODEL_LEVEL_FIELDS,
  VIDEO_ENGINE_MINIMAX_H3,
  getTabDisplay,
  getTabPromptGuide,
  getTabPromptOptimize,
  getTabStoreKey,
  getPromptOptimizeGlobal,
} from '../../constants/playgroundAdmin.constants';
import { defaultOptimizeSystemPrompt } from '../../constants/promptOptimize.constants';
import FieldInput from './FieldInput';

const { Text, Title } = Typography;

// 单个 tab 的专用配置面板：这个 tab 显不显示、用哪些模型、每个模型在这个 tab 下的参数。
//
// 「专用」是重点：只渲染该 tab 在中央元数据里声明的 fields。同一个模型挂多个玩法时，
// 文生视频那格填的时长不会连带限制图生视频，数字人也不会再出现一个点了不生效的尺寸框。
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

  // 挂在本 tab 下的模型 = 在这份 ModelConfig 里显式声明了 tabs[tab.key] 的那些。
  const rows = useMemo(
    () =>
      Object.entries(store?.models || {})
        .filter(([, m]) => m.tabs?.[tab.key] !== undefined)
        .map(([name, m]) => ({ name, model: m, entry: m.tabs[tab.key] || {} }))
        .sort((a, b) => a.name.localeCompare(b.name)),
    [store, tab.key],
  );

  // 默认系统提示词要按引擎族取:H3 要的是带字段名的分段结构,与通用模板形状相反。
  // **占位符与「填入默认内容」两处都必须传 engine** —— 不传的话,挂着 H3 模型的 tab 上
  // 展示的是通用散文模板,按钮还会把这份错的直接填进输入框、邀请运营保存。那不是
  // 「运营可能配错」,是界面主动把错的递过去。
  const tabEngines = useMemo(
    () => new Set(rows.map((r) => r.model?.engine || '')),
    [rows],
  );
  const hasH3 = tabEngines.has(VIDEO_ENGINE_MINIMAX_H3);
  const defaultSystemPrompt = defaultOptimizeSystemPrompt(
    tab.key,
    hasH3 ? VIDEO_ENGINE_MINIMAX_H3 : '',
  );
  // 系统提示词是 tab 级的、只有一份,混挂两个引擎族时必然有一半模型拿到形状不对的
  // 模板 —— 这个只能提示,没法在这里替运营决定。
  const mixedEngines = hasH3 && tabEngines.size > 1;

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
          rows={6}
          value={promptGuide}
          onChange={(v) =>
            draft.patchTabConfig(category, tab.key, { promptGuide: v })
          }
          placeholder={t(
            '如：主体 + 动作 + 镜头 + 光线 + 风格，一句话一个要素；少用「唯美」「大片感」这类抽象形容词，多写看得见的东西',
          )}
        />
        <Text type='tertiary' size='small' className='block mt-2'>
          {t(
            '留空=体验区不显示这个问号。填了以后展示在本玩法的提示词输入框上方，鼠标移上去展开，换行会原样保留，可以分条写。',
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
          <Text size='small'>{t('优化系统提示词')}</Text>
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
              '留空即使用内置默认（占位符里就是它），后续版本调优默认值时会自动跟随；改写后则以此为准。用哪个语言模型在「通用设置」里配，用户端不出模型选择器。',
            )}
          </Text>
          {hasH3 && !mixedEngines && (
            <Text type='tertiary' size='small' className='block mt-1'>
              {t(
                '本 tab 下都是 MiniMax H3 模型，占位符与「填入默认内容」给的已经是 H3 那套带字段名的分段模板。',
              )}
            </Text>
          )}
          {mixedEngines && (
            <Text type='warning' size='small' className='block mt-1'>
              {t(
                '⚠️ 本 tab 同时挂着 MiniMax H3 与其它引擎族的模型，而系统提示词只有一份（tab 级）。占位符与「填入默认内容」给的是 H3 的分段模板，一旦写进去，非 H3 的模型也会用它——两种模板形状相反，混用不报错、只是效果变差。要么留空（此时各模型按自己的引擎族取内置默认），要么把它们拆到不同 tab。',
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
                      onChange={(v) =>
                        draft.setTabField(storeKey, tab.key, name, f, v)
                      }
                    />
                  ))}
                </div>
              )}
              {modelLevelFields.length > 0 && (
                <div className='mt-3 pt-3 border-t border-gray-100 flex flex-wrap items-center gap-6'>
                  {modelLevelFields.map((f) => (
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
                            draft.setModelField(storeKey, name, f.key, v || '')
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
                  ))}
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
