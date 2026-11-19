import { useCallback, useEffect, useState } from 'react';

import { API } from '../../helpers';

// 渠道可见性汇总：把"渠道状态是个红点"翻译成"N 个模型对用户不可见"。
//
// 渠道被禁用时，能力行会被整体置为 enabled=false，那些模型就从模型广场、体验区、
// 令牌白名单里一起消失 —— 而渠道页上只有一个禁用状态点，没人会把它和"少了一批模型"
// 联系起来。真实盘点的结果：二十多条渠道长期禁用，背后是几十个模型悄无声息不可用。
//
// 失败一律静默（返回 null）：这只是提示信息，不该因为它让整页报错或挡住操作。
const ENDPOINT = '/api/channel/invisible_models';

export const useChannelVisibilitySummary = ({ watch } = {}) => {
  const [byChannel, setByChannel] = useState({});
  const [summary, setSummary] = useState(null);
  const [loading, setLoading] = useState(false);

  const reload = useCallback(async () => {
    setLoading(true);
    try {
      const res = await API.get(ENDPOINT, { skipErrorHandler: true });
      const data = res?.data?.data;
      if (res?.data?.success && data) {
        setByChannel(data.by_channel || {});
        setSummary({
          total: data.invisible_models || 0,
          names: data.invisible_model_names || [],
        });
      }
    } catch (_) {
      // 静默：下拉/表格本身不受影响，下次刷新再试
    } finally {
      setLoading(false);
    }
  }, []);

  // watch 传的是渠道列表数组本身：新建/删除/启用/禁用之后 useChannelsData 都会 setChannels，
  // 数组引用变化即重算。挂在"渠道数组"而不是"渠道数量"上，是因为启停不改变数量——
  // 那正是最常见的场景（禁用一条渠道，模型立刻不可见）。
  useEffect(() => {
    reload();
  }, [reload, watch]);

  return { byChannel, summary, loading, reload };
};
