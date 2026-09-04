import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';

vi.mock('../../../helpers', () => ({
  API: { get: vi.fn(), post: vi.fn() },
  showError: vi.fn(),
  processGroupsData: (data) =>
    (data || []).map((g) => ({ label: g, value: g })),
  processModelsData: (list, cur) => ({
    modelOptions: (list || []).map((m) => ({ label: m, value: m })),
    selectedModel: (list || []).includes(cur) ? cur : (list || [])[0],
  }),
  getUserModelsCached: vi.fn(() =>
    Promise.resolve({ success: true, data: ['z-image', 'sensenova-u1.5'] }),
  ),
  cachedGet: vi.fn((url) =>
    String(url).includes('group')
      ? Promise.resolve({ success: true, data: ['default', 'premium'] })
      : Promise.resolve({
          success: true,
          data: [
            { model_name: 'z-image', enable_groups: ['default', 'premium'] },
            {
              model_name: 'sensenova-u1.5',
              enable_groups: ['default', 'premium'],
            },
          ],
        }),
  ),
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

// 从别的页面切回来时，mount 会自动把用户放回「还在跑」的那条会话。恢复会话有两条路径，
// 以前只有点历史列表那条（openHistoryItem）会把会话参数写回左侧面板，mount 这条只切
// 会话、不回填 —— 于是中间对话区是这条会话的，左侧面板却是另一套值。
//
// 之所以不是"显示旧值"而是"显示错值"，在于两边的到达时机：会话列表由 localStorage
// **同步**读出，首轮渲染就把会话锁上；而分组/模型是 HTTP 异步拿的，响应回来时
// loadGroups / loadModels 里的回填已被 lockedRef 挡掉，双双停在空串；画幅那条 effect
// 则赶在上锁之前跑了一轮，那时 inputs.model 还是空的，于是落到兜底档，显示出一个既不
// 属于这条会话、也不属于任何选中模型的尺寸。
//
// 所以这条用例必须让候选**异步**到达（cachedGet / getUserModelsCached 都是 Promise），
// 一开始就把值塞进去是复现不出来的。
const CFG = JSON.stringify({
  models: {
    'z-image': { tabs: { text2image: { sizes: ['1024x1024'] } } },
    'sensenova-u1.5': {
      sizeAlign: 32,
      tabs: {
        text2image: { aspectRatios: ['1:1', '9:16'], sizeTiers: ['2048'] },
      },
    },
  },
});

// 刻意都不是页面默认值：分组不是首个、模型不是首个，这样"没回填"才看得出来。
const RUNNING = [
  {
    id: 'img-1',
    group: 'premium',
    model: 'sensenova-u1.5',
    size: '1536x2720',
    aspectRatio: '9:16',
    // 数字不是字符串:运营在配置里填的是字符串，但 normalizeTierList 用 parseInt
    // 统一成数字，会话里存下来的就是数字。夹具跟真实数据一致才守得住面板里那个
    // availableTiers.includes(inputs.sizeTier) 的严格比较。
    sizeTier: 2048,
    seed: '',
    batchCount: 1,
    title: 'x',
    messages: [
      {
        id: 'img-1-a',
        role: 'assistant',
        status: 'pending',
        imageTasks: [{ taskId: 't1', status: 'pending' }],
        images: [],
      },
    ],
  },
];

beforeEach(() => {
  localStorage.setItem(
    'image_playground_conversations',
    JSON.stringify(RUNNING),
  );
});
afterEach(() => localStorage.clear());

const renderImage = () =>
  renderHook(() => useImageGeneration({ mode: 'text2image' }), {
    wrapper: ({ children }) => (
      <UserContext.Provider
        value={[{ user: { id: 1, group: 'default' } }, () => {}]}
      >
        <StatusContext.Provider
          value={[{ status: { ImageModelSizeConfig: CFG } }, () => {}]}
        >
          {children}
        </StatusContext.Provider>
      </UserContext.Provider>
    ),
  });

describe('mount 自动回到进行中的会话', () => {
  it('左侧配置跟着一起恢复，而不是停在默认值', async () => {
    const { result } = renderImage();
    await waitFor(() => expect(result.current.locked).toBe(true));
    // 给两个 HTTP 回填留出落地时间：回归版本正是在这一步把 inputs 覆盖/留空的。
    await waitFor(() =>
      expect(result.current.inputs.model).toBe('sensenova-u1.5'),
    );
    expect(result.current.currentConvId).toBe('img-1');
    expect(result.current.inputs.group).toBe('premium');
    expect(result.current.inputs.size).toBe('1536x2720');
    // 比例与档位也要回来：锁定态的两个控件按它俩渲染，缺了就退化成一行纯文字。
    expect(result.current.inputs.aspectRatio).toBe('9:16');
    expect(result.current.inputs.sizeTier).toBe(2048);
  });
});
