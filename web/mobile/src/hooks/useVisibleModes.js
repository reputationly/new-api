import { useMemo } from 'react';

import { usePlaygroundTabs } from '@classic/hooks/common/usePlaygroundTabs';

// 移动端各页的能力 tab 取「体验区管理」配置出的 tab 列表，再按每个 tab 的手机端开关
// （display.mobile）做一次减法。返回 [{key,title}]。
//
// 手机端隐藏项以前是这里硬编码的一张 MOBILE_HIDDEN 表：改一次要发版，运营改不了。
// 现在挂在 PlaygroundTabConfig 的 mobile 字段上，后台直接勾。原表的语义由一次性迁移
// （model/migrate_playground_tabs.go 的 seedPlaygroundMobileVisibility）种进配置，
// 升级前后手机端看到的 tab 不变。缺省=显示，新增能力上线两端都能看到。
export const useVisibleModes = (category) => {
  const visible = usePlaygroundTabs(category);
  return useMemo(
    () =>
      visible
        .filter((tb) => tb.display.mobile)
        .map((tb) => ({ key: tb.key, title: tb.label })),
    [visible],
  );
};

// 手机端做了功能降级（而非整个 tab 隐藏）的能力，提示语里一并点名，否则用户只会以为
// 网页端和手机端是一回事。这类降级在代码里（不是整个 tab 的显隐），故仍写死。
const MOBILE_SIMPLIFIED = {
  // 「生成后自动配音」目前两端都关着（DUB_PIPELINE_ENABLED=false），网页端也没有，
  // 所以这里不能写「请前往网页端使用」。等配音恢复且手机端仍不提供时，再把
  // video: ['视频配音'] 加回来。
  music: ['文生音乐高阶功能'],
};

// 「XX、YY 请前往网页端使用」；无可提的能力时返回 ''（调用方据此不渲染）。
//
// 只点名「后台开着、但手机端关了」的 tab：后台整体关掉的网页端同样没有，指过去是错的
// （usePlaygroundTabs 已过滤掉 enabled=false）。
export const useDesktopOnlyHint = (category) => {
  const visible = usePlaygroundTabs(category);
  return useMemo(() => {
    const names = visible
      .filter((tb) => !tb.display.mobile)
      .map((tb) => tb.label)
      .concat(MOBILE_SIMPLIFIED[category] || []);
    return names.length ? `${names.join('、')}请前往网页端使用` : '';
  }, [category, visible]);
};
