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

  // 「用户可用分组」表（运营设置里那张）——它决定一个分组是不是「所有人都能访问」。
  // GetUserUsableGroups(userGroup) 就是从这张表出发，再叠加各分组的 +:/-: 特殊增删。
  const universalGroups = useMemo(() => {
    try {
      const parsed = JSON.parse(draft.options?.UserUsableGroups || '{}');
      return parsed && typeof parsed === 'object' ? Object.keys(parsed) : [];
    } catch (e) {
      return [];
    }
  }, [draft.options]);

  const pickedModel = useMemo(
    () => (draft.allModels || []).find((m) => m.model_name === global.model),
    [draft.allModels, global.model],
  );

  // 'all' 是后端的哨兵（controller/pricing.go:23），语义是「该模型不限分组」。
  // 此时任何分组都能服务它，**不能**做「该分组挂没挂这个模型」的校验——
  // 把 'all' 塌成通用表会让手填的受限分组被误报成「每次优化都会失败」，
  // 那会吓得运营去删掉一个本来正确、只是限定给部分人的配置。
  const modelServesAnyGroup = !!pickedModel?.enable_groups?.includes('all');

  // 候选分组跟着**已选模型**走。取全部模型 enable_groups 的并集会让运营选到一个
  // 根本不挂优化模型的分组，保存后每次请求都在选渠道时炸。
  // 'all' 时给通用分组当**建议值**（非穷举，靠 allowCreate 手填其余）。
  const modelGroups = useMemo(() => {
    if (!pickedModel) return null; // 没选模型 / 拉不到候选：不给候选，靠手填
    return modelServesAnyGroup
      ? universalGroups
      : (pickedModel.enable_groups || []).filter(Boolean);
  }, [pickedModel, modelServesAnyGroup, universalGroups]);

  const groupOptions = useMemo(
    () =>
      [...new Set(modelGroups || [])].sort().map((g) => ({
        label: universalGroups.includes(g) ? g : `${g}${t('（非通用）')}`,
        value: g,
      })),
    [modelGroups, universalGroups, t],
  );

  // 配了分组但它不在通用表里 → 只有被特殊配置显式加过该分组的用户才用得了。
  // 不阻止保存（运营可能就是想只给部分人用），只提示。
  const groupNotUniversal =
    !!global.group && !universalGroups.includes(global.group);

  // 换过模型之后，原来选的分组可能已经不在新模型的可用分组里。不自动清空
  // （避免运营手滑丢配置），只提醒——这种组合下每次优化请求都会失败。
  const groupNotServingModel =
    !!global.group &&
    !!global.model &&
    !modelServesAnyGroup &&
    Array.isArray(modelGroups) &&
    !modelGroups.includes(global.group);

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
        <div style={{ maxWidth: 360 }} className='mt-4'>
          <Text size='small'>{t('优化用的分组')}</Text>
          <Select
            filter
            allowCreate
            showClear
            value={global.group || undefined}
            optionList={groupOptions}
            onChange={(v) => patchGlobal({ group: v || '' })}
            placeholder={
              global.model
                ? t('留空则按用户自己的分组')
                : t('请先选择优化用的语言模型')
            }
            style={{ width: '100%', marginTop: 4 }}
          />
          <Text type='tertiary' size='small' className='block mt-1'>
            {modelServesAnyGroup
              ? t(
                  '该模型不限分组，下拉里只是建议值，可直接输入任意分组。建议挑一个所有用户都能访问的。',
                )
              : t(
                  '候选来自所选模型实际启用的分组。建议挑一个所有用户都能访问的：优化模型通常是便宜的小模型、挂在通用分组上，而专用分组往往只挂业务模型 —— 留空走用户自己的分组时，分组越专用的用户反而越用不了这个功能。',
                )}
          </Text>
          {groupNotServingModel && (
            <Text type='danger' size='small' className='block mt-1'>
              {t(
                '模型「{{model}}」未在分组「{{group}}」启用：这样配的话每次优化都会失败，请重选分组。',
                { model: global.model, group: global.group },
              )}
            </Text>
          )}
          {groupNotUniversal && (
            <Text type='warning' size='small' className='block mt-1'>
              {t(
                '「{{group}}」不在「用户可用分组」表中：只有本身就在该分组、或在分组特殊配置里被显式授予的用户才能使用。',
                { group: global.group },
              )}
            </Text>
          )}
        </div>
        <Text type='tertiary' size='small' className='block mt-2'>
          {t(
            '未开总开关、未选模型或该 tab 单独关掉时，「AI 优化提示词」按钮一律不出现（而不是点了报错）。分组权限则无法在前端可靠判断，配错时用户会收到一句「请联系管理员」的提示。每次优化是一次普通的非流式对话请求，按所选分组的倍率正常计费到用户头上。',
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
