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
    // 选中态必须用 semi-color-* 色板:classic 的 tailwind.config.js 把 theme.colors
    // 整份换成了 semi 变量,bg-blue-500 / border-gray-200 这类默认色板在构建产物里
    // 根本不存在。aria-pressed 是对的、类名也写了,页面上却没有任何选中效果 ——
    // 这条曾在线上真实发生,靠人眼在 jsdom 里看不出来,只能守类名。
    const inactive = screen.getByText('1:1').closest('button');
    expect(active.className).toMatch(/(^|\s)bg-semi-color-primary(\s|$)/);
    expect(inactive.className).not.toMatch(/(^|\s)bg-semi-color-primary(\s|$)/);
    for (const btn of [active, inactive]) {
      expect(btn.className).not.toMatch(
        /(^|\s)(bg|border|text)-(blue|gray|white)/,
      );
    }
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

  // 下拉里**只出档名**，不出像素也不出面积基准。基准（2048）是算像素用的中间量，
  // 既不是宽也不是高；而像素会随比例变，写进四个选项会让文字跟着一起跳。
  // 真实像素由下面那一行单独给出，一行代替 N 个括号。
  it('多档时渲染画质下拉，标签只有档名', () => {
    renderPanel({
      shapeMode: 'area',
      availableRatios: ['16:9'],
      availableTiers: [1024, 2048],
    });
    expect(screen.queryByText('画质')).not.toBeNull();
    expect(screen.getByText('超清')).not.toBeNull();
    // 面积基准绝不能露给用户。
    expect(screen.queryByText(/2048（/)).toBeNull();
  });

  // 用户选的是比例和档位，真正下发的是算出来的 size——不写出来的话
  // 「我选了超清到底出多大」没有任何地方能看到。
  // 带上百万像素是给一个**跨比例可比**的量：同一档在 1:1 与 16:9 下长宽差很多、
  // 面积却基本一致，只看长宽会以为"换了比例就变小了"。
  it('把最终像素与百万像素写在下面', () => {
    renderPanel({
      shapeMode: 'area',
      availableRatios: ['16:9'],
      availableTiers: [2048],
    });
    expect(screen.getByText(/出图 2720×1536 · 4\.2MP/)).not.toBeNull();
  });

  // 锁定态分两种，判据是**这条会话自己有没有存过比例与档位**，不是"锁没锁"。
  //
  // 新会话存了 → 整套控件按原样渲染（只是 disabled），「生成前」「生成后」长得一样。
  // 原先无论哪种都退化成一行纯文字 `1152x2048`，用户会以为自己的设置丢了。
  it('锁定态：会话存了比例与档位时，照原样渲染两个控件', () => {
    renderPanel({
      shapeMode: 'area',
      availableRatios: ['1:1', '16:9'],
      availableTiers: [1024, 2048],
      sizeAlign: 32,
      disabled: true,
      inputs: { ...base.inputs, aspectRatio: '16:9', sizeTier: 2048 },
    });
    const active = screen.getByText('16:9').closest('button');
    expect(active.getAttribute('aria-pressed')).toBe('true');
    expect(active.getAttribute('disabled')).not.toBeNull();
    expect(screen.getByText('画质')).not.toBeNull();
    expect(screen.getByText(/出图 2720×1536/)).not.toBeNull();
  });

  // 老会话只有 size。那时 inputs 里残留的是**上一次草稿**的选择，拿它高亮按钮就是
  // 显示假信息，比不显示更糟——所以仍旧只写出 size。这条守的就是"不许拿残留值顶替"。
  it('锁定态：老会话没存过比例与档位时，只写出 size', () => {
    renderPanel({
      shapeMode: 'area',
      availableRatios: ['1:1', '16:9'],
      availableTiers: [1024, 2048],
      sizeAlign: 32,
      disabled: true,
      inputs: { ...base.inputs, aspectRatio: '', sizeTier: null },
    });
    expect(screen.getByText('2720x1536')).not.toBeNull();
    expect(screen.queryByText('16:9')).toBeNull();
    expect(screen.queryByText('画质')).toBeNull();
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
