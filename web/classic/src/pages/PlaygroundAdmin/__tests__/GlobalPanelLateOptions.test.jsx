import React from 'react';
import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, act } from '@testing-library/react';
import GlobalPanel from '../GlobalPanel';

vi.mock('../../../helpers', () => ({
  API: { get: vi.fn(), post: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
  showInfo: vi.fn(),
}));

// 候选一定是**挂载之后**才到的:分组与模型全集各走一次接口,页面先渲染再回填。
// Semi 2.72 的 Select 在 filter + allowCreate + 受控 value 下,对挂载后才变化的
// optionList 沿用旧快照,下拉永远「暂无数据」——线上体验区管理页就是这样,接口 200
// 返回了 ["default","vip",…] 却一个都选不了。所以这里必须模拟「先空、后到」,而不是
// 一开始就把候选传进去:后者从来没坏过,守不住这条。绕法见 components/common/CreatableSelect。
const draftWith = (allGroups, allModels) => ({
  tabConfig: {
    __global: { promptOptimize: { enabled: true, model: '', group: '' } },
  },
  options: { UserUsableGroups: JSON.stringify({ default: '默认' }) },
  allGroups,
  allModels,
  patchTabConfig: vi.fn(),
});

const open = async (el) => {
  await act(async () => {
    el.dispatchEvent(new MouseEvent('click', { bubbles: true }));
  });
  return [...document.querySelectorAll('.semi-select-option')].map(
    (o) => o.textContent,
  );
};

afterEach(() => {
  // Semi 的下拉挂在 document.body 上,RTL 的 cleanup 只回收自己建的容器。
  document.body.innerHTML = '';
});

describe('GlobalPanel 的两个候选下拉', () => {
  it('候选在挂载之后才到,打开时也要列出来', async () => {
    const { container, rerender } = render(
      <GlobalPanel draft={draftWith([], [])} />,
    );
    await act(async () => {
      rerender(
        <GlobalPanel
          draft={draftWith(
            ['default', 'vip'],
            [
              {
                model_name: 'qwen3.8-27b',
                supported_endpoint_types: ['openai'],
                enable_groups: ['default'],
              },
            ],
          )}
        />,
      );
    });
    const selects = container.querySelectorAll('.semi-select');
    expect(selects.length).toBe(2);
    expect(await open(selects[0])).toEqual(['default', 'vip（非通用）']);
    expect(await open(selects[1])).toContain('qwen3.8-27b');
  });
});
