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

  // 分组候选 = 分组倍率表全集（draft.allGroups，来自 /api/group/）。
  // 标注哪些在「用户可用分组」表里：不在的分组只有本身属于它、或被 +: 显式授予的
  // 用户才用得了，配上去等于把这个功能限定给一部分人。
  const groupOptions = useMemo(
    () =>
      [...new Set(draft.allGroups || [])].sort().map((g) => ({
        label: universalGroups.includes(g) ? g : `${g}${t('（非通用）')}`,
        value: g,
      })),
    [draft.allGroups, universalGroups, t],
  );

  // 模型候选跟着**已选分组**走——与用户原来在体验区的操作顺序、以及运行时
  // distributor 按 (group, model) 找渠道的顺序都一致，选出来的组合天然有效。
  // 'all' 是后端哨兵（controller/pricing.go:23），语义是「该模型不限分组」，任何分组都放行。
  // 分组留空 = 按用户自己的分组走，此时给不出确定的分组，列全集。
  //
  // 再叠一层 chat 过滤：优化请求固定打 /pg/chat/completions，选了视频/图像模型这里
  // 看不出异常（有值 → 各 tab 的按钮照常显示），用户每次点都会在上游炸。
  // allowCreate 保留：接口拉不到时候选为空，手填是唯一退路。
  const modelOptions = useMemo(
    () =>
      (draft.allModels || [])
        .filter((m) => isChatModel(m.supported_endpoint_types))
        .filter(
          (m) =>
            !global.group ||
            (m.enable_groups || []).some(
              (g) => g === global.group || g === 'all',
            ),
        )
        .map((m) => m.model_name)
        .filter(Boolean)
        .sort()
        .map((n) => ({ label: n, value: n })),
    [draft.allModels, global.group],
  );

  // 配了分组但它不在通用表里 → 只有被特殊配置显式加过该分组的用户才用得了。
  // 不阻止保存（运营可能就是想只给部分人用），只提示。
  const groupNotUniversal =
    !!global.group && !universalGroups.includes(global.group);

  // 候选可信与否只看**源数据**拉到没有。
  // 不能拿 modelOptions.length > 0 当哨兵：它为空有两种成因——接口没拉到（不该判），
  // 与该分组下确实没有 chat 模型（正该判）。用过滤结果做哨兵会把后者一起吞掉，
  // 而那恰恰是唯一会在运行时炸、页面上又再无别处提示的错误配置
  // （usePromptOptimize 的 available 判据刻意不含分组校验，前端判不了分组权限）。
  const candidatesLoaded = (draft.allModels || []).length > 0;

  // 分组下一个能做优化的 chat 模型都没有 → 换模型无济于事，得换分组。
  const groupHasNoChatModel =
    !!global.group && candidatesLoaded && modelOptions.length === 0;

  // 分组下有 chat 模型，只是当前选的这个不在其中（多见于换过分组之后）→ 换模型即可。
  // 不自动清空（避免运营手滑丢配置），只提醒。
  const modelNotInGroup =
    !!global.model &&
    !!global.group &&
    candidatesLoaded &&
    modelOptions.length > 0 &&
    !modelOptions.some((o) => o.value === global.model);

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
          <Text size='small'>{t('优化用的分组')}</Text>
          <Select
            filter
            allowCreate
            showClear
            value={global.group || undefined}
            optionList={groupOptions}
            onChange={(v) => patchGlobal({ group: v || '' })}
            placeholder={t('留空则按用户自己的分组')}
            style={{ width: '100%', marginTop: 4 }}
          />
          <Text type='tertiary' size='small' className='block mt-1'>
            {t(
              '统一指定优化请求走哪个分组，用户端不再自己选。建议挑一个所有用户都能访问的：优化模型通常是便宜的小模型、挂在通用分组上，而专用分组往往只挂业务模型 —— 留空走用户自己的分组时，分组越专用的用户反而越用不了这个功能。',
            )}
          </Text>
          {groupHasNoChatModel && (
            <Text type='danger' size='small' className='block mt-1'>
              {t(
                '分组「{{group}}」下没有任何支持 chat completions 的模型：这样配的话每次优化都会失败，请换一个分组。',
                { group: global.group },
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
        <div style={{ maxWidth: 360 }} className='mt-4'>
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
            {global.group
              ? t(
                  '只列出在分组「{{group}}」下已启用、且支持 chat completions 的模型 —— 优化请求固定走 /pg/chat/completions。',
                  { group: global.group },
                )
              : t(
                  '只列出支持 chat completions 的模型 —— 优化请求固定走 /pg/chat/completions。分组留空时无法确定范围，这里列的是全部。',
                )}
          </Text>
          {modelNotInGroup && (
            <Text type='danger' size='small' className='block mt-1'>
              {t(
                '模型「{{model}}」未在分组「{{group}}」启用：这样配的话每次优化都会失败，请重选。',
                { model: global.model, group: global.group },
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
