import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { renderHook, waitFor } from '@testing-library/react';

vi.mock('../../../helpers', () => ({
  API: { get: vi.fn(() => new Promise(() => {})), post: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showInfo: vi.fn(),
  processGroupsData: (data) =>
    (data || []).map((g) => ({ label: g, value: g })),
  processModelsData: (list, cur) => ({
    modelOptions: (list || []).map((m) => ({ label: m, value: m })),
    selectedModel: (list || []).includes(cur) ? cur : (list || [])[0],
  }),
  getUserModelsCached: vi.fn(() =>
    Promise.resolve({ success: true, data: ['wan2.2', 'ltx2.5-hd'] }),
  ),
  cachedGet: vi.fn((url) =>
    String(url).includes('group')
      ? Promise.resolve({ success: true, data: ['default', 'premium'] })
      : Promise.resolve({
          success: true,
          data: [
            { model_name: 'wan2.2', enable_groups: ['default', 'premium'] },
            { model_name: 'ltx2.5-hd', enable_groups: ['default', 'premium'] },
          ],
        }),
  ),
  getLogo: () => '',
  stringToColor: () => '#000',
}));
vi.mock('../../../helpers/playgroundMediaStorage', () => ({
  persistWithMedia: vi.fn(),
  hydrateConversationsFromStorage: vi.fn(() => Promise.resolve([])),
  stripUnresolvedMediaRefs: (x) => x,
  isMediaRef: () => false,
}));

import { StatusContext } from '../../../context/Status';
import { UserContext } from '../../../context/User';
import { useVideoGeneration } from '../useVideoGeneration';

// 与 imagePlayground/resumeRestoresInputs 同一个缺陷、同一种修法（两个 hook 各有一份
// applyConvToInputs）：mount 自动回到进行中的会话时，以前只切会话、不回填左侧面板，
// 而分组/模型的 HTTP 回填又被 lockedRef 挡掉，于是面板停在空串。详细成因见图像那份。
const RUNNING = [
  {
    id: 'vid-1',
    group: 'premium',
    model: 'ltx2.5-hd',
    size: '1280x720',
    seconds: 8,
    seed: '',
    batchCount: 1,
    title: 'x',
    messages: [
      {
        id: 'vid-1-1757000000000',
        role: 'assistant',
        status: 'in_progress',
        taskId: 'task-1',
      },
    ],
  },
];

beforeEach(() => {
  localStorage.setItem(
    'video_playground_conversations',
    JSON.stringify(RUNNING),
  );
});
afterEach(() => localStorage.clear());

const wrapper = ({ children }) => (
  <UserContext.Provider
    value={[{ user: { id: 1, group: 'default' } }, () => {}]}
  >
    <StatusContext.Provider value={[{ status: {} }, () => {}]}>
      {children}
    </StatusContext.Provider>
  </UserContext.Provider>
);

describe('视频体验区 mount 自动回到进行中的会话', () => {
  it('左侧配置跟着一起恢复，而不是停在默认值', async () => {
    const { result } = renderHook(
      () => useVideoGeneration({ mode: 'text2video' }),
      { wrapper },
    );
    await waitFor(() => expect(result.current.currentConvId).toBe('vid-1'));
    await waitFor(() => expect(result.current.inputs.model).toBe('ltx2.5-hd'));
    expect(result.current.inputs.group).toBe('premium');
    expect(result.current.inputs.seconds).toBe(8);
  });
});
