import { API, addResponseSuccessHook } from './api';
import { getUserIdFromLocalStorage } from './utils';

// 前端只读接口的共享缓存：按完整 URL 缓存返回信封（res.data），供图片/视频/对话三个页面共用。
// 目的：切换页面/切回分组时不再重复请求这些每次都直查后端的接口（models / pricing / groups）。
// 失效：模块级缓存，整页刷新即重置；另加默认 5 分钟 TTL，后台数据变更后最多滞后 TTL 自动纠正。
const DEFAULT_TTL = 5 * 60 * 1000;

const cache = new Map(); // cacheKey -> { payload, ts }
const inflight = new Map(); // cacheKey -> Promise<payload>

// 这些接口按当前登录用户/分组过滤，缓存 key 必须带用户 id：
// 同标签页切换账号(SPA 跳转不刷新页面)时才不会命中上一个账号的缓存而串号。
const cacheKeyFor = (url) => `${getUserIdFromLocalStorage()}::${url}`;

// 通用缓存 GET：返回 res.data（与直接 API.get(...).data 一致）。
// config 透传给 API.get（如 { skipErrorHandler: true }）；ttl 可覆盖默认值。
export async function cachedGet(url, { ttl = DEFAULT_TTL, config } = {}) {
  const key = cacheKeyFor(url);
  const hit = cache.get(key);
  if (hit && Date.now() - hit.ts < ttl) {
    return hit.payload;
  }
  if (inflight.has(key)) {
    return inflight.get(key);
  }
  const promise = API.get(url, config)
    .then((res) => {
      const payload = res.data;
      // 仅缓存成功结果，失败不缓存以便下次重试。
      if (payload && payload.success) {
        cache.set(key, { payload, ts: Date.now() });
      }
      return payload;
    })
    .finally(() => {
      inflight.delete(key);
    });
  inflight.set(key, promise);
  return promise;
}

// 便捷封装：/api/user/models?group=X
export async function getUserModelsCached(group = '') {
  const groupParam = group ? `?group=${encodeURIComponent(group)}` : '';
  return cachedGet(`/api/user/models${groupParam}`);
}

// 供切换账号/配置变更等场景手动失效整个缓存。
export function clearApiCache() {
  cache.clear();
  inflight.clear();
}

// 渠道写操作（保存/删除/启停/批量改标签/按标签改模型）会改变 /api/user/models、
// /api/pricing、/api/user/group 的结果。改完不失效的话，运营得等 TTL（最长 5 分钟）
// 或手动刷新整页才看得到效果 —— "改完没生效"是本项目最常见的误判来源（真实案例：
// 新加渠道后体验区选不到模型，DB 里一切正常，只是缓存没到点）。
//
// 放在统一钩子里而不是各个调用点：渠道的写入口有十来处（单个/批量/按标签/删除禁用/
// 复制/修 ability…），逐个补必然漏一处，而漏掉的那处又会变成同一个 bug。
//
// 用 addResponseSuccessHook 而不是自己 axios.interceptors：拦截器挂在实例上，而登录后
// updateAPI() 会新建实例，挂上去的回调随之失效（"登录后改配置不生效"就是这么来的）。
const CHANNEL_WRITE_RE = /\/api\/channel(\/|\?|$)/;

// 判断一次请求是否属于「会改变模型可见性的渠道写操作」。
// 导出仅为可测：拦截器里直接用，不对外当作公共 API。
export function isChannelCacheInvalidatingRequest(config) {
  const method = String(config?.method || 'get').toLowerCase();
  if (method === 'get' || method === 'head' || method === 'options') {
    return false;
  }
  return CHANNEL_WRITE_RE.test(String(config?.url || ''));
}

addResponseSuccessHook((response) => {
  // 失败的写操作不该清缓存：它什么都没改，清了只是让用户多等一次请求。
  if (
    isChannelCacheInvalidatingRequest(response?.config) &&
    response?.data?.success !== false
  ) {
    clearApiCache();
  }
});
