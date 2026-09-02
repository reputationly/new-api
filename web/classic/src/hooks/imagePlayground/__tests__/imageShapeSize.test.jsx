import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
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

import { API } from '../../../helpers';
import { StatusContext } from '../../../context/Status';
import { UserContext } from '../../../context/User';
import { useImageGeneration } from '../useImageGeneration';

// 图生图 + 同时配了「尺寸白名单」与「比例 + 分辨率档」。
// 管理页把这种组合标为「尺寸列表不生效，但不报错」——必须真的不报错。
const BOTH = JSON.stringify({
  models: {
    m: {
      sizeAlign: 32,
      tabs: {
        image2image: {
          sizes: ['1024x1024'],
          aspectRatios: ['16:9'],
          sizeTiers: [2048],
        },
      },
    },
  },
});

// mock 不会在用例间自动清空。不清的话下面用 find 找"生图请求"会捞到上一条用例发的那次
// —— 断言看着过了/挂了都跟本用例无关，是最典型的假测试。
beforeEach(() => {
  API.post.mockClear();
});

const renderImage = (config, mode = 'image2image') =>
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

describe('area 模式下 i2i 的白名单机制必须让开', () => {
  // ⚠️ 这条曾经是**页面卡死**，不是"多一个没用的控件"：白名单校验 effect 把算出来的
  // 2720x1536 改回白名单首档，area effect 又改回来，两个 effect 互相触发成死循环。
  // 回归前这个用例跑 249 秒不收敛（浏览器里是 Maximum update depth exceeded），
  // 修完 8 毫秒。所以它断言的不是某个值，而是「能跑完」。
  it('两套都配也不会死循环，size 收敛到算出来的像素', () => {
    const { result } = renderImage(BOTH);
    act(() => {
      result.current.handleInputChange('model', 'm');
    });
    expect(result.current.inputs.size).toBe('2720x1536');
    expect(result.current.shapeMode).toBe('area');
    // 白名单让开 = 尺寸下拉不出现、上传时不再"就近选档"、校验 effect 不再插手
    expect(result.current.canPickI2ISize).toBe(false);
  });

  // canPickI2ISize 关掉之后，提交那段的原条件 `canPickI2ISize && ...` 就为假了。
  // 少了 usesComputedShape 那一支，i2i 会一个 size 都不发——高分辨率白算，且不报错。
  it('i2i 仍然把算出来的 size 发出去', async () => {
    const { result } = renderImage(BOTH);
    act(() => {
      result.current.handleInputChange('model', 'm');
      result.current.handleInputChange('imageUrls', [
        'data:image/png;base64,x',
      ]);
    });
    await act(async () => {
      await result.current.generate('一只猫');
    });
    const call = API.post.mock.calls.findLast((c) =>
      String(c[0]).includes('/images/'),
    );
    expect(call, '没有发出生图请求').toBeTruthy();
    expect(call[1].size).toBe('2720x1536');
  });
});

// 图生图从**模型级 / 分类默认值**继承来的比例，不能替运营开"下发 size"这个能力：
// 那些值多半是给文生图配的，而 i2i 的 size 会流向该 tab 下所有渠道，gpt-image /
// dall-e 的 edits 只认固定档位（后端对 dall-e 系不合规尺寸直接 400）。
const INHERITED_ONLY = JSON.stringify({
  default: ['16:9', '1:1'],
  models: { m: { tabs: { image2image: {} } } },
});

describe('图生图的画幅是 tab 级 opt-in', () => {
  it('只从默认值继承到比例时，不出画幅控件、也不发 size', async () => {
    const { result } = renderImage(INHERITED_ONLY);
    act(() => {
      result.current.handleInputChange('model', 'm');
      result.current.handleInputChange('imageUrls', [
        'data:image/png;base64,x',
      ]);
    });
    expect(result.current.shapeMode).toBe('none');
    await act(async () => {
      await result.current.generate('一只猫');
    });
    const call = API.post.mock.calls.findLast((c) =>
      String(c[0]).includes('/images/'),
    );
    expect(call, '没有发出生图请求').toBeTruthy();
    expect(call[1].size).toBeUndefined();
  });

  it('文生图不受这条限制——它本来就一直有尺寸控件', () => {
    const { result } = renderImage(INHERITED_ONLY, 'text2image');
    act(() => {
      result.current.handleInputChange('model', 'm');
    });
    // 继承来的比例词进尺寸下拉、原样下发，与改造前一致
    expect(result.current.shapeMode).toBe('table');
    expect(result.current.availableSizes).toEqual(['16:9', '1:1']);
  });
});

// 运营填了解析不出的比例写法（中文全角冒号最常见，中文输入法默认就是它）。
// computeImageSize 返回 ''，它的注释约定"调用方据此退回不下发 size"——不判空的话
// 每次请求都会带一个 "size": ""，而且零用户操作就触发。
const BAD_RATIO = JSON.stringify({
  models: {
    m: {
      tabs: { text2image: { aspectRatios: ['16：9'], sizeTiers: [2048] } },
    },
  },
});

describe('算不出像素时一个 size 字段都不发', () => {
  it('文生图：比例解析不出 → 不下发 size，而不是发空串', async () => {
    const { result } = renderImage(BAD_RATIO, 'text2image');
    act(() => {
      result.current.handleInputChange('model', 'm');
    });
    expect(result.current.inputs.size).toBe('');
    await act(async () => {
      await result.current.generate('一只猫');
    });
    const call = API.post.mock.calls.findLast((c) =>
      String(c[0]).includes('/images/'),
    );
    expect(call, '没有发出生图请求').toBeTruthy();
    expect(call[1]).not.toHaveProperty('size');
  });
});
