import React from 'react';
import { beforeEach, describe, it, expect, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

// 分类页会问一次「哪些模型当前有可用渠道」。默认全部可用，需要折叠的用例自己改。
vi.mock('../../../helpers', () => ({ API: { get: vi.fn() } }));

import { API } from '../../../helpers';
import CategoryPanel from '../CategoryPanel';
import { resetModelAvailabilityCache } from '../useModelAvailability';
import { getPlaygroundCategory } from '../../../constants/playgroundAdmin.constants';

// 体验区管理 → 视频模型（分类页）里「没挂玩法的模型」的模型级字段编辑区。
//
// 这条守的是一次真实事故：那一块**渲染出了标题、字段却一个都不显示**，而且不报错 ——
// 原因是 FieldInput 只认「tab 字段名」这种字符串形态，模型级字段是**整个描述对象**
// （有自己的 label/type/help，不在 PLAYGROUND_FIELD_META 里），查不到就静默 return null。
// 表现是运营在页面上找不到「超分倍率」，于是超分段只能发缺省的 4.0，RelayVSR 每次失败
// 并静默降级。jsdom 里只要把这一块渲染出来就能发现，所以必须有一个渲染级测试。

const VIDEO = getPlaygroundCategory('video');

const makeDraft = (models) => ({
  stores: { VideoModelConfig: { models, defaults: {} } },
  tabConfig: {},
  setDefaultField: vi.fn(),
  setModelField: vi.fn(),
  addBareModel: vi.fn(),
  removeModelFromTab: vi.fn(),
});

const renderPanel = (models) =>
  render(<CategoryPanel category={VIDEO} draft={makeDraft(models)} />);

const mockAvailability = (names) =>
  API.get.mockResolvedValue({ data: { data: { models: names } } });

beforeEach(() => {
  vi.clearAllMocks();
  // 可用性结果在模块级缓存（同一页面两个面板共用一次请求），用例之间必须清掉，
  // 否则第二个用例拿到的是第一个用例的集合。
  resetModelAvailabilityCache();
  mockAvailability(['relayvsr', 'minimax-h3-fl2va']);
});

describe('没挂玩法的模型：模型级字段要真的渲染出来', () => {
  it('超分模型那一行有「超分倍率」输入框', () => {
    renderPanel({
      relayvsr: { tabs: {}, capabilities: ['视频超分'], sizes: ['2K'] },
    });
    // 标题（一直有，之前就是它能显示、字段不显示）
    expect(screen.queryByText(/该模型没有挂任何玩法/)).not.toBeNull();
    // 字段本身
    expect(screen.queryByText('超分倍率')).not.toBeNull();
  });

  it('填值写回的是模型级字段 srRatio', () => {
    const draft = makeDraft({
      relayvsr: { tabs: {}, capabilities: ['视频超分'], sizes: ['2K'] },
    });
    render(<CategoryPanel category={VIDEO} draft={draft} />);
    // 这一块里还有别的数字输入（驱动音频时长上限那类），按标签定位到那一格
    const input = screen
      .getByText('超分倍率')
      .closest('div')
      .querySelector('input');
    fireEvent.change(input, { target: { value: '2' } });
    expect(draft.setModelField).toHaveBeenCalledWith(
      'VideoModelConfig',
      'relayvsr',
      'srRatio',
      expect.anything(),
    );
  });

  // 挂了玩法的模型在各自的 tab 页里编辑模型级字段（TabPanel 那边故意把 bare 字段滤掉），
  // 这里不能再出现一份 —— 两份 UI 改同一份数据，迟早有一处被绕过。
  it('挂了玩法的模型不显示超分倍率', () => {
    renderPanel({
      'minimax-h3-fl2va': { tabs: { text2video: {} }, sizes: ['768P'] },
    });
    expect(screen.queryByText('超分倍率')).toBeNull();
  });
});

describe('没有可用渠道的模型折叠起来', () => {
  // 可用性是异步取的：断言前必须等它落地，否则测的是"还没拿到"的那一帧。
  it('默认收起，只留一行汇总；展开后才显示', async () => {
    mockAvailability([]); // 一个都没有可用渠道
    renderPanel({
      relayvsr: { tabs: {}, capabilities: ['视频超分'], sizes: ['2K'] },
      seedvr2: { tabs: {}, capabilities: ['视频超分'], sizes: ['1080P'] },
    });
    const summary = await screen.findByText(/个模型当前没有可用渠道/);
    expect(screen.queryByText('超分倍率')).toBeNull();

    fireEvent.click(summary);
    // 展开后两个超分模型都会渲染出这一格
    expect((await screen.findAllByText('超分倍率')).length).toBe(2);
  });

  // 折叠是"把东西藏起来"的动作，只能建立在确定的信息上：接口失败时一律不折叠。
  it('可用性接口失败时不折叠（fail-open）', async () => {
    API.get.mockRejectedValue(new Error('boom'));
    renderPanel({
      relayvsr: { tabs: {}, capabilities: ['视频超分'], sizes: ['2K'] },
    });
    await waitFor(() => expect(API.get).toHaveBeenCalled());
    expect(screen.queryByText(/个模型当前没有可用渠道/)).toBeNull();
    expect(screen.getByText('超分倍率')).toBeTruthy();
  });
});
