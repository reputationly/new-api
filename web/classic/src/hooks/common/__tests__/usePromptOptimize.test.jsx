import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act } from '@testing-library/react';

// helpers 整个模块被替换：本用例只关心「发给优化模型的 system 是哪一份」，
// 不需要真的打请求。
vi.mock('../../../helpers', () => ({
  API: { post: vi.fn() },
  showError: vi.fn(),
  showInfo: vi.fn(),
}));

import { API } from '../../../helpers';
import { StatusContext } from '../../../context/Status';
import { usePromptOptimize } from '../usePromptOptimize';
import {
  IMAGE_ENGINE_SENSENOVA_U15,
  VIDEO_ENGINE_LTX25,
  getModelOptimizePrompt,
} from '../../../constants/playgroundAdmin.constants';
import { defaultOptimizeSystemPrompt } from '../../../constants/promptOptimize.constants';
import { parseVideoModelConfig } from '../../../constants/videoPlayground.constants';
import { parseImageSizeConfig } from '../../../constants/imagePlayground.constants';
import { parseMusicModelConfig } from '../../../constants/musicPlayground.constants';
import { parseAudioModelConfig } from '../../../constants/audioPlayground.constants';

// 「AI 优化提示词」的系统提示词取值链：模型级 → tab 级通用 → 内置默认。
//
// 这条链每一环失效都**不报错**：优化照样返回一段文本，只是模板形状对不上模型
// （H3 要带字段名的分段结构、LTX-2.5 要长段单段落视听描述、通用版要一句话镜头描述），
// 出片默默变差。所以由测试守住，而不是靠 review。

const TAB_CONFIG = JSON.stringify({
  __global: { promptOptimize: { enabled: true, model: 'gpt-optimizer' } },
  video: {
    text2video: {
      promptOptimize: { enabled: true, systemPrompt: 'TAB 级通用模板' },
    },
  },
});

const videoConfig = (models) => JSON.stringify({ models });

const renderOptimize = (status, opts) =>
  renderHook(() => usePromptOptimize('video', 'text2video', opts), {
    wrapper: ({ children }) => (
      <StatusContext.Provider value={[{ status }, () => {}]}>
        {children}
      </StatusContext.Provider>
    ),
  });

// 跑一次 optimize，回传实际发出去的 system content。
const sentSystem = async (status, opts) => {
  const { result } = renderOptimize(status, opts);
  expect(result.current.available).toBe(true);
  await act(async () => {
    await result.current.optimize('一只猫在窗台上打盹');
  });
  const [, body] = API.post.mock.calls.at(-1);
  return body.messages.find((m) => m.role === 'system').content;
};

describe('usePromptOptimize 的系统提示词取值链', () => {
  beforeEach(() => {
    API.post.mockReset();
    API.post.mockResolvedValue({
      data: { choices: [{ message: { content: '优化后的提示词' } }] },
    });
  });

  it('模型定制过就用模型那份，压过 tab 级通用方案', async () => {
    const status = {
      PlaygroundTabConfig: TAB_CONFIG,
      VideoModelConfig: videoConfig({
        'ltx-2.5': {
          tabs: { text2video: { optimizePrompt: '本模型专用模板' } },
        },
      }),
    };
    expect(await sentSystem(status, { model: 'ltx-2.5' })).toBe(
      '本模型专用模板',
    );
  });

  it('模型没定制就跟随 tab 级通用方案', async () => {
    const status = {
      PlaygroundTabConfig: TAB_CONFIG,
      VideoModelConfig: videoConfig({
        'ltx-2.5': { tabs: { text2video: {} } },
      }),
    };
    expect(await sentSystem(status, { model: 'ltx-2.5' })).toBe(
      'TAB 级通用模板',
    );
  });

  it('两级都留空则回落到该引擎族的内置默认', async () => {
    const status = {
      PlaygroundTabConfig: JSON.stringify({
        __global: { promptOptimize: { enabled: true, model: 'gpt-optimizer' } },
      }),
      VideoModelConfig: videoConfig({
        'ltx-2.5': { tabs: { text2video: {} } },
      }),
    };
    expect(
      await sentSystem(status, {
        model: 'ltx-2.5',
        engine: VIDEO_ENGINE_LTX25,
      }),
    ).toBe(defaultOptimizeSystemPrompt('text2video', VIDEO_ENGINE_LTX25));
  });

  it('不传模型名维持原行为（只走 tab 级）', async () => {
    const status = {
      PlaygroundTabConfig: TAB_CONFIG,
      VideoModelConfig: videoConfig({
        'ltx-2.5': {
          tabs: { text2video: { optimizePrompt: '本模型专用模板' } },
        },
      }),
    };
    expect(await sentSystem(status, {})).toBe('TAB 级通用模板');
  });

  it('context 仍无条件追加在末尾（模型级也不例外）', async () => {
    const status = {
      PlaygroundTabConfig: TAB_CONFIG,
      VideoModelConfig: videoConfig({
        'ltx-2.5': {
          tabs: { text2video: { optimizePrompt: '本模型专用模板' } },
        },
      }),
    };
    expect(
      await sentSystem(status, { model: 'ltx-2.5', context: '\n\n本次：5 秒' }),
    ).toBe('本模型专用模板\n\n本次：5 秒');
  });

  it('模型级只对配置它的那个 tab 生效，不串到别的 tab', () => {
    const raw = videoConfig({
      'ltx-2.5': { tabs: { text2video: { optimizePrompt: '文生视频专用' } } },
    });
    expect(getModelOptimizePrompt(raw, 'text2video', 'ltx-2.5')).toBe(
      '文生视频专用',
    );
    expect(getModelOptimizePrompt(raw, 'image2video', 'ltx-2.5')).toBe('');
    expect(getModelOptimizePrompt(raw, 'text2video', 'wan2.2')).toBe('');
  });
});

