import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';

vi.mock('../../../helpers', () => ({
  API: { post: vi.fn() },
  showError: vi.fn(),
  showInfo: vi.fn(),
}));

import { API } from '../../../helpers';
import { StatusContext } from '../../../context/Status';
import { usePromptOptimize } from '../usePromptOptimize';

// 图生图要把**底图本身**一并发给优化模型。
//
// 这条链断了同样不报错：按钮还在、优化照样返回一段像模像样的文本，只是改写模型压根
// 没看见底图，只能从文字猜——实测过一次：用户传的是南京受降的彩色油画，优化模型从
// "日本投降递交投降文件"几个字猜成"密苏里号战舰 / 黑白纪实摄影风格"，而出图模型是
// 看得见底图的，两边直接对着干。官方 edit_pe.py 正是图 + 原文一起发的。
//
// 所以这里守三件事，每一件都是静默失效：
//   1. 有底图时 content 必须是多模态数组，不能退回字符串；
//   2. 顺序必须是「图在前、文本在后」，且图之间保持数组下标顺序——顺序就是模型认的
//      图片编号，用户说"第 2 张"靠的正是它，乱序等于指错图；
//   3. idb-media: 裸引用必须被剔掉——那是本地 IDB 的键，发过去是在图片位喂垃圾文本。

const STATUS = {
  PlaygroundTabConfig: JSON.stringify({
    __global: { promptOptimize: { enabled: true, model: 'gpt-optimizer' } },
  }),
};

const renderI2I = (opts) =>
  renderHook(() => usePromptOptimize('image', 'image2image', opts), {
    wrapper: ({ children }) => (
      <StatusContext.Provider value={[{ status: STATUS }, () => {}]}>
        {children}
      </StatusContext.Provider>
    ),
  });

// 跑一次 optimize，回传实际发出去的 user message content。
const sentUserContent = async (opts) => {
  const { result } = renderI2I(opts);
  expect(result.current.available).toBe(true);
  await act(async () => {
    await result.current.optimize('把天空改成晚霞');
  });
  const [, body] = API.post.mock.calls.at(-1);
  return body.messages[1].content;
};

describe('图生图把底图一并发给优化模型', () => {
  beforeEach(() => {
    API.post.mockReset();
    API.post.mockResolvedValue({
      data: { choices: [{ message: { content: '优化后的指令' } }] },
    });
  });

  it('有底图时 content 是多模态数组，图在前、文本在后', async () => {
    const content = await sentUserContent({
      images: ['data:image/png;base64,AAA', 'data:image/png;base64,BBB'],
    });
    expect(Array.isArray(content)).toBe(true);
    expect(content).toHaveLength(3);
    expect(content[0]).toEqual({
      type: 'image_url',
      image_url: { url: 'data:image/png;base64,AAA' },
    });
    expect(content[1]).toEqual({
      type: 'image_url',
      image_url: { url: 'data:image/png;base64,BBB' },
    });
    // 文本恒在最后一项，且带着官方 edit_pe.py 的 Rewritten Prompt 后缀语义。
    expect(content[2].type).toBe('text');
    expect(content[2].text).toContain('把天空改成晚霞');
  });

  it('多图顺序 = 传入数组顺序（用户说「第 N 张」靠的就是它）', async () => {
    const content = await sentUserContent({
      images: ['data:image/png;base64,ONE', 'data:image/png;base64,TWO'],
    });
    const urls = content
      .filter((p) => p.type === 'image_url')
      .map((p) => p.image_url.url);
    expect(urls).toEqual([
      'data:image/png;base64,ONE',
      'data:image/png;base64,TWO',
    ]);
  });

  it('没有底图时 content 保持字符串（文生图不退化成单元素数组）', async () => {
    const content = await sentUserContent({ images: [] });
    expect(typeof content).toBe('string');
    expect(content).toContain('把天空改成晚霞');
  });

  it('idb-media: 裸引用被剔除，不会当成图片 URL 发出去', async () => {
    const content = await sentUserContent({
      images: ['idb-media:abc123', 'data:image/png;base64,REAL'],
    });
    const urls = content
      .filter((p) => p.type === 'image_url')
      .map((p) => p.image_url.url);
    expect(urls).toEqual(['data:image/png;base64,REAL']);
  });

  it('底图全是裸引用时退回纯文本，而不是发一串垃圾 URL', async () => {
    const content = await sentUserContent({ images: ['idb-media:abc123'] });
    expect(typeof content).toBe('string');
  });
});
