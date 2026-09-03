import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';

vi.mock('../../../helpers', () => ({
  API: { get: vi.fn(), post: vi.fn() },
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
import { IMAGE_GEN_STATUS } from '../../../constants/imagePlayground.constants';
import { useImageGeneration } from '../useImageGeneration';

// 多候选拆成 N 条独立消息（与视频侧同构）。
//
// 改造前是「一条消息挂 N 个任务」，N 个状态只能聚合成一个数字，而聚合必然丢信息。
// 生产上的样子：三个任务里两个在跑、一个排队 → 整条消息被标成「排队中」，掩盖了
// 2/3 已经在出图；等第一张图出来后排队信息又整个消失，剩下那个还没开始的任务没有
// 任何提示。两个阶段都在骗人，且都不报错。
//
// 拆开之后每条消息只对应一个任务，各显各的状态。下面守的就是这件事。

const renderImage = () =>
  renderHook(
    () => useImageGeneration({ mode: 'text2image', allowBatch: true }),
    {
      wrapper: ({ children }) => (
        <UserContext.Provider value={[{ user: { id: 1 } }, () => {}]}>
          <StatusContext.Provider value={[{ status: {} }, () => {}]}>
            {children}
          </StatusContext.Provider>
        </UserContext.Provider>
      ),
    },
  );

const setup = async (batchCount) => {
  const { result } = renderImage();
  await act(async () => {
    result.current.handleInputChange('model', 'm');
    result.current.handleInputChange('batchCount', batchCount);
  });
  return result;
};

const assistants = (result) =>
  result.current.messages.filter((m) => m.role === 'assistant');

beforeEach(() => {
  API.post.mockReset();
  API.get.mockReset();
});

describe('多张下发拆成独立消息', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    return () => vi.useRealTimers();
  });

  it('张数 3 → 3 条 assistant 消息，各带一个任务', async () => {
    let n = 0;
    API.post.mockImplementation(() =>
      Promise.resolve({ data: { id: `task-${++n}` }, headers: {} }),
    );
    API.get.mockResolvedValue({ data: { status: 'queued', queue_ahead: 0 } });

    const result = await setup(3);
    await act(async () => {
      await result.current.generate('画一只猫');
    });

    const msgs = assistants(result);
    expect(msgs).toHaveLength(3);
    // 每条只挂自己那一个任务 —— 这是"各显各的状态"的前提。
    expect(msgs.map((m) => (m.imageTasks || []).length)).toEqual([1, 1, 1]);
    expect(msgs.map((m) => m.imageTasks[0].taskId)).toEqual([
      'task-1',
      'task-2',
      'task-3',
    ]);
    // 序号供结果区显示「第 n/N 张」。
    expect(msgs.map((m) => m.batchIndex)).toEqual([0, 1, 2]);
  });

  it('各报各的排队位置，不再取全体最大值', async () => {
    let n = 0;
    API.post.mockImplementation(() =>
      Promise.resolve({ data: { id: `task-${++n}` }, headers: {} }),
    );
    // 三条各不相同：task-1 排第 1 个、task-2 已在出图、task-3 排第 5 个。
    //
    // 位置**故意取两个不同的值**：聚合口径是「整条消息以最慢的那个为准」(max)，
    // 所以旧代码会把排第 1 的那条也报成 5。两条都排队且位置不同，是能把
    // 「各报各的」和「取最大值」区分开的唯一构造 —— 只让一条排队的话，
    // max 恰好等于它自己，用例在旧代码下照样绿，等于没测。
    API.get.mockImplementation((url) => {
      const s = String(url);
      if (s.includes('task-1'))
        return Promise.resolve({
          data: {
            status: 'queued',
            queue_ahead: 1,
            estimated_start_seconds: 60,
          },
        });
      if (s.includes('task-3'))
        return Promise.resolve({
          data: {
            status: 'queued',
            queue_ahead: 5,
            estimated_start_seconds: 300,
          },
        });
      return Promise.resolve({
        data: { status: 'in_progress', queue_ahead: 0 },
      });
    });

    const result = await setup(3);
    await act(async () => {
      await result.current.generate('画一只猫');
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3500);
    });

    const msgs = assistants(result);
    expect(msgs[0].queueAhead).toBe(1);
    expect(msgs[2].queueAhead).toBe(5);
    // 在跑的那条一个字都不提排队。改造前三条共用一个聚合值，
    // 于是正在出图的也被标成「排队中，前面还有 5 个」。
    expect(msgs[1].queueAhead).toBeUndefined();
  });

  it('先出的图立刻定稿，还在排队的那条照旧报排队', async () => {
    let n = 0;
    API.post.mockImplementation(() =>
      Promise.resolve({ data: { id: `task-${++n}` }, headers: {} }),
    );
    API.get.mockImplementation((url) =>
      Promise.resolve({
        data: String(url).includes('task-1')
          ? { status: 'queued', queue_ahead: 2, estimated_start_seconds: 120 }
          : { status: 'completed', data: [{ url: 'https://x/1.png' }] },
      }),
    );

    const result = await setup(3);
    await act(async () => {
      await result.current.generate('画一只猫');
    });
    await act(async () => {
      await vi.advanceTimersByTimeAsync(3500);
    });

    const msgs = assistants(result);
    // 已出图的两条转成终态。改造前它们要等最慢的那条一起结束。
    expect(msgs[1].status).toBe(IMAGE_GEN_STATUS.SUCCESS);
    expect(msgs[2].status).toBe(IMAGE_GEN_STATUS.SUCCESS);
    // 而排队那条仍在 PENDING **且仍然报得出排队位置** —— 改造前一旦有图出来，
    // 排队信息就整个消失了，只剩一句笼统的「其余生成中…」。
    expect(msgs[0].status).toBe(IMAGE_GEN_STATUS.PENDING);
    expect(msgs[0].queueAhead).toBe(2);
  });

  it('单张仍用 `-a` 结尾的 id —— 历史恢复与 IDB 媒体都按 msgId 索引', async () => {
    API.post.mockResolvedValue({ data: { id: 'task-1' }, headers: {} });
    API.get.mockResolvedValue({ data: { status: 'queued', queue_ahead: 0 } });

    const result = await setup(1);
    await act(async () => {
      await result.current.generate('画一只猫');
    });

    const msgs = assistants(result);
    expect(msgs).toHaveLength(1);
    expect(msgs[0].id).toMatch(/-a$/);
    // 单张不写 batch 字段，存量消息也没有，渲染侧一视同仁。
    expect(msgs[0].batchTotal).toBeUndefined();
  });

  it('某一张提交失败只判它自己失败，不连累其余两条', async () => {
    let n = 0;
    API.post.mockImplementation(() => {
      n += 1;
      return n === 2
        ? Promise.reject(new Error('上游拒绝'))
        : Promise.resolve({ data: { id: `task-${n}` }, headers: {} });
    });
    API.get.mockResolvedValue({ data: { status: 'queued', queue_ahead: 0 } });

    const result = await setup(3);
    await act(async () => {
      await result.current.generate('画一只猫');
    });

    const msgs = assistants(result);
    // 没建起任务的那条必须就地判失败：没有任务就没人推进它，
    // 漏掉的话它会永远停在 PENDING —— 界面上一个不会动的「生成中」。
    expect(msgs[1].status).toBe(IMAGE_GEN_STATUS.FAILED);
    expect(msgs[0].status).toBe(IMAGE_GEN_STATUS.PENDING);
    expect(msgs[2].status).toBe(IMAGE_GEN_STATUS.PENDING);
  });
});
