import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';

vi.mock('../../../helpers', () => ({
  API: {
    get: vi.fn(() => Promise.reject(new Error('no net'))),
    post: vi.fn(() => Promise.resolve({ data: { data: [{ b64_json: 'x' }] } })),
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

import { StatusContext } from '../../../context/Status';
import { UserContext } from '../../../context/User';
import { useImageGeneration } from '../useImageGeneration';

// 喂给「AI 优化提示词」的画幅，必须与**本次真正下发的那个值**一致。
//
// 这两者会分叉：图生图下先选一个配了 i2i sizes 的模型（inputs.size 被填成具体像素），
// 再切到一个没配的模型 —— shapeMode 变 'none'、availableSizes 为空，合法性 effect 在
// valid.length === 0 处直接 return，inputs.size 保持前一个模型的旧值；而提交时
// resolveSubmitImageSize 把 size 整段丢掉。此时界面上连尺寸控件都不显示，人眼发现不了
// 那个陈旧值。直接用 inputs.size 就会告诉模型一个本次根本不会下发的画幅 —— 它会照着做
// §6 的文案压缩判断，比不说更糟。

// sized = 显式配了 i2i sizes（canPickI2ISize=true，size 会下发）；
// bare  = 只配了 t2i（i2i 下 shapeMode='none'，size 不下发）。
const TWO_MODELS = JSON.stringify({
  models: {
    sized: { tabs: { image2image: { sizes: ['1024x1536'] } } },
    bare: { tabs: { text2image: { sizes: ['1024x1024'] } } },
  },
});

// t2i 配的是比例词——运营历来这么填，size 原样下发，不能被说成 pixels。
const T2I_RATIO = JSON.stringify({
  models: { m: { tabs: { text2image: { sizes: ['16:9'] } } } },
});

const renderImage = (config, mode) =>
  renderHook(() => useImageGeneration({ mode }), {
    wrapper: ({ children }) => (
      <UserContext.Provider value={[{ user: { id: 1 } }, () => {}]}>
        <StatusContext.Provider
          value={[{ status: { ImageModelSizeConfig: config } }, () => {}]}
        >
          {children}
        </StatusContext.Provider>
      </UserContext.Provider>
    ),
  });

const withModel = (config, mode, model) => {
  const { result } = renderImage(config, mode);
  act(() => {
    result.current.handleInputChange('model', model);
  });
  return result;
};

describe('optimizeContext 的画幅与实际下发保持同一判据', () => {
  it('i2i 配了 sizes：画幅照常告诉模型', () => {
    const result = withModel(TWO_MODELS, 'image2image', 'sized');
    expect(result.current.canPickI2ISize).toBe(true);
    expect(result.current.optimizeContext).toContain(
      'Target canvas: 1024x1536 pixels',
    );
  });

  it('切到没配 i2i sizes 的模型：inputs.size 仍是旧值，但不再告诉模型画幅', () => {
    const result = withModel(TWO_MODELS, 'image2image', 'sized');
    act(() => {
      result.current.handleInputChange('model', 'bare');
    });
    // 前提断言：陈旧值确实还在，否则这条用例什么都没守住 —— 分叉正来源于它。
    expect(result.current.inputs.size).toBe('1024x1536');
    expect(result.current.shapeMode).toBe('none');
    expect(result.current.canPickI2ISize).toBe(false);
    expect(result.current.optimizeContext).not.toContain('Target canvas');
  });

  it('t2i 不受这条判据影响，画幅照发', () => {
    const result = withModel(TWO_MODELS, 'text2image', 'bare');
    expect(result.current.optimizeContext).toContain(
      'Target canvas: 1024x1024 pixels',
    );
  });

  it('比例词档写成 aspect ratio，不写 pixels', () => {
    const result = withModel(T2I_RATIO, 'text2image', 'm');
    expect(result.current.optimizeContext).toContain('aspect ratio 16:9');
    expect(result.current.optimizeContext).not.toContain('pixels');
  });
});
