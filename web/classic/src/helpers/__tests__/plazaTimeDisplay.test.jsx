import { describe, it, expect } from 'vitest';
import { renderHook } from '@testing-library/react';
import { getPricingTableColumns } from '../../components/table/model-pricing/view/table/PricingTableColumns';

// 把 React 元素树摊平成字符串，便于断言"渲染出来到底写了什么"。
const flatten = (node, out = []) => {
  if (node === null || node === undefined || node === false) return out;
  if (typeof node === 'string' || typeof node === 'number') {
    out.push(String(node));
    return out;
  }
  if (Array.isArray(node)) {
    node.forEach((n) => flatten(n, out));
    return out;
  }
  if (node.props) flatten(node.props.children, out);
  return out;
};

// 忙时目录价 2/8、固定 5 折；闲时目录价 1/7（输入系数 0.5、输出系数 0.875）。
// 广场必须显示：划线 = 该时段目录价，成交 = 划线 × 5 折，角标恒为 5 折。
describe('广场展示口径：划线取目录价、角标取固定折扣', () => {
  const record = {
    model_name: 'deepseek-v4-1-flash',
    quota_type: 0,
    model_ratio: 2,
    completion_ratio: 4,
    enable_groups: ['default'],
  };

  const timeView = {
    discount_ratio: 0.5,
    input_directory_ratio: 0.5,
    output_directory_ratio: 0.875,
    completion_ratio: 7,
    label: '夜间',
    windows: [],
  };

  // 列定义的工厂内部会调 useIsMobile()，必须在 hook 上下文里执行
  const build = (view) =>
    renderHook(() =>
      getPricingTableColumns({
        t: (s) => s,
        selectedGroup: 'default',
        groupRatio: { default: 1 },
        // 闲时成交终值 = 常规价 0.5 × 输入系数 0.5 = 0.25
        groupModelRatio: { default: { 'deepseek-v4-1-flash': 0.25 } },
        groupTimeRatio: view
          ? { default: { 'deepseek-v4-1-flash': view } }
          : {},
        groupModelCompletionRatio: view
          ? { default: { 'deepseek-v4-1-flash': view.completion_ratio } }
          : {},
        copyText: () => {},
        currency: 'CNY',
        siteDisplayType: 'CNY',
        displayPrice: (v) => `¥${Number(v).toFixed(2)}`,
        pointsConfig: {},
        entitlementConfig: {},
      }),
    ).result.current;

  it('闲时：输入划线 ¥1.00 → 成交 ¥0.50；输出划线 ¥7.00 → 成交 ¥3.50', () => {
    const cols = build(timeView);
    const priceCol = cols.find((c) => c.dataIndex === 'model_price');
    const text = flatten(priceCol.render(undefined, record)).join('|');
    // 划线 = 闲时目录价（输入 2×0.5=1、输出 2×4×0.875=7）
    expect(text).toContain('¥1.00');
    expect(text).toContain('¥7.00');
    // 成交 = 目录价 × 5 折
    expect(text).toContain('¥0.50');
    expect(text).toContain('¥3.50');
  });

  it('角标用 discount_ratio，恒为 5 折（不随时段变）', () => {
    const cols = build(timeView);
    const nameCol = cols.find((c) => c.dataIndex === 'model_name');
    const text = flatten(nameCol.render('deepseek-v4-1-flash', record)).join(
      '|',
    );
    expect(text).toContain('5折');
  });

  it('没有分时规则时退回原行为（成交价不含时段系数）', () => {
    const cols = build(null);
    const priceCol = cols.find((c) => c.dataIndex === 'model_price');
    const text = flatten(priceCol.render(undefined, record)).join('|');
    expect(text).toContain('¥0.50');
    expect(text).not.toContain('¥7.00');
  });
});
