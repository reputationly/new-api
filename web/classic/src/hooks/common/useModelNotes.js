import { useCallback, useContext, useMemo } from 'react';
import { StatusContext } from '../../context/Status';
import {
  buildModelNoteIndex,
  getTabStoreKey,
} from '../../constants/playgroundAdmin.constants';

// 取当前「分类 + 玩法」下各模型的运营备注（体验区管理里逐模型填的一句用途说明）。
// 返回 noteOf(model) -> string，未配置返回 ''。
// 备注按 tab 存（见 playgroundAdmin.constants 的 buildModelNoteIndex），所以哪份
// ModelConfig 由 getTabStoreKey 决定 —— 「视频配音」入口在语音页、模型却配在
// VideoModelConfig，这里不需要特判。
export const useModelNotes = (category, tabKey) => {
  const [statusState] = useContext(StatusContext);
  const storeKey = getTabStoreKey(category, tabKey);
  const raw = storeKey ? statusState?.status?.[storeKey] : null;
  const index = useMemo(() => buildModelNoteIndex(raw, tabKey), [raw, tabKey]);
  return useCallback((model) => index.get(model) || '', [index]);
};
