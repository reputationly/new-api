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
  getTabDisplay,
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
            placeholder={defaultOptimizeSystemPrompt(tab.key)}
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
                    systemPrompt: defaultOptimizeSystemPrompt(tab.key),
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
                      <Switch
                        size='small'
                        checked={model[f.key] === true}
                        onChange={(v) =>
                          draft.setModelField(storeKey, name, f.key, v)
                        }
                      />
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
