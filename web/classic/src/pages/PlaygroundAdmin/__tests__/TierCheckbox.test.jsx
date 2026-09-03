import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render, within, act } from '@testing-library/react';

import FieldInput from '../FieldInput';

// 画质档从「自由文本列表」改成复选框之后的两条约束。
//
// 1. **只出档名，不出数字**。面积基准是算像素用的中间量（2048 既不是宽也不是高，
//    16:9 下算出来是 2720×1536），让运营去理解它没有意义，填错还会静默出错档。
// 2. **老配置里的非标准档位不能被吞掉**。这个字段原来是自由文本，运营手填过什么都
//    有可能（4096 更是被文档明确提过）。只按白名单过滤的话它们会渲染成"一个都没勾"，
//    运营随手点一下就把配置覆盖掉 —— 而体验区那边还在按旧值出图，两处说法不一致
//    且全程无报错。

const renderTiers = (value, onChange = () => {}) =>
  render(<FieldInput field='sizeTiers' value={value} onChange={onChange} />);

const clickLabel = (container, text) => {
  const el = within(container).getByText(text);
  act(() => {
    el.click();
  });
};

describe('画质档复选框', () => {
  it('只渲染档名，面积基准不出现在界面上', () => {
    const { container } = renderTiers(['1024', '2048']);
    const scope = within(container);
    expect(scope.getByText('标准')).toBeTruthy();
    expect(scope.getByText('高清')).toBeTruthy();
    expect(scope.getByText('超清')).toBeTruthy();
    expect(scope.queryByText('1024')).toBeNull();
    expect(scope.queryByText('2048')).toBeNull();
  });

  it('三档都能取消——只给高清以上或只给标准都是合法的产品决策', () => {
    const onChange = vi.fn();
    const { container } = renderTiers(['1024', '1536'], onChange);
    clickLabel(container, '标准');
    expect(onChange).toHaveBeenCalledWith(['1536']);
  });

  it('全部取消 → 写 undefined（=不展示画质选择器，回到只由宽高比定画幅）', () => {
    const onChange = vi.fn();
    const { container } = renderTiers(['1024'], onChange);
    clickLabel(container, '标准');
    expect(onChange).toHaveBeenCalledWith(undefined);
  });

  // 这条守的是「改配置形态时不吞掉运营已有的值」。
  it('老配置里的非标准档位照样渲染出来，且不会被下一次点击吞掉', () => {
    const onChange = vi.fn();
    const { container } = renderTiers(['1024', '4096'], onChange);
    // 4096 有档名（极清），渲染成一个普通勾选项，而不是消失。
    expect(within(container).getByText('极清')).toBeTruthy();
    // 勾上高清之后，4096 必须还在 —— 只按白名单重建会把它丢掉。
    clickLabel(container, '高清');
    expect(onChange).toHaveBeenCalledWith(['1024', '1536', '4096']);
  });

  it('档名表里没有的值回落成数字本身，界面不会因此空掉', () => {
    const { container } = renderTiers(['1600']);
    expect(within(container).getByText('1600')).toBeTruthy();
  });
});
