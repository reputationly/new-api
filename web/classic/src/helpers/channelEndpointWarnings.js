import { PLAYGROUND_UNSUPPORTED_ENDPOINTS } from '../constants/playground.constants';

// 把 GET /api/channel/endpoint_types 的响应翻成「保存前预警」条目。
//
// 分工刻意这样切：
//   - 「这个模型会被判成什么端点」由后端算（common.GetEndpointTypesByChannelType，
//     同时看渠道类型和模型名，例如 GPUStackPlus 按名字区分图片/视频）—— 在 JS 里复刻
//     这套规则必然漂移，而漂移的表现是"页面没警告、模型却选不到"。
//   - 「哪些端点会让模型从文本体验区消失」用前端已有的 PLAYGROUND_UNSUPPORTED_ENDPOINTS
//     （模型下拉就是按这份键过滤的），保证预警与真实过滤完全一致。
//   - 后端返回的 playground_blocked 只作为是否需要展开计算的开关，不据它直接下结论：
//     万一两侧键集合漂移，以实际过滤用的那份为准，宁可多报一句也不漏报。
//
// 纯函数，便于单测：只做映射，不碰网络与 i18n。
export function collectPlaygroundBlockedModels(modelsMap) {
  if (!modelsMap || typeof modelsMap !== 'object') {
    return [];
  }
  const result = [];
  Object.entries(modelsMap).forEach(([model, info]) => {
    if (!info?.playground_blocked) {
      return;
    }
    const endpoints = (info.endpoint_types || [])
      .filter((type) => type in PLAYGROUND_UNSUPPORTED_ENDPOINTS)
      .map((type) => ({
        type,
        label: PLAYGROUND_UNSUPPORTED_ENDPOINTS[type]?.label || type,
        path: PLAYGROUND_UNSUPPORTED_ENDPOINTS[type]?.path || '',
      }));
    if (endpoints.length === 0) {
      return;
    }
    result.push({ model, endpoints });
  });
  // 名字排序：同一个模型列表每次返回的键顺序不保证，界面文案要稳定
  result.sort((a, b) => String(a.model).localeCompare(String(b.model)));
  return result;
}
