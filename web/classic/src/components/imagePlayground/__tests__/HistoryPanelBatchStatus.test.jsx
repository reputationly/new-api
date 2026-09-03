import React from 'react';
import { describe, it, expect } from 'vitest';
import { render, within } from '@testing-library/react';

import ImageHistoryPanel from '../ImageHistoryPanel';
import { IMAGE_GEN_STATUS } from '../../../constants/imagePlayground.constants';

// 历史面板的状态徽标按**整批**取，不是按最后一条消息取。
//
// 多张候选拆成 N 条独立消息之后（useImageGeneration 的 asstMsgs），末条只是其中一个
// 候选。只读末条会让「2 张成功 + 最后一张被拒」显示成红色「失败」，而同一行右边还
// 写着 2 张图 —— 自相矛盾，用户会以为整批白跑了。
//
// 这不是罕见组合：gpustack 的准入控制按非终态任务数估排队时间，越靠后的请求越容易
// 吃 429（playgroundBatch.constants 把「选了 3 张，回来 2 张 + 1 个 429」列为漏配时
// 的常态表现）。而拆分前那条聚合消息是 `done.length > 0 ? SUCCESS : FAILED`，
// 徽标一直是绿色的 —— 所以这是拆分引入的回归，不是原有缺陷。

const asst = (id, status, images = []) => ({
  id,
  role: 'assistant',
  status,
  images,
  prompt: '画一只猫',
});

const conv = (messages) => ({
  id: 'c1',
  title: '画一只猫',
  updatedAt: '2026-09-03T15:22:53.000Z',
  messages: [{ id: 'u', role: 'user', content: '画一只猫' }, ...messages],
});

const renderPanel = (messages) =>
  render(
    <ImageHistoryPanel
      history={[conv(messages)]}
      onNewConversation={() => {}}
      onClear={() => {}}
      onDelete={() => {}}
      onOpen={() => {}}
    />,
  );

describe('历史面板的批次状态', () => {
  it('2 张成功 + 最后一张失败 → 已完成，不是失败', () => {
    const { container } = renderPanel([
      asst('a0', IMAGE_GEN_STATUS.SUCCESS, ['https://x/0.png']),
      asst('a1', IMAGE_GEN_STATUS.SUCCESS, ['https://x/1.png']),
      asst('a2', IMAGE_GEN_STATUS.FAILED),
    ]);
    const scope = within(container);
    expect(scope.getByText('已完成')).toBeTruthy();
    expect(scope.queryByText('失败')).toBeNull();
  });

  it('第一张失败、后两张成功 → 同样是已完成（顺序不该影响结论）', () => {
    const { container } = renderPanel([
      asst('a0', IMAGE_GEN_STATUS.FAILED),
      asst('a1', IMAGE_GEN_STATUS.SUCCESS, ['https://x/1.png']),
      asst('a2', IMAGE_GEN_STATUS.SUCCESS, ['https://x/2.png']),
    ]);
    expect(within(container).getByText('已完成')).toBeTruthy();
  });

  it('全部失败才是失败', () => {
    const { container } = renderPanel([
      asst('a0', IMAGE_GEN_STATUS.FAILED),
      asst('a1', IMAGE_GEN_STATUS.FAILED),
      asst('a2', IMAGE_GEN_STATUS.FAILED),
    ]);
    const scope = within(container);
    expect(scope.getByText('失败')).toBeTruthy();
    expect(scope.queryByText('已完成')).toBeNull();
  });

  it('还有一条在跑 → 生成中，哪怕已经出了图', () => {
    // 与拆分前的口径一致：聚合消息要等所有任务都终态才离开 PENDING。
    const { container } = renderPanel([
      asst('a0', IMAGE_GEN_STATUS.SUCCESS, ['https://x/0.png']),
      asst('a1', IMAGE_GEN_STATUS.PENDING),
      asst('a2', IMAGE_GEN_STATUS.FAILED),
    ]);
    const scope = within(container);
    expect(scope.getByText('生成中')).toBeTruthy();
    expect(scope.queryByText('已完成')).toBeNull();
  });

  it('单张（存量会话的形态）行为不变', () => {
    const { container } = renderPanel([
      asst('a', IMAGE_GEN_STATUS.SUCCESS, ['https://x/0.png']),
    ]);
    expect(within(container).getByText('已完成')).toBeTruthy();
  });
});
