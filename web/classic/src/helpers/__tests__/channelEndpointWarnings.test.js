import { describe, expect, it } from 'vitest';

import { collectPlaygroundBlockedModels } from '../channelEndpointWarnings';

// 保存前预警必须与模型下拉的真实过滤一致：下拉按 PLAYGROUND_UNSUPPORTED_ENDPOINTS
// 的键过滤，所以这里也用同一份键把端点翻成玩法名。任何一侧漂移，预警就会变成
// "页面说没事、模型却选不到"——那正是这套功能要消灭的现象。
describe('collectPlaygroundBlockedModels', () => {
  it('只保留会被文本体验区排除的模型', () => {
    const result = collectPlaygroundBlockedModels({
      'qwen3.8-max': { endpoint_types: ['openai'], playground_blocked: false },
      'wan2.2-t2v': {
        endpoint_types: ['openai-video'],
        playground_blocked: true,
      },
    });
    expect(result).toHaveLength(1);
    expect(result[0].model).toBe('wan2.2-t2v');
    expect(result[0].endpoints).toEqual([
      { type: 'openai-video', label: '视频生成', path: '/v1/videos' },
    ]);
  });

  it('一个模型命中多个非 chat 端点时全部列出', () => {
    const result = collectPlaygroundBlockedModels({
      'flux-1-dev': {
        endpoint_types: ['image-generation', 'embeddings'],
        playground_blocked: true,
      },
    });
    expect(result[0].endpoints.map((e) => e.type)).toEqual([
      'image-generation',
      'embeddings',
    ]);
  });

  it('后端标记 blocked 但端点全是 chat 时不下结论（以实际过滤键为准）', () => {
    const result = collectPlaygroundBlockedModels({
      'qwen3.8-max': { endpoint_types: ['openai'], playground_blocked: true },
    });
    expect(result).toEqual([]);
  });

  it('输出按模型名排序，空输入安全', () => {
    const result = collectPlaygroundBlockedModels({
      'zz-b': { endpoint_types: ['openai-video'], playground_blocked: true },
      'aa-a': { endpoint_types: ['openai-video'], playground_blocked: true },
    });
    expect(result.map((r) => r.model)).toEqual(['aa-a', 'zz-b']);

    expect(collectPlaygroundBlockedModels(undefined)).toEqual([]);
    expect(collectPlaygroundBlockedModels({})).toEqual([]);
    expect(collectPlaygroundBlockedModels(null)).toEqual([]);
  });
});
