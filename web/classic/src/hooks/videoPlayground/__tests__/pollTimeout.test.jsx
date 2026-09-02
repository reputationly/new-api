import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';

vi.mock('../../../helpers', () => ({
  API: { get: vi.fn(), post: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showInfo: vi.fn(),
  processGroupsData: () => [],
  processModelsData: () => [],
  getUserModelsCached: vi.fn(() => Promise.reject(new Error('no net'))),
  cachedGet: vi.fn(() => Promise.reject(new Error('no net'))),
  getLogo: () => '',
  stringToColor: () => '#000',
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
import { useVideoGeneration } from '../useVideoGeneration';
import {
  VIDEO_POLL_INTERVAL_MS,
  VIDEO_POLL_MAX_TIMES,
} from '../../../constants/videoPlayground.constants';

// 轮询必须**始终**能超时停下。
//
// ⚠️ 这里守的不是某个值，而是"会停"。曾经的实现在调度处读粘性的 active.queuedPolls：
// 它只在成功轮询里更新，于是「排过一次队 + 之后请求持续失败（403/400/断网）」时
// count 冻住、queuedPolls 也涨不动 —— 两个停止条件同时失效，变成 4 秒一次的无限轮询
// 和一个永远转圈的进度卡。回归时这条用例会一直等不到 pollTimedOut。

const CONV = [
  {
    id: 'conv-1',
    model: 'wan2.2',
    messages: [
      {
        id: 'msg-1',
        role: 'assistant',
        status: 'queued',
        taskId: 'task-1',
      },
    ],
  },
];

const wrapper = ({ children }) => (
  <UserContext.Provider value={[{ user: { id: 1 } }, () => {}]}>
    <StatusContext.Provider value={[{ status: {} }, () => {}]}>
      {children}
    </StatusContext.Provider>
  </UserContext.Provider>
);

describe('排过队之后持续失败也要能超时', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    localStorage.setItem(
      'video_playground_conversations',
      JSON.stringify(CONV),
    );
    // 第一轮成功且是排队中（把"排过队"这个前提造出来），之后一律失败。
    let first = true;
    API.get.mockImplementation(() => {
      if (first) {
        first = false;
        return Promise.resolve({ data: { status: 'queued', queue_ahead: 2 } });
      }
      return Promise.reject(new Error('403'));
    });
  });

  afterEach(() => {
    vi.useRealTimers();
    localStorage.clear();
  });

  it('最终会标记 pollTimedOut，而不是无限轮询', async () => {
    const { result } = renderHook(
      () => useVideoGeneration({ mode: 'text2video' }),
      { wrapper },
    );
    // 跑满生成侧预算再多给几轮余量；修复前 count 永远停在 1，这里等不到超时。
    await act(async () => {
      await vi.advanceTimersByTimeAsync(
        VIDEO_POLL_INTERVAL_MS * (VIDEO_POLL_MAX_TIMES + 5),
      );
    });
    const msg = result.current.conversations
      .find((c) => c.id === 'conv-1')
      ?.messages.find((m) => m.id === 'msg-1');
    expect(msg?.pollTimedOut, '轮询没有停下来（回归：count 被冻住）').toBe(
      true,
    );
  });
});
