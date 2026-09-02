import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

vi.mock('../../../helpers', () => ({
  makeModelOptionRenderer: () => () => null,
  renderGroupOption: () => null,
  selectFilter: () => true,
  API: { get: vi.fn(), post: vi.fn() },
  showError: vi.fn(),
}));
vi.mock('../../../hooks/common/useModelNotes', () => ({
  useModelNotes: () => () => '',
}));
vi.mock('../../playground/PromptGuideTip', () => ({ default: () => null }));
vi.mock('../../playground/ImageUrlInput', () => ({ default: () => null }));

import ImageConfigPanel from '../ImageConfigPanel';

// 画幅交互：比例是图形按钮组，分辨率是下拉，下面写出算出来的最终像素。
//
// 守的是「用户选的东西真的变成了下发的 size」这条链的前半段。断了不报错——控件照样
// 在，只是点了没反应或者显示的像素跟实际出图对不上。

const base = {
  inputs: {
    group: '',
    model: 'u1.5',
    size: '2720x1536',
    aspectRatio: '16:9',
    sizeTier: 2048,
    seed: '',
    batchCount: 1,
    imageUrls: [],
  },
  groups: [],
  models: [],
  availableSizes: [],
  onInputChange: vi.fn(),
};

const renderPanel = (props) =>
  render(<ImageConfigPanel {...base} {...props} />);

describe('area 模式：比例按钮组 + 分辨率下拉', () => {
  it('每个比例出一个按钮，当前值是选中态', () => {
    renderPanel({
      shapeMode: 'area',
      availableRatios: ['1:1', '16:9', '9:16'],
      availableTiers: [2048],
      sizeAlign: 32,
    });
    for (const r of ['1:1', '16:9', '9:16']) {
      expect(screen.getByText(r)).not.toBeNull();
    }
    const active = screen.getByText('16:9').closest('button');
    expect(active.getAttribute('aria-pressed')).toBe('true');
    expect(
      screen.getByText('1:1').closest('button').getAttribute('aria-pressed'),
    ).toBe('false');
  });

  it('点比例按钮把值抬给调用方', () => {
    const onInputChange = vi.fn();
    renderPanel({
      shapeMode: 'area',
      availableRatios: ['1:1', '16:9'],
      availableTiers: [2048],
      sizeAlign: 32,
      onInputChange,
    });
    screen.getByText('1:1').closest('button').click();
    expect(onInputChange).toHaveBeenCalledWith('aspectRatio', '1:1');
  });

  // 一个只能选一项的下拉没有意义：只配一档时它已经生效了，直接隐藏，
  // 用户看到的控件数与改造前一致。
  it('只有一档时不渲染分辨率下拉', () => {
    renderPanel({
      shapeMode: 'area',
      availableRatios: ['16:9'],
      availableTiers: [2048],
      sizeAlign: 32,
    });
    expect(screen.queryByText('分辨率')).toBeNull();
  });

  it('多档时渲染分辨率下拉，标签写出各档算出来的像素', () => {
    renderPanel({
      shapeMode: 'area',
      availableRatios: ['16:9'],
      availableTiers: [1024, 2048],
      sizeAlign: 32,
    });
    expect(screen.queryByText('分辨率')).not.toBeNull();
    // 选中项的标签：面积基准 + 算出来的像素
    expect(screen.getByText(/2048（2720x1536）/)).not.toBeNull();
  });

  // 用户选的是比例和档位，真正下发的是算出来的 size——不写出来的话
  // 「我选了 2K 到底出多大」没有任何地方能看到。
  it('把最终像素写在下面', () => {
    renderPanel({
      shapeMode: 'area',
      availableRatios: ['16:9'],
      availableTiers: [2048],
      sizeAlign: 32,
    });
    expect(screen.getByText(/出图 2720x1536/)).not.toBeNull();
  });

  // 锁定态（打开历史会话）**不能**拿 inputs 里的比例/档位去高亮按钮：会话只存了
  // size，这两个字段从来没存过（改造前的老会话更不可能有），残留的是上一次草稿的
  // 选择——显示出来就是假信息。改为直接写出真正产出这张图的值。
  it('锁定态显示真实产出值，不渲染比例按钮与档位下拉', () => {
    renderPanel({
      shapeMode: 'area',
      availableRatios: ['1:1', '16:9'],
      availableTiers: [1024, 2048],
      sizeAlign: 32,
      disabled: true,
      inputs: { ...base.inputs, aspectRatio: '9:16', sizeTier: 1024 },
    });
    // size 是从会话里恢复的，它才是事实
    expect(screen.getByText('2720x1536')).not.toBeNull();
    // 残留的 9:16 / 1024 一个都不能露出来
    expect(screen.queryByText('9:16')).toBeNull();
    expect(screen.queryByText('分辨率')).toBeNull();
    expect(screen.queryByText(/1024（/)).toBeNull();
  });
});

describe('none 模式（图生图未在本 tab 开启画幅）', () => {
  it('比例按钮组与尺寸下拉都不出现', () => {
    renderPanel({ shapeMode: 'none', availableRatios: ['16:9'] });
    expect(screen.queryByText('宽高比')).toBeNull();
    expect(screen.queryByText('图片尺寸')).toBeNull();
    expect(screen.queryByText('输出尺寸')).toBeNull();
  });
});

describe('table 模式：维持原来的尺寸下拉', () => {
  it('不出比例按钮组', () => {
    renderPanel({
      shapeMode: 'table',
      availableSizes: ['1024x1024', '1664x928'],
      inputs: {
        ...base.inputs,
        aspectRatio: '',
        sizeTier: null,
        size: '1024x1024',
      },
    });
    expect(screen.queryByText('宽高比')).toBeNull();
    expect(screen.queryByText('图片尺寸')).not.toBeNull();
  });
});
