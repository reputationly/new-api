import { describe, it, expect } from 'vitest';
import { pickResumeConvId } from '../../hooks/imagePlayground/useImageGeneration';

// mount 时「把用户放回哪条会话」的选取。
//
// 这条测试守的是一个**顺序约定**:conversations 是新在前(两个写入分支都是前插)。
// 它一旦被当成「按追加顺序存」,选取就会反向取到最旧那条 —— 不丢数据、轮询照常,
// 只是这个功能的意图整个反了,而且没有任何报错。第一版就是这么写错的。

const pending = (id) => ({
  id,
  messages: [
    {
      role: 'assistant',
      status: 'pending',
      imageTasks: [{ taskId: `t-${id}`, status: 'pending' }],
    },
  ],
});
const done = (id) => ({
  id,
  messages: [
    {
      role: 'assistant',
      status: 'success',
      images: ['x'],
      imageTasks: [{ taskId: `t-${id}`, status: 'success' }],
    },
  ],
});

describe('pickResumeConvId', () => {
  // 核心:多条并行时必须取**最新**那条。数组是新在前,所以是第一个匹配。
  it('多条在跑时取最新那条（数组新在前 → 第一个匹配）', () => {
    expect(
      pickResumeConvId([pending('新'), pending('中'), pending('旧')]),
    ).toBe('新');
  });

  it('跳过已完成的会话，取第一条仍在跑的', () => {
    expect(
      pickResumeConvId([done('a'), done('b'), pending('c'), pending('d')]),
    ).toBe('c');
  });

  it('没有在跑的任务时返回 null（维持「新对话」）', () => {
    expect(pickResumeConvId([done('a'), done('b')])).toBeNull();
    expect(pickResumeConvId([])).toBeNull();
    expect(pickResumeConvId(null)).toBeNull();
  });

  // 同步生成的消息没有 imageTasks(那条路径留空),刷新即丢,不该被当成可恢复。
  it('同步消息（无 imageTasks）不算可恢复', () => {
    const sync = {
      id: 's',
      messages: [{ role: 'assistant', status: 'pending', imageTasks: [] }],
    };
    expect(pickResumeConvId([sync])).toBeNull();
  });

  // 任务已经全部结束、只是消息状态还没落盘时，也不该把用户拽过去
  it('任务槽里没有 pending 的不算可恢复', () => {
    const finished = {
      id: 'f',
      messages: [
        {
          role: 'assistant',
          status: 'pending',
          imageTasks: [{ taskId: 't', status: 'success' }],
        },
      ],
    };
    expect(pickResumeConvId([finished])).toBeNull();
  });

  it('会话结构残缺时不抛异常', () => {
    expect(pickResumeConvId([{}, { messages: null }, pending('ok')])).toBe(
      'ok',
    );
  });
});
