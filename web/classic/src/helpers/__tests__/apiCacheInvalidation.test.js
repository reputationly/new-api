import { describe, expect, it } from 'vitest';

import { isChannelCacheInvalidatingRequest } from '../apiCache';

// 渠道写操作后必须让共享缓存失效，否则运营改完配置要等最长 5 分钟才看得到效果，
// 这会把"配置错了"和"缓存没到点"混成同一种现象。判据写在拦截器里，这里只锁边界：
// 写操作要清、读操作不能清（读也要清就等于彻底关掉缓存），无关接口不能清。
describe('isChannelCacheInvalidatingRequest', () => {
  it('渠道写操作要清缓存', () => {
    expect(
      isChannelCacheInvalidatingRequest({
        method: 'put',
        url: '/api/channel/',
      }),
    ).toBe(true);
    expect(
      isChannelCacheInvalidatingRequest({
        method: 'post',
        url: '/api/channel/',
      }),
    ).toBe(true);
    expect(
      isChannelCacheInvalidatingRequest({
        method: 'delete',
        url: '/api/channel/73/',
      }),
    ).toBe(true);
    expect(
      isChannelCacheInvalidatingRequest({
        method: 'put',
        url: '/api/channel/tag',
      }),
    ).toBe(true);
    expect(
      isChannelCacheInvalidatingRequest({
        method: 'post',
        url: '/api/channel/batch',
      }),
    ).toBe(true);
    // 带 query 的写操作（如 status 相关）同样要清
    expect(
      isChannelCacheInvalidatingRequest({
        method: 'post',
        url: '/api/channel/?status=1',
      }),
    ).toBe(true);
  });

  it('渠道读操作不能清缓存', () => {
    expect(
      isChannelCacheInvalidatingRequest({
        method: 'get',
        url: '/api/channel/?p=0',
      }),
    ).toBe(false);
    expect(
      isChannelCacheInvalidatingRequest({
        method: 'get',
        url: '/api/channel/search',
      }),
    ).toBe(false);
    expect(
      isChannelCacheInvalidatingRequest({
        method: 'head',
        url: '/api/channel/',
      }),
    ).toBe(false);
  });

  it('渠道命名空间下的只读 POST 不清缓存', () => {
    // 渠道编辑弹窗每选一个模型都会打一次；清了会让模型下拉反复重拉
    expect(
      isChannelCacheInvalidatingRequest({
        method: 'post',
        url: '/api/channel/endpoint_types',
      }),
    ).toBe(false);
  });

  it('无关接口与缺字段的请求不清缓存', () => {
    expect(
      isChannelCacheInvalidatingRequest({
        method: 'put',
        url: '/api/user/self',
      }),
    ).toBe(false);
    // 前缀相似的路径不能误判（/api/channels 之类的第三方/未来路由）
    expect(
      isChannelCacheInvalidatingRequest({
        method: 'post',
        url: '/api/channelsettings',
      }),
    ).toBe(false);
    expect(isChannelCacheInvalidatingRequest(undefined)).toBe(false);
    expect(isChannelCacheInvalidatingRequest({})).toBe(false);
  });
});