// 图像体验区此前根本不传 engine（图像没有引擎族这一层），SenseNova-U1.5 的模板是随
// 这层一起加的。这里守的是「engine 真的走到了模板选择」——断了不报错，只是退回通用版。
describe('图像 tab 也按引擎族取内置模板', () => {
  beforeEach(() => {
    API.post.mockReset();
    API.post.mockResolvedValue({
      data: { choices: [{ message: { content: '优化后的提示词' } }] },
    });
  });

  const renderImage = (opts) =>
    renderHook(() => usePromptOptimize('image', 'text2image', opts), {
      wrapper: ({ children }) => (
        <StatusContext.Provider
          value={[
            {
              status: {
                PlaygroundTabConfig: JSON.stringify({
                  __global: {
                    promptOptimize: { enabled: true, model: 'gpt-optimizer' },
                  },
                }),
              },
            },
            () => {},
          ]}
        >
          {children}
        </StatusContext.Provider>
      ),
    });

  it('声明了 SenseNova-U1.5 就用它的模板', async () => {
    const { result } = renderImage({ engine: IMAGE_ENGINE_SENSENOVA_U15 });
    await act(async () => {
      await result.current.optimize('一张发布会海报');
    });
    const [, body] = API.post.mock.calls.at(-1);
    expect(body.messages[0].content).toBe(
      defaultOptimizeSystemPrompt('text2image', IMAGE_ENGINE_SENSENOVA_U15),
    );
  });

  it('没声明引擎族的图像模型维持原行为（通用图像模板）', async () => {
    const { result } = renderImage({});
    await act(async () => {
      await result.current.optimize('一张发布会海报');
    });
    const [, body] = API.post.mock.calls.at(-1);
    expect(body.messages[0].content).toBe(
      defaultOptimizeSystemPrompt('text2image', ''),
    );
  });
});

// 优化结果是直接回填用户输入框的，所以「返回值里不能混进思考段」是这条链路的硬要求：
// 辅助模型配成推理模型时，思考若拼在 content 里，用户会眼看着输入框被灌满推理过程。
describe('回填前要剥掉推理模型的思考段', () => {
  beforeEach(() => {
    API.post.mockReset();
  });

  it('思考段 + 围栏都剥干净，只回正文', async () => {
    API.post.mockResolvedValue({
      data: {
        choices: [
          {
            message: {
              content:
                '用户只给了一句话，我需要补充镜头与光线…</think>\n```\n一只橘猫在洒满夕阳的窗台上打盹，浅景深特写\n```',
            },
          },
        ],
      },
    });
    const { result } = renderOptimize(
      { PlaygroundTabConfig: TAB_CONFIG },
      { model: 'ltx-2.5' },
    );
    let out;
    await act(async () => {
      out = await result.current.optimize('一只猫');
    });
    expect(out).toBe('一只橘猫在洒满夕阳的窗台上打盹，浅景深特写');
  });
});

// 四份 ModelConfig 的 parse 都是**白名单式重建**，而管理页草稿正是用它水合
// （usePlaygroundAdminDraft 的 toDraft）：漏一个键 = 运营每次打开体验区管理保存
// 一次，就把刚写的模型级模板删掉一次，且症状是「优化效果某天起悄悄退回通用版」。
describe('模型级优化提示词能往返（白名单式 parse 不能漏）', () => {
  it('VideoModelConfig', () => {
    const parsed = parseVideoModelConfig(
      videoConfig({
        'ltx-2.5': { tabs: { text2video: { optimizePrompt: '  视频模板  ' } } },
      }),
    );
    expect(parsed.models['ltx-2.5'].tabs.text2video.optimizePrompt).toBe(
      '视频模板',
    );
  });

  it('ImageModelSizeConfig', () => {
    const parsed = parseImageSizeConfig(
      JSON.stringify({
        models: {
          'flux-1': { tabs: { text2image: { optimizePrompt: '图像模板' } } },
        },
      }),
    );
    expect(parsed.models['flux-1'].tabs.text2image.optimizePrompt).toBe(
      '图像模板',
    );
  });

  it('MusicModelConfig', () => {
    const parsed = parseMusicModelConfig(
      JSON.stringify({
        models: {
          'minimax-music3': { tabs: { t2m: { optimizePrompt: '音乐模板' } } },
        },
      }),
    );
    expect(parsed.models['minimax-music3'].tabs.t2m.optimizePrompt).toBe(
      '音乐模板',
    );
  });

  // 今天没有语音玩法声明 promptOptimize（「视频配音」的模型走 VideoModelConfig），
  // 这条是提前把坑填上：哪天补了声明，管理页能存得住，而不是下次加载被静默删掉。
  it('AudioModelConfig', () => {
    const parsed = parseAudioModelConfig(
      JSON.stringify({
        models: {
          'tts-2.5': { tabs: { synthesis: { optimizePrompt: '语音模板' } } },
        },
      }),
    );
    expect(parsed.models['tts-2.5'].tabs.synthesis.optimizePrompt).toBe(
      '语音模板',
    );
  });

  it('未配置时不落键，好让取值链正确降级到 tab 级', () => {
    const parsed = parseVideoModelConfig(
      videoConfig({ 'ltx-2.5': { tabs: { text2video: {} } } }),
    );
    expect('optimizePrompt' in parsed.models['ltx-2.5'].tabs.text2video).toBe(
      false,
    );
  });
});
