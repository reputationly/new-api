import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';

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

const renderArea = (props) =>
  render(
    <UserContext.Provider value={[{ user: { username: 'tester' } }, () => {}]}>
      <StatusContext.Provider value={[{ status: STATUS }, () => {}]}>
        <ImageChatArea messages={[]} mode='text2image' {...props} />
      </StatusContext.Provider>
    </UserContext.Provider>,
  );

const clickOptimize = async () => {
  const btn = screen.getByText('AI 优化提示词').closest('button');
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
    renderArea({ optimizeEngine: IMAGE_ENGINE_SENSENOVA_U15 });
    // 输入框为空时按钮只给一句提示、不发请求，所以先填一句。
    const textarea = document.querySelector('textarea');
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLTextAreaElement.prototype,
        'value',
      ).set;
      setter.call(textarea, '一张发布会海报');
      textarea.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await clickOptimize();
    expect(API.post).toHaveBeenCalled();
    const [, body] = API.post.mock.calls.at(-1);
    expect(body.messages[0].content).toBe(
      defaultOptimizeSystemPrompt('text2image', IMAGE_ENGINE_SENSENOVA_U15),
    );
  });
});
