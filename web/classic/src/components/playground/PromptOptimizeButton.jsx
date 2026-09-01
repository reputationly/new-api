import React, { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { usePromptOptimize } from '../../hooks/common/usePromptOptimize';
import AiAssistButton from './AiAssistButton';

// 「AI 优化提示词」按钮。外观与交互来自 AiAssistButton，与音乐页 ACE-Step 的同名按钮
// （draftPlan 分支，见 MusicChatArea）共用一份。
//
// 运营没开总开关 / 没配优化模型 / 单独关掉了这个 tab 时整体不渲染 —— 与其给一个点了
// 报「未配置」的按钮，不如让它不存在。配置读取与调用都在 usePromptOptimize 里，这里
// 只管把结果写回输入框。
//
// 输入框为空时按钮仍可点：点了给一句「先写个大概方向」的提示，比一个说不清为什么点不
// 动的灰按钮好懂。优化过程中按钮转圈并禁用，同时通过 onOptimizingChange 把状态抬给
// 调用方去灰掉发送按钮——这一步是真的不能发：请求在途，此刻发出去的还是没优化的原文。
//
// model 是当前选中的模型名：运营可以在体验区管理里给单个模型另写一份优化系统提示词
// （models[x].tabs[y].optimizePrompt），不写则跟随 tab 那份通用的。不传即只走 tab 级。
//
// engine / optimizeContext 只有视频体验区传（选中模型的引擎族、本次请求的输入形态与
// 时长），用来给 MiniMax H3 换一套分段结构的系统提示词；不传即维持原行为。
const PromptOptimizeButton = ({
  category,
  tabKey,
  value,
  onChange,
  disabled = false,
  onOptimizingChange,
  model,
  engine,
  optimizeContext,
}) => {
  const { t } = useTranslation();
  const { available, optimizing, optimize } = usePromptOptimize(
    category,
    tabKey,
    { engine, context: optimizeContext, model },
  );
  useEffect(() => {
    onOptimizingChange?.(optimizing);
  }, [optimizing, onOptimizingChange]);
  if (!available) return null;
  return (
    <AiAssistButton
      label={t('AI 优化提示词')}
      busyLabel={t('优化中…')}
      hint={t('把大白话补全成模型认的描述，结果会填回输入框，可再改')}
      busyHint={t('正在优化，请勿刷新或切换页面，否则要重新来一次')}
      busy={optimizing}
      disabled={disabled}
      onClick={async () => {
        const out = await optimize(value);
        if (out) onChange(out);
      }}
    />
  );
};

export default PromptOptimizeButton;
