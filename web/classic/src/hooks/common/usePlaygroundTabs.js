import { useContext, useMemo } from 'react';
import { StatusContext } from '../../context/Status';
import {
  parsePlaygroundTabConfig,
  resolvePlaygroundTabs,
} from '../../constants/playgroundAdmin.constants';

// 返回某分类下「当前可见」的 tab 列表（按运营 PlaygroundTabConfig 过滤+排序+改名，
// 缺省=显示、按声明顺序、用内置名）。各体验区分类页共用，避免重复解析。
// 返回 [{key,label,capability,fields,display:{enabled,mobile,order,label}}]。
// display.mobile 供手机端再做一次减法（见 web/mobile useVisibleModes），网页端不看它。
export const usePlaygroundTabs = (category) => {
  const [statusState] = useContext(StatusContext);
  const raw = statusState?.status?.PlaygroundTabConfig;
  return useMemo(() => {
    const cfg = parsePlaygroundTabConfig(raw);
    return resolvePlaygroundTabs(category, cfg).filter(
      (tb) => tb.display.enabled,
    );
  }, [category, raw]);
};
