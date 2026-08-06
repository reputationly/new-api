import { useCallback, useContext, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { StatusContext } from '../../context/Status';
import { API, showError, showInfo } from '../../helpers';
import {
  parsePlaygroundTabConfig,
  getPlaygroundTab,
  getPromptOptimizeGlobal,
  getTabPromptOptimize,
} from '../../constants/playgroundAdmin.constants';
import { defaultOptimizeSystemPrompt } from '../../constants/promptOptimize.constants';

// 「AI 优化提示词」共用 hook(图像/视频/音效体验区共用一份)。
//
// 与文生音乐的「AI 帮我写词」同一条链路(单次非流式打 /pg/chat/completions,后端按
// 会话身份注入上游 key),但刻意不给用户模型选择器:优化用哪个语言模型是运营的事,
// 配在「体验区管理 → 通用设置」,用户只看到一个按钮。没配模型 / 没开总开关 / 该 tab
// 单独关掉时 available=false,调用方据此不渲染按钮 —— 而不是渲染一个点了报错的按钮。
//
// 系统提示词优先用运营为该 tab 改写的版本,留空则用内置默认
// (constants/promptOptimize.constants.js,按 tab 分开写)。
//
// 返回 { available, optimizing, optimize }。optimize(text) 成功返回优化后的字符串,
// 失败返回 null 并已弹过错误提示 —— 调用方只需判空后回填输入框。
export const usePromptOptimize = (category, tabKey) => {
  const { t } = useTranslation();
  const [statusState] = useContext(StatusContext);
  const [optimizing, setOptimizing] = useState(false);
  const raw = statusState?.status?.PlaygroundTabConfig;

  const { available, model, systemPrompt } = useMemo(() => {
    const cfg = parsePlaygroundTabConfig(raw);
    const global = getPromptOptimizeGlobal(cfg);
    const tab = getTabPromptOptimize(cfg, category, tabKey);
    // 中央元数据里没声明 promptOptimize 的 tab 一律不出按钮:语音四个玩法输入的是
    // 待合成文本(改写=改内容,不是优化),文生音乐已有更专的「AI 帮我写词」。
    const declared =
      getPlaygroundTab(category, tabKey)?.promptOptimize === true;
    return {
      available: declared && global.enabled && !!global.model && tab.enabled,
      model: global.model,
      systemPrompt:
        (tab.systemPrompt || '').trim() || defaultOptimizeSystemPrompt(tabKey),
    };
  }, [raw, category, tabKey]);

  const optimize = useCallback(
    async (rawText) => {
      const text = (rawText || '').trim();
      if (!text) {
        // 空输入不是错误，是「还没轮到我」：优化是补全而不是凭空创作，得先有个方向。
        showInfo(
          t('先写个大概方向，比如「一只猫在窗台上打盹」，AI 再帮你补细节'),
        );
        return null;
      }
      if (!available) return null;
      setOptimizing(true);
      try {
        // group 不下发:分组是用户维度的,让后端按用户默认分组走,与体验区其它调用一致。
        const res = await API.post(
          '/pg/chat/completions',
          {
            model,
            stream: false,
            messages: [
              { role: 'system', content: systemPrompt },
              { role: 'user', content: text },
            ],
          },
          { skipErrorHandler: true },
        );
        // 模型偶尔会包一层 ``` 围栏或首尾引号,剥掉再回填。
        const out = (res?.data?.choices?.[0]?.message?.content || '')
          .trim()
          .replace(/^```(?:\w+)?\s*/i, '')
          .replace(/\s*```$/, '')
          .replace(/^["'“”]+|["'“”]+$/g, '')
          .trim();
        if (!out) {
          showError(t('提示词优化失败:模型未返回内容'));
          return null;
        }
        return out;
      } catch (e) {
        showError(
          t('提示词优化失败:') +
            (e?.response?.data?.error?.message || e?.message || ''),
        );
        return null;
      } finally {
        setOptimizing(false);
      }
    },
    [available, model, systemPrompt, t],
  );

  return { available, optimizing, optimize };
};
