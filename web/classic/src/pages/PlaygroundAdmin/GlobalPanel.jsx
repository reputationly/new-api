import React, { useMemo } from 'react';
import { useTranslation } from 'react-i18next';
import { Card, Select, Switch, Typography } from '@douyinfe/semi-ui';
import {
  PLAYGROUND_CATEGORIES,
  PLAYGROUND_GLOBAL_KEY,
  getPromptOptimizeGlobal,
  getTabPromptOptimize,
} from '../../constants/playgroundAdmin.constants';
import { isChatModel } from '../../helpers/playground';

const { Text, Title } = Typography;

// 跨 tab 的通用设置。目前只有「AI 优化提示词」用哪个语言模型——刻意做成全局：
// 用户端不给模型选择器（选模型是运营的事，用户只按一个按钮），各 tab 只保留「开不开」
// 与「用什么系统提示词」。
const GlobalPanel = ({ draft }) => {
  const { t } = useTranslation();
  const global = getPromptOptimizeGlobal(draft.tabConfig);

  // 只列 chat 能力的模型：优化请求固定打 /pg/chat/completions，选了视频/图像模型
  // 这里看不出异常（有值 → 各 tab 的按钮照常显示），用户每次点都会在上游炸。
  // allowCreate 保留：/api/pricing 拉不到时 allModels 为空，手填是唯一退路。
  const modelOptions = useMemo(
    () =>
      (draft.allModels || [])
        .filter((m) => isChatModel(m.supported_endpoint_types))
        .map((m) => m.model_name)
        .filter(Boolean)
        .sort()
        .map((n) => ({ label: n, value: n })),
    [draft.allModels],
  );

  // 各 tab 的开关状态一览：全局开着但某个 tab 单独关了，这里能一眼看出来。
  const tabStates = useMemo(() => {
    const out = [];
    PLAYGROUND_CATEGORIES.forEach((cat) => {
      cat.tabs.forEach((tab) => {
        if (!tab.promptOptimize) return;
        const cfg = getTabPromptOptimize(draft.tabConfig, cat.key, tab.key);
        out.push({
          key: `${cat.key}/${tab.key}`,
          label: `${t(cat.label)} · ${t(tab.label)}`,
          enabled: cfg.enabled,
          custom: !!cfg.systemPrompt,
        });
      });
    });
    return out;
  }, [draft.tabConfig, t]);

  const patchGlobal = (patch) =>
    draft.patchTabConfig(PLAYGROUND_GLOBAL_KEY, 'promptOptimize', {
      ...global,
      ...patch,
    });

  return (
    <div className='flex flex-col gap-3'>
      <Card>
        <Title heading={5}>{t('通用设置')}</Title>
        <Text type='tertiary' size='small'>
          {t('跨分类生效的体验区设置。')}
        </Text>
      </Card>

      <Card title={t('AI 优化提示词')}>
        <div className='flex items-center gap-2 mb-4'>
          <Switch
            size='small'
            checked={global.enabled}
            onChange={(v) => patchGlobal({ enabled: v })}
          />
          <Text>{t('总开关')}</Text>
        </div>
        <div style={{ maxWidth: 360 }}>
          <Text size='small'>{t('优化用的语言模型')}</Text>
          <Select
            filter
            allowCreate
            value={global.model || undefined}
            optionList={modelOptions}
            onChange={(v) => patchGlobal({ model: v || '' })}
            placeholder={t('选择或输入模型名')}
            style={{ width: '100%', marginTop: 4 }}
          />
          <Text type='tertiary' size='small' className='block mt-1'>
            {t(
              '只列出支持 chat completions 的模型 —— 优化请求固定走 /pg/chat/completions。',
            )}
          </Text>
        </div>
        <Text type='tertiary' size='small' className='block mt-2'>
          {t(
            '不配分组：按用户自己的默认分组计费，与体验区其它调用一致。未开总开关或未选模型时，各 tab 的「AI 优化提示词」按钮一律不出现（而不是点了报错）。每次优化是一次普通的非流式对话请求，按该模型正常计费。',
          )}
        </Text>

        <div className='mt-4 pt-3 border-t border-gray-100'>
          <Text size='small' strong>
            {t('各 tab 状态')}
          </Text>
          <div className='flex flex-wrap gap-x-6 gap-y-1 mt-2'>
            {tabStates.map((s) => (
              <Text key={s.key} type='tertiary' size='small'>
                {s.label}：{s.enabled ? t('开') : t('关')}
                {s.custom ? t('（已改写提示词）') : t('（默认提示词）')}
              </Text>
            ))}
          </div>
        </div>
      </Card>
    </div>
  );
};

export default GlobalPanel;
