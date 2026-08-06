import React, { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Card, Empty, Tag, Typography } from '@douyinfe/semi-ui';
import {
  capabilitiesWithoutTab,
  listTabsByStoreKey,
  orphanFields,
} from '../../constants/playgroundAdmin.constants';
import { STORE_META } from './usePlaygroundAdminDraft';
import FieldInput from './FieldInput';

const { Text, Title } = Typography;

// 分类级面板：这份 ModelConfig 的兜底默认值 + 一张「按模型」交叉表。
//
// 交叉表是给运营做自检用的：新页面按 tab 编辑，看不到「同一个模型到底挂了几个玩法」，
// 也看不到配置里那些没有对应玩法的历史能力标签（如图像的「图像编辑」）——这些标签在
// 模型广场仍会展示，保存时原样保留，但体验区没有对应入口，容易看着像丢了配置。
//
// 它同时是「孤儿字段」唯一的编辑入口（见 orphanFields）：没挂任何玩法的模型（超分
// seedvr2）和没有玩法认领的字段（音乐的 videoMaxMB）都收在这里。这些值服务端护栏
// 照读不误，不给编辑入口就成了暗配置。
const CategoryPanel = ({ category, draft }) => {
  const { t } = useTranslation();
  const storeKey = category.configKey;
  const store = draft.stores[storeKey];
  const meta = STORE_META[storeKey];
  const owned = useMemo(() => listTabsByStoreKey(storeKey), [storeKey]);
  const labelByTabKey = useMemo(
    () => new Map(owned.map((x) => [x.tab.key, x.tab.label])),
    [owned],
  );

  const rows = useMemo(
    () =>
      Object.entries(store?.models || {})
        .map(([name, m]) => ({
          name,
          tabs: Object.keys(m.tabs || {}),
          orphanCaps: capabilitiesWithoutTab(storeKey, m.capabilities),
          orphanFields: orphanFields(storeKey, m),
          model: m,
        }))
        .sort((a, b) => a.name.localeCompare(b.name)),
    [store, storeKey],
  );

  return (
    <div className='flex flex-col gap-3'>
      <Card>
        <Title heading={5}>{t(category.label)}</Title>
        <Text type='tertiary' size='small'>
          {t('配置存放于')} <Text code>{storeKey}</Text>
        </Text>
      </Card>

      <Card title={t('分类默认值')}>
        <Text type='tertiary' size='small' className='block mb-3'>
          {t(
            '模型在某个 tab 下没配的字段，先落到模型级、再落到这里的默认值，最后才是内置兜底。给同类模型统一口径时改这里，不用逐个模型填。',
          )}
        </Text>
        <div className='grid grid-cols-1 md:grid-cols-2 gap-4'>
          {(meta?.defaultFields || []).map((f) => (
            <FieldInput
              key={f}
              field={f}
              value={store?.defaults?.[f]}
              onChange={(v) => draft.setDefaultField(storeKey, f, v)}
            />
          ))}
        </div>
      </Card>

      <Card title={t('按模型交叉检查')}>
        {rows.length === 0 ? (
          <Empty
            description={
              <Text type='tertiary'>{t('这份配置里还没有模型')}</Text>
            }
            style={{ padding: '16px 0' }}
          />
        ) : (
          rows.map((r) => (
            <div
              key={r.name}
              className='py-2 border-b border-gray-100 last:border-0'
            >
              <div className='flex flex-wrap items-center gap-2'>
                <Text strong style={{ minWidth: 220 }}>
                  {r.name}
                </Text>
                {r.tabs.length === 0 && (
                  <Tag color='grey' size='small'>
                    {t('未挂任何玩法')}
                  </Tag>
                )}
                {r.tabs.map((k) => (
                  <Tag key={k} color='blue' size='small'>
                    {t(labelByTabKey.get(k) || k)}
                  </Tag>
                ))}
                {r.orphanCaps.map((c) => (
                  <Tag key={c} color='amber' size='small'>
                    {t(c)}
                    {t('（无体验区玩法，仅模型广场展示）')}
                  </Tag>
                ))}
              </div>
              {r.orphanFields.length > 0 && (
                <div className='mt-2 ml-1 pl-3 border-l-2 border-amber-200'>
                  <Text type='tertiary' size='small' className='block mb-2'>
                    {t(
                      '以下参数没有对应玩法认领，体验区不展示，但直连请求仍受它约束——在这里改。',
                    )}
                  </Text>
                  <div className='grid grid-cols-1 md:grid-cols-2 gap-4'>
                    {r.orphanFields.map((f) => (
                      <FieldInput
                        key={f}
                        field={f}
                        value={r.model[f]}
                        onChange={(v) =>
                          draft.setModelField(storeKey, r.name, f, v)
                        }
                      />
                    ))}
                  </div>
                </div>
              )}
            </div>
          ))
        )}
      </Card>
    </div>
  );
};

export default CategoryPanel;
