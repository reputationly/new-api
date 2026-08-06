import React, { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Card, Spin, Tag, Typography } from '@douyinfe/semi-ui';
import { AlertTriangle } from 'lucide-react';
import {
  PLAYGROUND_CATEGORIES,
  getTabDisplay,
  getTabStoreKey,
  resolvePlaygroundTabs,
} from '../../constants/playgroundAdmin.constants';
import SettingsSidebarModulesAdmin from '../Setting/Operation/SettingsSidebarModulesAdmin';
import { usePlaygroundAdminDraft } from './usePlaygroundAdminDraft';
import GlobalPanel from './GlobalPanel';
import CategoryPanel from './CategoryPanel';
import TabPanel from './TabPanel';

const { Text, Title } = Typography;

// 「体验区管理」——以 tab（玩法）为中心。
//
// 改造前这页是四份 ModelConfig 的编辑器摞在一起：一行一个模型，横着摆满尺寸/时长/
// 上传上限/能力标签……一个模型挂多个能力时这一行参数被所有玩法共用，运营既分不清哪个
// 字段对哪个玩法生效，改一处还会串到别的玩法。
//
// 现在左边是「分类 → 玩法」，右边只渲染该玩法在中央元数据里声明的字段
// （playgroundAdmin.constants.js 的 fields，同一份 schema 也喂给体验区决定显示哪些
// 控件），模型按玩法挂载。模型级平铺字段与能力标签保存时由 tabs 反推，不再手工维护。
const PlaygroundAdmin = () => {
  const { t } = useTranslation();
  const draft = usePlaygroundAdminDraft();
  const [nav, setNav] = useState({ type: 'global' });

  const categories = useMemo(
    () => PLAYGROUND_CATEGORIES.filter((c) => c.tabs.length > 0),
    [],
  );

  // 每个 tab 挂了几个模型：侧栏直接标出来，开着却是 0 的一眼可见。
  const modelCount = (category, tabKey) => {
    const storeKey = getTabStoreKey(category, tabKey);
    const models = draft.stores[storeKey]?.models || {};
    return Object.values(models).filter((m) => m.tabs?.[tabKey] !== undefined)
      .length;
  };

  const navItem = (active, onClick, content, indent = false) => (
    <div
      onClick={onClick}
      className={`cursor-pointer rounded-md px-3 py-2 text-sm flex items-center justify-between gap-2 ${
        indent ? 'ml-3' : 'font-medium'
      } ${active ? 'bg-blue-50 text-blue-600' : 'hover:bg-gray-50'}`}
    >
      {content}
    </div>
  );

  const renderPanel = () => {
    if (nav.type === 'sidebar') {
      return (
        <div className='flex flex-col gap-3'>
          <Card>
            <Title heading={5}>{t('分类显示')}</Title>
            <Text type='tertiary' size='small'>
              {t(
                '控制左侧边栏各模块（含体验区各分类）的显示。本区块有独立的保存按钮。',
              )}
            </Text>
          </Card>
          <SettingsSidebarModulesAdmin
            options={draft.options}
            refresh={draft.reload}
          />
        </div>
      );
    }
    if (nav.type === 'category') {
      const cat = categories.find((c) => c.key === nav.category);
      return cat ? <CategoryPanel category={cat} draft={draft} /> : null;
    }
    if (nav.type === 'tab') {
      const cat = categories.find((c) => c.key === nav.category);
      const tab = cat?.tabs.find((tb) => tb.key === nav.tabKey);
      return tab ? (
        <TabPanel
          key={`${cat.key}/${tab.key}`}
          category={cat.key}
          tab={tab}
          draft={draft}
        />
      ) : null;
    }
    return <GlobalPanel draft={draft} />;
  };

  return (
    <div className='mt-[64px] px-3 pb-6'>
      <Card className='mb-3'>
        <div className='flex items-start justify-between gap-4 flex-wrap'>
          <div>
            <Title heading={4}>{t('体验区管理')}</Title>
            <Text type='tertiary'>
              {t(
                '按玩法配置体验区：每个玩法一格独立配置，只出现该玩法真正用得到的参数。模型的能力标签由「挂在哪些玩法下」自动派生，无需另勾。配置对网页端、移动端与直连接口同时生效。',
              )}
            </Text>
          </div>
          <div className='flex items-center gap-2'>
            {draft.dirty.size > 0 && (
              <Text type='warning' size='small'>
                {t('有 {{count}} 项未保存', { count: draft.dirty.size })}
              </Text>
            )}
            <Button
              theme='borderless'
              disabled={!draft.dirty.size || draft.saving}
              onClick={draft.reset}
            >
              {t('放弃修改')}
            </Button>
            <Button
              theme='solid'
              loading={draft.saving}
              disabled={!draft.dirty.size}
              onClick={draft.save}
            >
              {t('保存')}
            </Button>
          </div>
        </div>
      </Card>

      <Spin spinning={draft.loading}>
        <div className='flex gap-3 items-start flex-col lg:flex-row'>
          <Card className='w-full lg:w-64 flex-shrink-0'>
            {navItem(
              nav.type === 'global',
              () => setNav({ type: 'global' }),
              <span>{t('通用设置')}</span>,
            )}
            {navItem(
              nav.type === 'sidebar',
              () => setNav({ type: 'sidebar' }),
              <span>{t('分类显示')}</span>,
            )}
            {categories.map((cat) => (
              <div key={cat.key} className='mt-2'>
                {navItem(
                  nav.type === 'category' && nav.category === cat.key,
                  () => setNav({ type: 'category', category: cat.key }),
                  <span>{t(cat.label)}</span>,
                )}
                {resolvePlaygroundTabs(cat.key, draft.tabConfig).map((tb) => {
                  const count = modelCount(cat.key, tb.key);
                  const display = getTabDisplay(
                    draft.tabConfig,
                    cat.key,
                    tb.key,
                  );
                  return (
                    <React.Fragment key={tb.key}>
                      {navItem(
                        nav.type === 'tab' &&
                          nav.category === cat.key &&
                          nav.tabKey === tb.key,
                        () =>
                          setNav({
                            type: 'tab',
                            category: cat.key,
                            tabKey: tb.key,
                          }),
                        <>
                          <span
                            className={display.enabled ? '' : 'line-through'}
                          >
                            {t(tb.label)}
                          </span>
                          <span className='flex items-center gap-1'>
                            {display.enabled && count === 0 && (
                              <AlertTriangle
                                size={13}
                                className='text-amber-500'
                              />
                            )}
                            <Tag size='small' color={count ? 'blue' : 'grey'}>
                              {count}
                            </Tag>
                          </span>
                        </>,
                        true,
                      )}
                    </React.Fragment>
                  );
                })}
              </div>
            ))}
          </Card>

          <div className='flex-1 min-w-0 w-full'>{renderPanel()}</div>
        </div>
      </Spin>
    </div>
  );
};

export default PlaygroundAdmin;
