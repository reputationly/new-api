import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import ModelTimePriceSection from '../ModelTimePriceSection';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (k, opts) => (opts ? k + JSON.stringify(opts) : k),
  }),
}));

// 内嵌分时价区块的**渲染**测试：这类断言之前完全缺失，
// 结果「未选模型时崩掉整个编辑器」「补集铺成 4 段噪音」都没被抓到。
describe('ModelTimePriceSection', () => {
  const windows = {
    peak: {
      label: '高峰时段',
      segments: [
        { start: '09:00', end: '12:00', days: [1, 2, 3, 4, 5] },
        { start: '14:00', end: '18:00', days: [1, 2, 3, 4, 5] },
      ],
    },
    offpeak: { label: '空闲时段', exclude_of: 'peak' },
  };

  it('没有档位时给出空态与绑定入口（不崩）', () => {
    render(
      <ModelTimePriceSection
        modelName='m1'
        windows={windows}
        rules={[]}
        base={{ input: 2, output: 8, cache: 0.04 }}
        onChange={() => {}}
      />,
    );
    expect(screen.getByText('这个模型没有分时价')).toBeTruthy();
  });

  it('已绑定时段时，收起态显示时段名与三档摘要', () => {
    render(
      <ModelTimePriceSection
        modelName='m1'
        windows={windows}
        rules={[
          { window: 'peak', prices: { input: 2, output: 8, cache: 0.04 } },
        ]}
        base={{ input: 2, output: 8, cache: 0.04 }}
        onChange={() => {}}
      />,
    );
    expect(screen.getByText('高峰时段')).toBeTruthy();
    expect(screen.getByText('展开')).toBeTruthy();
  });

  it('补集时段显示「其余时间（除「高峰时段」以外）」而不是铺开的多段', () => {
    render(
      <ModelTimePriceSection
        modelName='m1'
        windows={windows}
        rules={[{ window: 'offpeak', prices: { input: 1 } }]}
        base={{ input: 2 }}
        onChange={() => {}}
      />,
    );
    expect(screen.getByText('空闲时段')).toBeTruthy();
    expect(screen.getByText(/其余时间/)).toBeTruthy();
  });

  it('按次计费模型只列出「按次价」一个分量', () => {
    const { container } = render(
      <ModelTimePriceSection
        modelName='mj'
        windows={windows}
        rules={[{ window: 'peak', prices: { per_call: 0.2 } }]}
        base={{ per_call: 0.1 }}
        onChange={() => {}}
      />,
    );
    expect(container.textContent).toContain('按次价');
    expect(container.textContent).not.toContain('缓存命中');
  });
});
