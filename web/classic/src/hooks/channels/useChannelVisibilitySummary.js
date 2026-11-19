import { useCallback, useEffect, useMemo, useState } from 'react';

import { API } from '../../helpers';

// 渠道可见性汇总：把"渠道状态是个红点"翻译成"N 个模型对用户不可见"。
//
// 渠道被禁用时，能力行会被整体置为 enabled=false，那些模型就从模型广场、体验区、
// 令牌白名单里一起消失 —— 而渠道页上只有一个禁用状态点，没人会把它和"少了一批模型"
// 联系起来。真实盘点的结果：二十多条渠道长期禁用，背后是几十个模型悄无声息不可用。
//
// 失败一律静默（返回 null）：这只是提示信息，不该因为它让整页报错或挡住操作。
const ENDPOINT = '/api/channel/invisible_models';

// 渠道列表 -> 可见性签名。只有签名变化才重算：
//  - 带上子行：标签聚合视图里顶层行是「标签行」，真正的渠道在 children 里，启停单个
//    子渠道只改子行状态、标签行状态不变 —— 不带子行就会漏掉这种变化，角标与横幅停在旧数字上；
//  - 不用数组引用：单渠道测速、查余额同样会替换数组，每次都重算就是白跑两条全表查询。
export const buildVisibilitySignature = (watch) => {
  if (!Array.isArray(watch)) return '';
  return watch
    .map((channel) => {
      const self = `${channel?.id}:${channel?.status}`;
      if (!Array.isArray(channel?.children) || channel.children.length === 0) {
        return self;
      }
      const children = channel.children
        .map((child) => `${child?.id}:${child?.status}`)
        .join('|');
      return `${self}[${children}]`;
    })
    .join(',');
};

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

  // watch 传渠道列表：新建/删除/启用/禁用之后 useChannelsData 都会 setChannels。
  const signature = useMemo(() => buildVisibilitySignature(watch), [watch]);

  useEffect(() => {
    reload();
  }, [reload, signature]);

  return { byChannel, summary, loading, reload };
};
