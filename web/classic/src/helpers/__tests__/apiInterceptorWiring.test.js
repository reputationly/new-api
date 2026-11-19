import { afterEach, describe, expect, it, vi } from 'vitest';

import { API, addResponseSuccessHook, updateAPI } from '../api';
import { cachedGet, clearApiCache } from '../apiCache';

// 拦截器必须挂在**当前**实例上。历史 bug：拦截器只在模块加载时挂到首个实例，登录后
// updateAPI() 换了实例，401 处理与缓存失效一起失效——而单测只测了判据函数，看不出这个。
// 这里跑真实的实例与拦截器链。
const runResponseChain = async (response) => {
  const handlers = API.interceptors.response.handlers;
  let promise = Promise.resolve(response);
  for (const handler of handlers) {
    promise = promise.then(handler.fulfilled, handler.rejected);
  }
  return promise;
};

afterEach(() => {
  clearApiCache();
  vi.restoreAllMocks();
});

describe('响应拦截器挂在实例上', () => {
  it('updateAPI() 之后新实例仍然带响应拦截器', () => {
    const before = API.interceptors.response.handlers.length;
    expect(before).toBeGreaterThan(0);

    updateAPI();

    const after = API.interceptors.response.handlers.length;
    expect(after).toBe(before);
    expect(after).toBeGreaterThan(0);
  });

  it('成功的响应会触发登记过的钩子', async () => {
    const seen = [];
    const remove = addResponseSuccessHook((response) => seen.push(response));
    updateAPI(); // 换实例后钩子依然生效，这正是本次修复的核心

    const response = {
      data: { success: true },
      config: { method: 'get', url: '/api/foo' },
    };
    await runResponseChain(response);

    expect(seen).toHaveLength(1);
    expect(seen[0]).toBe(response);
    remove();
  });

  it('渠道写操作成功后清掉共享缓存，读操作不清', async () => {
    updateAPI();
    const getSpy = vi
      .spyOn(API, 'get')
      .mockResolvedValue({ data: { success: true, data: [] } });

    await cachedGet('/api/user/models');
    await cachedGet('/api/user/models');
    expect(getSpy).toHaveBeenCalledTimes(1); // 第二次命中缓存

    // 渠道读操作不清缓存
    await runResponseChain({
      data: { success: true },
      config: { method: 'get', url: '/api/channel/?p=0' },
    });
    await cachedGet('/api/user/models');
    expect(getSpy).toHaveBeenCalledTimes(1);

    // 渠道写操作清缓存
    await runResponseChain({
      data: { success: true },
      config: { method: 'put', url: '/api/channel/' },
    });
    await cachedGet('/api/user/models');
    expect(getSpy).toHaveBeenCalledTimes(2);

    // 失败的写操作不清缓存（它什么都没改）
    await runResponseChain({
      data: { success: false },
      config: { method: 'put', url: '/api/channel/' },
    });
    await cachedGet('/api/user/models');
    expect(getSpy).toHaveBeenCalledTimes(2);
  });
});
