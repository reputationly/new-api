import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, within, act } from '@testing-library/react';

vi.mock('../../../helpers', () => ({
  API: { post: vi.fn(), get: vi.fn() },
  showError: vi.fn(),
  showInfo: vi.fn(),
  showSuccess: vi.fn(),
  getLogo: () => '',
  stringToColor: () => '#000000',
}));

import { API } from '../../../helpers';
import ImageChatArea from '../ImageChatArea';
import { StatusContext } from '../../../context/Status';
import { UserContext } from '../../../context/User';
import { IMAGE_ENGINE_SENSENOVA_U15 } from '../../../constants/playgroundAdmin.constants';
import { defaultOptimizeSystemPrompt } from '../../../constants/promptOptimize.constants';

// 守的是**prop 透传**，不是模板内容（模板由 constants 的用例守）。
//
// 这条链断了不报错：按钮照样在、优化照样返回，只是拿的是通用模板。历史上手机端视频
// 体验区就漏传过 engine，选了 H3 却拿到通用模板，静默出差档（见 promptOptimize.constants
// 顶部注释）。图像这边是刚接上的同一条线，所以补一个真点一次按钮的用例。
const STATUS = {
  PlaygroundTabConfig: JSON.stringify({
    __global: { promptOptimize: { enabled: true, model: 'gpt-optimizer' } },
  }),
};

// 查询一律限定在本次渲染的 container 里，**不要用 screen / document.querySelector**：
// Semi 的 Chat 会往 document.body 挂 portal，RTL 的 cleanup 只回收自己建的容器，
// 于是上一个用例的 textarea 会残留在 document 上。用全局查询时第二个用例会敲到上一个
// 用例那棵已卸载的树里，inputValue 恒为空 → optimize 直接早退、一个请求都不发，
// 报错还长得像「透传断了」，极易误判成产品 bug。
const renderArea = (props) =>
  render(
    <UserContext.Provider value={[{ user: { username: 'tester' } }, () => {}]}>
      <StatusContext.Provider value={[{ status: STATUS }, () => {}]}>
        <ImageChatArea messages={[]} mode='text2image' {...props} />
      </StatusContext.Provider>
    </UserContext.Provider>,
  );

const typePrompt = async (container, text) => {
  const textarea = container.querySelector('textarea');
  await act(async () => {
    const setter = Object.getOwnPropertyDescriptor(
      window.HTMLTextAreaElement.prototype,
      'value',
    ).set;
    setter.call(textarea, text);
    textarea.dispatchEvent(new Event('input', { bubbles: true }));
  });
};

const clickOptimize = async (container) => {
  const btn = within(container).getByText('AI 优化提示词').closest('button');
  await act(async () => {
    btn.click();
  });
};

describe('图像体验区把引擎族透传给「AI 优化提示词」', () => {
  beforeEach(() => {
    API.post.mockReset();
    API.post.mockResolvedValue({
      data: { choices: [{ message: { content: '优化后的提示词' } }] },
    });
  });

  it('optimizeEngine=SenseNova-U1.5 时发出去的是 U1.5 的模板', async () => {
    const { container } = renderArea({
      optimizeEngine: IMAGE_ENGINE_SENSENOVA_U15,
    });
    // 输入框为空时按钮只给一句提示、不发请求，所以先填一句。
    await typePrompt(container, '一张发布会海报');
    await clickOptimize(container);
    expect(API.post).toHaveBeenCalled();
    const [, body] = API.post.mock.calls.at(-1);
    expect(body.messages[0].content).toBe(
      defaultOptimizeSystemPrompt('text2image', IMAGE_ENGINE_SENSENOVA_U15),
    );
  });

  // 底图与请求事实走的是同一条透传线，断了同样不报错：图生图会退回「蒙眼改写」，
  // 产出一份和底图对着干的提示词（见 usePromptOptimizeImages 用例头部）。
  it('optimizeImages 透传成多模态 content，顺序不变', async () => {
    const { container } = renderArea({
      mode: 'image2image',
      optimizeEngine: IMAGE_ENGINE_SENSENOVA_U15,
      optimizeImages: ['data:image/png;base64,A', 'data:image/png;base64,B'],
    });
    await typePrompt(container, '参考第 2 张的风格改第 1 张');
    await clickOptimize(container);
    const [, body] = API.post.mock.calls.at(-1);
    const urls = body.messages[1].content
      .filter((p) => p.type === 'image_url')
      .map((p) => p.image_url.url);
    expect(urls).toEqual([
      'data:image/png;base64,A',
      'data:image/png;base64,B',
    ]);
  });

  it('optimizeContext 拼在系统提示词末尾', async () => {
    const { container } = renderArea({
      mode: 'image2image',
      optimizeEngine: IMAGE_ENGINE_SENSENOVA_U15,
      optimizeContext: '\n\n---\n\nCurrent request:\n\n- Target canvas: 9x16.',
    });
    await typePrompt(container, '把天空改成晚霞');
    await clickOptimize(container);
    const [, body] = API.post.mock.calls.at(-1);
    expect(body.messages[0].content).toBe(
      defaultOptimizeSystemPrompt('image2image', IMAGE_ENGINE_SENSENOVA_U15) +
        '\n\n---\n\nCurrent request:\n\n- Target canvas: 9x16.',
    );
  });
});
