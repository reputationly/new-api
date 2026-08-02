import { useMemo } from 'react';

import { usePlaygroundTabs } from '@classic/hooks/common/usePlaygroundTabs';

// 移动端各页的能力 tab 直接取「体验区管理」配置出的完整 tab 列表，不再各页维护 curated
// 子集：显隐基本只由后台一处控制。返回 [{key,title}]。
//
// 例外见下：个别能力在手机上体验不成立，才在这里额外挡掉。后台关掉的仍然两端都不显示，
// 这里只做减法。
const MOBILE_HIDDEN = {
  // 视频编辑要传 1~2 段源视频外加参考图，手机上传成本和流量都太重，先只留桌面端。
  video: ['vace'],
  // 音乐页手机端只留「文生音乐」「文生音效」两个纯文本玩法：改编/重绘要传整首源音频，
  // 歌声合成要传两段，都不适合手机。
  music: ['cover', 'repaint', 'svs'],
};

// 手机端做了功能降级（而非整个 tab 隐藏）的能力，提示语里一并点名，否则用户只会以为
// 网页端和手机端是一回事。
const MOBILE_SIMPLIFIED = {
  // 「生成后自动配音」目前两端都关着（DUB_PIPELINE_ENABLED=false），网页端也没有，
  // 所以这里不能写「请前往网页端使用」。等配音恢复且手机端仍不提供时，再把
  // video: ['视频配音'] 加回来。
  music: ['文生音乐高阶功能'],
};

export const useVisibleModes = (category) => {
  const visible = usePlaygroundTabs(category);
  return useMemo(() => {
    const hidden = MOBILE_HIDDEN[category] || [];
    return visible
      .filter((tb) => !hidden.includes(tb.key))
      .map((tb) => ({ key: tb.key, title: tb.label }));
  }, [category, visible]);
};

// 「XX、YY 请前往网页端使用」；无可提的能力时返回 ''（调用方据此不渲染）。
//
// 只点名「后台开着、但手机端藏了」的能力：后台关掉的网页端同样没有，指过去是错的。
export const useDesktopOnlyHint = (category) => {
  const visible = usePlaygroundTabs(category);
  return useMemo(() => {
    const hidden = MOBILE_HIDDEN[category] || [];
    const names = visible
      .filter((tb) => hidden.includes(tb.key))
      .map((tb) => tb.label)
      .concat(MOBILE_SIMPLIFIED[category] || []);
    return names.length ? `${names.join('、')}请前往网页端使用` : '';
  }, [category, visible]);
};
