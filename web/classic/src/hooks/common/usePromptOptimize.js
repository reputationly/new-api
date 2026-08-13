import { useCallback, useContext, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { StatusContext } from '../../context/Status';
import { API, showError, showInfo } from '../../helpers';
import { isPlaygroundConfigIssue } from '../../helpers/playground';
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
// 会话身份注入上游 key),但刻意不给用户模型选择器:优化用哪个语言模型、走哪个分组
// 都是运营的事,配在「体验区管理 → 通用设置」,用户只看到一个按钮。
//
// available=false 时调用方不渲染按钮 —— 而不是渲染一个点了报错的按钮。三个理由:
// 没开总开关 / 没配模型 / 该 tab 单独关掉。
//
// **刻意不判「用户对配置分组有没有权限」**:唯一能拿到的判据 /api/user/self/groups
// 只列出**有倍率配置**的分组(controller/group.go:32),而后端放行的 GetUserUsableGroups
// 还包含 +: 特殊授予与用户自己的分组。前者是后者的子集,「不在列表里」推不出
// 「不可用」——照它藏按钮会把功能从本来能用的人眼前拿走,那比报个错糟得多。
// 分组配错时改为在 catch 里给一句能行动的提示。
//
// 系统提示词优先用运营为该 tab 改写的版本,留空则用内置默认
// (constants/promptOptimize.constants.js,按 tab 分开写)。
//
// 第三个参数是可选的模型上下文,只有视频体验区传:
//   engine  —— 所选模型的引擎族(minimax-h3 要的是分段结构,与通用契约形状相反);
//   context —— 本次请求的既成事实(传了首帧还是尾帧、选了几秒、有几张参考图)。
//              优化模型看不到左侧面板,不喂它就只能猜,猜错同样不报错、只是默默变差。
// 两者都留空即维持原行为(手机端 PromptBar、图像/音乐体验区都不传)。
//
// 返回 { available, optimizing, optimize }。optimize(text) 成功返回优化后的字符串,
// 失败返回 null 并已弹过错误提示 —— 调用方只需判空后回填输入框。
export const usePromptOptimize = (
  category,
  tabKey,
  { engine, context } = {},
) => {
  const { t } = useTranslation();
  const [statusState] = useContext(StatusContext);
  const [optimizing, setOptimizing] = useState(false);
  const raw = statusState?.status?.PlaygroundTabConfig;

  const { available, model, group, systemPrompt } = useMemo(() => {
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
      group: global.group,
      // context 无条件追加在末尾:它不是模板而是本次请求的事实,运营改写过模板时
      // 同样需要(改写的多半也是 H3 模板,少了这段照样分不清 I2VA / L2VA)。
      systemPrompt:
        ((tab.systemPrompt || '').trim() ||
          defaultOptimizeSystemPrompt(tabKey, engine)) + (context || ''),
    };
  }, [raw, category, tabKey, engine, context]);

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
        // group 由运营在「体验区管理 → 通用设置」里配。留空则不下发,后端按用户
        // 自己的分组走(早先唯一的行为)。/pg/ 路由本就支持请求体带 group,并由
        // middleware/distributor.go 校验 GroupInUserUsableGroups——不存在越权。
        //
        // 之所以要能配:优化模型通常是便宜小模型、挂在通用分组上,而 VIP/内部用户
        // 反而在只挂业务模型的专用分组里,不配分组就会「分组越专用越用不了」。
        const res = await API.post(
          '/pg/chat/completions',
          {
            model,
            ...(group ? { group } : {}),
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
        const msg = e?.response?.data?.error?.message || e?.message || '';
        // 只认「分组权限 / 无可用渠道」这两类配置问题(判据见 helpers/playground.js,
        // 与音乐页的「AI 帮我写词」共用一份)。
        showError(
          isPlaygroundConfigIssue(msg)
            ? // 前缀 + 原文,而不是替换:普通用户看开头知道找谁,管理员看后半段
              // 知道去哪查。原始报错正是定位问题的唯一线索,不能吞掉。
              t('AI 优化暂不可用，请联系管理员') + ' — ' + msg
            : t('提示词优化失败:') + msg,
        );
        return null;
      } finally {
        setOptimizing(false);
      }
    },
    [available, model, group, systemPrompt, t],
  );

  return { available, optimizing, optimize };
};
