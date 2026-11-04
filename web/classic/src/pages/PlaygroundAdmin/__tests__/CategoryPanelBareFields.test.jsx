import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';

import CategoryPanel from '../CategoryPanel';
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
    const input = screen.getByText('超分倍率').closest('div').querySelector('input');
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
