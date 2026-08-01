import { useMemo } from 'react';

import { usePlaygroundTabs } from '@classic/hooks/common/usePlaygroundTabs';

// 移动端各页的能力 tab 直接取「体验区管理」配置出的完整 tab 列表，不再各页维护 curated
// 子集：桌面端有哪些能力手机端就有哪些，显隐只由后台一处控制。返回 [{key,title}]。
export const useVisibleModes = (category) => {
  const visible = usePlaygroundTabs(category);
  return useMemo(
    () => visible.map((tb) => ({ key: tb.key, title: tb.label })),
    [visible],
  );
};
