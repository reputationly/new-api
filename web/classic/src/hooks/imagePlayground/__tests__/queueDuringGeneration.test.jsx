import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';

vi.mock('../../../helpers', () => ({
  API: {
    get: vi.fn(),
    post: vi.fn(),
  },
  showError: vi.fn(),
  processGroupsData: () => [],
  processModelsData: () => [],
  getUserModelsCached: vi.fn(() => Promise.reject(new Error('no net'))),
  cachedGet: vi.fn(() => Promise.reject(new Error('no net'))),
}));
vi.mock('../../../helpers/playgroundMediaStorage', () => ({
  persistWithMedia: vi.fn(),
  hydrateConversationsFromStorage: vi.fn(() => Promise.resolve([])),
  stripUnresolvedMediaRefs: (x) => x,
  isMediaRef: () => false,
}));

import { API } from '../../../helpers';
import { StatusContext } from '../../../context/Status';
import { UserContext } from '../../../context/User';
import { useImageGeneration } from '../useImageGeneration';

// 门面对「正在出图」的任务按设计回 queue_ahead: 0（gpustackplus 那侧有
// TestRunningTaskKeepsZeroAhead 守着），而 0 在 formatQueueHint 里读作「即将开始…」。
// 图片这条链路把 queued 和 in_progress 都折成本地的 pending 态，卡片只判「还没出图」
// 就显示排队文案 —— 于是整个生成过程中界面写着「还没开始」，图其实正在出。
// 视频/语音/音乐三处都用 status === QUEUED 门控，图片只能靠 pollOnce 记下这个区分。
//
// 这里守的是**接口字段到 queued 标志的那一步映射**：aggregateQueue 自己的筛选逻辑
// 由 aggregateQueue.test.js 覆盖，但把 'queued' 写成 'in_progress'、或干脆漏掉这行，
// 纯函数测试是看不见的。

beforeEach(() => {
  API.post.mockReset();
  API.get.mockReset();
});

const renderImage = () =>
  renderHook(() => useImageGeneration({ mode: 'text2image' }), {
    wrapper: ({ children }) => (
      <UserContext.Provider value={[{ user: { id: 1 } }, () => {}]}>
        <StatusContext.Provider value={[{ status: {} }, () => {}]}>
          {children}
        </StatusContext.Provider>
      </UserContext.Provider>
    ),
  });

// 跑一轮：提交 → 等一个轮询间隔 → 拿到 pollStatus 描述的那次查询结果。
const pollOnceWith = async (pollStatus) => {
  API.post.mockResolvedValue({ data: { id: 'task-1' }, headers: {} });
  API.get.mockResolvedValue({
    data: {
      status: pollStatus,
      queue_ahead: 0,
      estimated_start_seconds: 0,
    },
  });

  const { result } = renderImage();
  await act(async () => {
    result.current.handleInputChange('model', 'm');
  });
  await act(async () => {
    await result.current.generate('画一只猫');
  });
  // 轮询是 setTimeout 调度的，快模型间隔 3s。
  await act(async () => {
    await vi.advanceTimersByTimeAsync(3500);
  });
  return result.current.messages.find((m) => m.role === 'assistant');
};

describe('图片排队回显只在真的排队时出现', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    return () => vi.useRealTimers();
  });

  it('in_progress + queue_ahead:0 → 不报排队（否则整个出图过程显示「即将开始…」）', async () => {
    const msg = await pollOnceWith('in_progress');
    expect(msg.queueAhead).toBeUndefined();
  });

  it('queued + queue_ahead:0 → 照报，0 是「下一个就轮到我」', async () => {
    const msg = await pollOnceWith('queued');
    expect(msg.queueAhead).toBe(0);
  });
});
