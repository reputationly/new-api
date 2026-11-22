import React from 'react';
import { describe, it, expect } from 'vitest';
import { render } from '@testing-library/react';
import TimeRulesTooltip from '../TimeRulesTooltip';

// 「动态目录价 / 动态折扣」悬浮层的三种形态：
//   全 catalog → 标题「动态目录价」，行内列绝对价目 + 固定折扣；
//   全 factor  → 标题「分时定价」，行内「该时段最终几折」；
//   混配       → 标题回落「分时定价」，逐行按各自模式渲染。
// catalog 行绝不能把 w.ratio（组折扣×价目比的合成值）标成折——那是本次要修的点。

const t = (s) => s;
const renderText = (node) => render(<div>{node}</div>).container.textContent;

const baseEntry = {
  active: true,
  best_ratio: 0.25,
  normal_ratio: 0.5,
  normal_applies: false,
  windows: [
    {
      label: '高峰时段',
      start: '09:00',
      end: '18:00',
      days: [1, 2, 3, 4, 5],
      price_mode: 'catalog',
      prices: { input: 2, output: 8 },
      ratio: 0.5,
      discount_ratio: 0.5,
      active: false,
    },
    {
      label: '空闲时段',
      complement_of: '高峰时段',
      price_mode: 'catalog',
      prices: { input: 1, output: 4, cache: 0.02 },
      ratio: 0.25,
      discount_ratio: 0.5,
      active: true,
    },
  ],
};

const show = (entry) =>
  renderText(
    <TimeRulesTooltip
      group='default'
      modelName='m'
      groupTimeRatio={{ default: { m: entry } }}
      t={t}
    />,
  );

describe('TimeRulesTooltip 价目模式', () => {
  it('全动态目录价：标题换「动态目录价」，行内不重复折数与价目，底部引导点详情', () => {
    const text = show(baseEntry);
    expect(text).toContain('动态目录价');
    expect(text).not.toContain('分时定价');
    expect(text).toContain('空闲时段 除「高峰时段」以外');
    // 折数在顶行（「已按 5 折 计价」）已经写过，逐行不再重复
    expect(text).not.toContain('5折');
    // 价目明细交给详情页，不在这里铺
    expect(text).not.toContain('输入¥1');
    expect(text).toContain('生效中');
    expect(text).toContain('点击查看详情');
  });

  it('全动态折扣：维持「分时定价」+ 该时段最终折', () => {
    const factorEntry = {
      ...baseEntry,
      windows: baseEntry.windows.map((w) => ({
        ...w,
        price_mode: undefined,
        prices: undefined,
      })),
    };
    const text = show(factorEntry);
    expect(text).toContain('分时定价');
    expect(text).not.toContain('动态目录价');
    // factor 行按 w.ratio 折算：空闲 0.25 → 2.5折（顶行没有这个信息，必须保留）
    expect(text).toContain('2.5折');
    expect(text).toContain('点击查看详情');
  });

  it('混配：标题回落「分时定价」，catalog 行仍列价目', () => {
    const mixed = {
      ...baseEntry,
      windows: [
        baseEntry.windows[0],
        { ...baseEntry.windows[1], price_mode: undefined, prices: undefined },
      ],
    };
    const text = show(mixed);
    expect(text).toContain('分时定价');
    // catalog 行不铺价目；factor 行保留该档最终折
    expect(text).not.toContain('输入¥2');
    expect(text).toContain('2.5折');
  });
});
