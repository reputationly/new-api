import { useCallback, useContext, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { StatusContext } from '../../context/Status';
import { API, showError, showInfo } from '../../helpers';
import {
  extractRenderJson,
  isPlaygroundConfigIssue,
  stripModelThinking,
} from '../../helpers/playground';
import { isMediaRef } from '../../helpers/playgroundMediaStorage';
import {
  parsePlaygroundTabConfig,
  getModelOptimizePrompt,
  getPlaygroundTab,
  getPromptOptimizeGlobal,
  getTabPromptOptimize,
  getTabStoreKey,
} from '../../constants/playgroundAdmin.constants';
import {
  defaultOptimizeSystemPrompt,
  optimizeOutputsJson,
  optimizeUserSuffix,
} from '../../constants/promptOptimize.constants';

// 「AI 优化提示词」共用 hook(图像/视频/音效体验区共用一份)。
//
// 与文生音乐 ACE-Step 的 draftPlan 同一条链路(单次非流式打 /pg/chat/completions,后端按
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
// 系统提示词三级取值:**模型级改写 → tab 级通用改写 → 内置默认**
// (constants/promptOptimize.constants.js,按 tab + 引擎族分开写)。
//
// 模型级那层是后加的,解决的是「一个 tab 挂多个引擎族」:tab 级只有一份,运营一旦改写
// 它,同 tab 下别家引擎的模型也被迫用这份形状不对的模板(H3 要带字段名的分段结构、
// LTX-2.5 要长段视听描述、通用版要一句话镜头描述),不报错、只是默默出差档。
// 运营在体验区管理的模型卡片里给单个模型另写一份即可,留空则跟随 tab。
//
// 第三个参数是可选的模型上下文:
//   model   —— 当前选中的模型名,用来取它的模型级改写;不传即只走 tab 级(原行为);
//   engine  —— 所选模型的引擎族(minimax-h3 要的是分段结构,与通用契约形状相反);
//   context —— 本次请求的既成事实(传了首帧还是尾帧、选了几秒、有几张参考图)。
//              优化模型看不到左侧面板,不喂它就只能猜,猜错同样不报错、只是默默变差。
//   images  —— 本次请求的输入图**本身**,会作为多模态 content 一并发给优化模型。
//
// images 那条与 context 的区别是"看没看到图":context 只说"有一张底图",images 让模型
// 真的看见它。图生图缺了它会**主动编错**——不是效果打折,是产出与底图打架:
// 用户传了一张彩色油画(南京受降),优化模型只从文字"日本投降递交投降文件"猜,写出
// "密苏里号战舰 / 黑白纪实摄影风格",而底图模型是能看到底图的,两边直接对着干。
// 官方 edit_pe.py 正是把输入图与原文一起发给改写模型的(见 promptOptimize.constants.js
// 里 U15_I2I_PROMPT 的注释),这条模板里过半规则(与底图风格一致 / 保持人物核心视觉
// 一致 / "沿用当前风格"时提取配色构图 / 多图角色分配)都必须看到图才能执行。
//
// 前提是运营配的优化模型得是 VLM。**刻意不做能力探测**:唯一能拿到的判据是模型名,
// 而按名字猜是不是 VL 模型必然误判,把功能从本来能用的人眼前拿走比报个错糟得多——
// 与本文件顶上「不判分组权限」同一个取舍。配错了会在 catch 里报出上游原话。
//
// 返回 { available, optimizing, optimize }。optimize(text) 成功返回优化后的字符串,
// 失败返回 null 并已弹过错误提示 —— 调用方只需判空后回填输入框。
export const usePromptOptimize = (
  category,
  tabKey,
  { engine, context, model: selectedModel, images } = {},
) => {
  const { t } = useTranslation();
  const [statusState] = useContext(StatusContext);
  const [optimizing, setOptimizing] = useState(false);
  const raw = statusState?.status?.PlaygroundTabConfig;
  // 模型级改写存在四份 ModelConfig 的 models[x].tabs[y].optimizePrompt 里,哪一份由
  // getTabStoreKey 决定 —— 「视频配音」入口在语音页、模型却配在 VideoModelConfig,
  // 与 useModelNotes 同一套,这里不需要特判。
  const storeKey = getTabStoreKey(category, tabKey);
  const modelConfigRaw = storeKey ? statusState?.status?.[storeKey] : null;

  const { available, model, group, systemPrompt } = useMemo(() => {
    const cfg = parsePlaygroundTabConfig(raw);
    const global = getPromptOptimizeGlobal(cfg);
    const tab = getTabPromptOptimize(cfg, category, tabKey);
    // 中央元数据里没声明 promptOptimize 的 tab 一律不出按钮:语音四个玩法输入的是
    // 待合成文本(改写=改内容,不是优化);文生音乐的 ACE-Step 走更专的 draftPlan 分支。
    const declared =
      getPlaygroundTab(category, tabKey)?.promptOptimize === true;
    return {
      available: declared && global.enabled && !!global.model && tab.enabled,
      model: global.model,
      group: global.group,
      // context 无条件追加在末尾:它不是模板而是本次请求的事实,运营改写过模板时
      // 同样需要(改写的多半也是 H3 模板,少了这段照样分不清 I2VA / L2VA)。
      systemPrompt:
        (getModelOptimizePrompt(modelConfigRaw, tabKey, selectedModel) ||
          (tab.systemPrompt || '').trim() ||
          defaultOptimizeSystemPrompt(tabKey, engine)) + (context || ''),
    };
  }, [raw, modelConfigRaw, category, tabKey, selectedModel, engine, context]);

  // idb-media: 是本地 IDB 的裸引用(见 helpers/playgroundMediaStorage),对模型毫无意义
  // ——发过去等于在图片位喂一串垃圾文本。hydrate 已保证不残留,这里与提交底图前那道
  // 过滤(useImageGeneration 的 params.images)同源再兜一次:那边发空底图是被后端拒,
  // 这边是**静默产出一份基于幻觉的提示词**,后者更难发现。
  const optimizeImages = useMemo(
    () => (images || []).filter((s) => s && !isMediaRef(s)),
    [images],
  );

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
      const userText = text + optimizeUserSuffix(tabKey, engine);
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
              // 后缀只有 U1.5 的图生图有(官方 edit_pe.py 的 USER_SUFFIX),其余为空串,
              // 拼上去等于没拼 —— 不必在这里再分支一次。
              //
              // 有图时换成多模态数组,**图在前、文本在后**,与官方 edit_pe.py 的
              // messages 构造逐项对齐。顺序不是风格问题:它就是模型认的图片编号,
              // 用户说"参考第二张图的风格"时靠的正是这个顺序,乱序等于指错图。
              //
              // 没图则保持字符串原样 —— 不退化成单元素数组:纯文本模型收字符串是
              // 一定认的,收 content 数组则未必,而文生图这一路本来就没图。
              {
                role: 'user',
                content: optimizeImages.length
                  ? [
                      ...optimizeImages.map((url) => ({
                        type: 'image_url',
                        image_url: { url },
                      })),
                      { type: 'text', text: userText },
                    ]
                  : userText,
              },
            ],
          },
          { skipErrorHandler: true },
        );
        // 先剥思考段再剥围栏:围栏那条正则是 ^ 锚定的,思考段还在前面时它匹配不到。
        // 推理模型把思考拼进 content 时,不剥就等于把整段思考回填进用户的输入框。
        const body = stripModelThinking(
          res?.data?.choices?.[0]?.message?.content,
        );
        // 产物是 JSON 的模板(SenseNova-U1.5 文生图)走官方那套提取,**不能走下面的通用
        // 清洗**:那条剥首尾引号的正则对 JSON 无害但也无用,而"取首个 { 到末个 }"才是
        // 官方兜模型多说两句的办法。两条路互斥,别叠加。
        const out = optimizeOutputsJson(tabKey, engine)
          ? extractRenderJson(body)
          : body
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
        // 与音乐页 ACE-Step 的 draftPlan 共用一份)。
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
    // tabKey / engine 是新加的:用户消息后缀与"产物是不是 JSON"都按它俩分支,
    // 漏进依赖数组的话,切了 tab 或换了模型仍会沿用上一份闭包里的判断。
    // optimizeImages 同理,且漏掉它的后果最隐蔽:用户换了底图却仍拿旧图去改写,
    // 既不报错也看不出来,只是改写结果对不上眼前这张图。
    [available, model, group, systemPrompt, tabKey, engine, optimizeImages, t],
  );

  return { available, optimizing, optimize };
};
