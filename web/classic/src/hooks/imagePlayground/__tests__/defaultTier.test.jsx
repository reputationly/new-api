import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { renderHook, act } from '@testing-library/react';

vi.mock('../../../helpers', () => ({
  API: {
    get: vi.fn(() => Promise.reject(new Error('no net'))),
    post: vi.fn(() => Promise.resolve({ data: { data: [] } })),
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

// 进页面时默认选中哪一档画质。
//
// 两头都是坑，而且都不报错：
//   - 取最大档 → Ideogram-4 默认 2048²，实测 209 秒，第一次点生成的人先等三分半。
//   - 取最小档 → 就是这个功能存在的理由（「出图分辨率太低」）没被解决。
// 所以取中间档，两档时取高的那档。
//
// tiers 由 normalizeTierList 升序排好，所以「中间偏上」要用 ceil 不是 floor ——
// 两档时 floor((2-1)/2)=0 会取到最低档，而**推荐档里有四个模型正好是两档**
// （kr2 / z-image / qwen-image / qwen-image-edit），一个 floor 就让它们全部默认最低。

const cfg = (tiers) =>
  JSON.stringify({
    models: {
      m: {
        sizeAlign: 32,
        tabs: { text2image: { aspectRatios: ['1:1'], sizeTiers: tiers } },
      },
    },
  });

const pickTier = (tiers) => {
  const { result } = renderHook(
    () => useImageGeneration({ mode: 'text2image' }),
    {
      wrapper: ({ children }) => (
        <UserContext.Provider value={[{ user: { id: 1 } }, () => {}]}>
          <StatusContext.Provider
            value={[{ status: { ImageModelSizeConfig: cfg(tiers) } }, () => {}]}
          >
            {children}
          </StatusContext.Provider>
        </UserContext.Provider>
      ),
    },
  );
  act(() => {
    result.current.handleInputChange('model', 'm');
  });
  return result.current;
};

describe('默认画质档', () => {
  it('两档 → 取高的那档（floor 会取到最低档，四个推荐档正好是两档）', () => {
    const r = pickTier(['1024', '1536']);
    expect(r.inputs.sizeTier).toBe(1536);
    expect(r.inputs.size).toBe('1536x1536');
  });

  it('三档 → 取中间那档，不是最大的（最大档 209 秒会劝退首次使用者）', () => {
    const r = pickTier(['1024', '1536', '2048']);
    expect(r.inputs.sizeTier).toBe(1536);
  });

  it('一档 → 就是它', () => {
    const r = pickTier(['2048']);
    expect(r.inputs.sizeTier).toBe(2048);
  });

  // 配置顺序是运营写的，不保证有序；normalizeTierList 会升序排好，
  // 默认档必须按排序后的位置取，否则同一份配置换个书写顺序就默认到别的档。
  it('乱序配置也按大小取中间档', () => {
    const r = pickTier(['2048', '1024', '1536']);
    expect(r.inputs.sizeTier).toBe(1536);
  });
});
