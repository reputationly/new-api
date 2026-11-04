import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import RatioPicker from '../RatioPicker';

// 画幅选择器：图片体验区与视频体验区共用。守三件事 ——
//   1. 显示顺序按「宽→方→竖」，与运营录入顺序无关（录入是横竖成对挨着，折行会把
//      配对拆到两行）；
//   2. 固定 6 列折行，11 个档位落成 6+5、第二行末尾空一格（单排时每格 17px，
//      比里面的 20px 方块还窄）；
//   3. 「跟随上传素材」是模式不是比例：图标另做、钉在第一格。

const H3_RATIOS = [
  '16:9',
  '9:16',
  '1:1',
  '4:3',
  '3:4',
  '21:9',
  '9:21',
  '5:4',
  '4:5',
  '3:2',
  '2:3',
];

const titles = () =>
  screen.getAllByRole('button').map((b) => b.getAttribute('title'));

const renderPicker = (props) =>
  render(<RatioPicker choices={H3_RATIOS} value='' onChange={vi.fn()} {...props} />);

describe('排序：宽 → 方 → 竖', () => {
  it('与配置顺序无关，按比例降序排', () => {
    renderPicker();
    expect(titles()).toEqual([
      '21:9',
      '16:9',
      '3:2',
      '4:3',
      '5:4',
      '1:1',
      '4:5',
      '3:4',
      '2:3',
      '9:16',
      '9:21',
    ]);
  });

  // 解析不出比例的（运营可以录自定义写法）不能把排序搞崩，排最后并保持配置顺序。
  it('无法解析的档位排在最后', () => {
    renderPicker({ choices: ['1:1', 'bogus', '16:9', 'also-bogus'] });
    expect(titles()).toEqual(['16:9', '1:1', 'bogus', 'also-bogus']);
  });
});

describe('折行：固定 6 列', () => {
  it('11 个档位落成 6+5', () => {
    const { container } = renderPicker();
    const grid = container.firstChild;
    expect(grid.style.gridTemplateColumns).toBe(
      'repeat(6, minmax(0, 1fr))',
    );
    const buttons = screen.getAllByRole('button');
    expect(buttons).toHaveLength(11);
    // 第 7 个换行（第二行第一个）是 4:5
    expect(buttons[6].getAttribute('title')).toBe('4:5');
  });

  // 只有一两个档位的模型不该得到一个孤零零的小方块，仍然铺满整行。
  it('不足 6 个时按实际个数等分', () => {
    const { container } = renderPicker({ choices: ['1:1', '16:9'] });
    expect(container.firstChild.style.gridTemplateColumns).toBe(
      'repeat(2, minmax(0, 1fr))',
    );
  });
});

describe('跟随上传素材', () => {
  const withFollow = (extra = {}) =>
    renderPicker({
      choices: ['auto', ...H3_RATIOS],
      followValue: 'auto',
      followLabel: '跟随',
      followHint: '画幅跟随上传的素材',
      ...extra,
    });

  it('钉在第一格，图标与标签都另做一套', () => {
    withFollow();
    const buttons = screen.getAllByRole('button');
    expect(buttons[0].getAttribute('title')).toBe('跟随');
    // 12 个正好填满两行
    expect(buttons).toHaveLength(12);
    expect(buttons[6].getAttribute('title')).toBe('1:1');
  });

  it('点它抬的是档位值而不是文字', () => {
    const onChange = vi.fn();
    withFollow({ onChange });
    screen.getAllByRole('button')[0].click();
    expect(onChange).toHaveBeenCalledWith('auto');
  });

  it('说明只在选中它时出现', () => {
    const { unmount } = withFollow({ value: 'auto' });
    expect(screen.queryByText('画幅跟随上传的素材')).not.toBeNull();
    unmount();
    withFollow({ value: '16:9' });
    expect(screen.queryByText('画幅跟随上传的素材')).toBeNull();
  });
});

describe('交互', () => {
  it('点击把档位值抬给调用方，当前值是选中态', () => {
    const onChange = vi.fn();
    renderPicker({ value: '16:9', onChange });
    const active = screen.getByTitle('16:9');
    expect(active.getAttribute('aria-pressed')).toBe('true');
    expect(screen.getByTitle('1:1').getAttribute('aria-pressed')).toBe('false');
    screen.getByTitle('1:1').click();
    expect(onChange).toHaveBeenCalledWith('1:1');
  });

  it('禁用时整组按钮都不可点', () => {
    renderPicker({ disabled: true });
    for (const b of screen.getAllByRole('button')) {
      expect(b.getAttribute('disabled')).not.toBeNull();
    }
  });
});
