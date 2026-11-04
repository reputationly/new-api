import React from 'react';
import { beforeEach, describe, it, expect, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

// 玩法页（TabPanel）也要折叠"没有可用渠道"的模型。
//
// 这条来自一次真实抱怨：分类页已经折叠了，但运营日常是在**玩法页**（文生视频/参考生视频…）
// 改参数，那一页仍然把渠道已停用/已删除的模型一张张列出来，页面越翻越长。
// 判据与分类页同源（useModelAvailability），不然两个页面对同一个模型会给出相反答案。
vi.mock('../../../helpers', () => ({ API: { get: vi.fn() } }));

import { API } from '../../../helpers';
import TabPanel from '../TabPanel';
import { getPlaygroundTab } from '../../../constants/playgroundAdmin.constants';
import { resetModelAvailabilityCache } from '../useModelAvailability';

const T2V = getPlaygroundTab('video', 'text2video');

const makeDraft = (models) => ({
  // storeKey 由 getTabStoreKey 决定（这里的视频模型都落在 VideoModelConfig 这一份里）
  stores: { VideoModelConfig: { models, defaults: {} } },
  tabConfig: {},
  allModels: [],
  options: {},
  patchTabConfig: vi.fn(),
  setTabField: vi.fn(),
  setModelField: vi.fn(),
  addModelToTab: vi.fn(),
  removeModelFromTab: vi.fn(),
});

const renderTab = (models) =>
  render(<TabPanel category='video' tab={T2V} draft={makeDraft(models)} />);

// 两个模型都挂在本 tab 下，但只有一个有可用渠道。
const MODELS = {
  'h3-good': { tabs: { text2video: {} }, sizes: ['768P'] },
  'h3-stale': { tabs: { text2video: {} }, sizes: ['768P'] },
};

beforeEach(() => {
  vi.clearAllMocks();
  resetModelAvailabilityCache();
});

describe('玩法页把无可用渠道的模型折叠起来', () => {
  it('默认收起只留汇总，展开后才出现', async () => {
    API.get.mockResolvedValue({ data: { data: { models: ['h3-good'] } } });
    renderTab(MODELS);

    const summary = await screen.findByText(/个模型当前没有可用渠道/);
    // 有渠道的那个照常可见
    expect(screen.queryByText('h3-good')).not.toBeNull();
    // 没渠道的默认不出现
    expect(screen.queryByText('h3-stale')).toBeNull();

    fireEvent.click(summary);
    expect(screen.queryByText('h3-stale')).not.toBeNull();
  });

  // 折叠是"把东西藏起来"的动作，只能建立在确定的信息上。
  it('可用性接口失败时不折叠（fail-open）', async () => {
    API.get.mockRejectedValue(new Error('boom'));
    renderTab(MODELS);

    await waitFor(() => expect(API.get).toHaveBeenCalled());
    expect(screen.queryByText(/个模型当前没有可用渠道/)).toBeNull();
    expect(screen.queryByText('h3-stale')).not.toBeNull();
  });

  // 全都可用时不该出现任何折叠区（避免页面无端多一行）。
  it('全部有渠道时不显示汇总行', async () => {
    API.get.mockResolvedValue({
      data: { data: { models: ['h3-good', 'h3-stale'] } },
    });
    renderTab(MODELS);

    await waitFor(() => expect(API.get).toHaveBeenCalled());
    await waitFor(() =>
      expect(screen.queryByText(/个模型当前没有可用渠道/)).toBeNull(),
    );
  });
});
