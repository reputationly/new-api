import { describe, it, expect, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { formatVideoMatrixSummary } from '../utils';

/**
 * 视频矩阵在卡片/表格视图里的摘要行。
 *
 * 这一行此前只显示一个折后区间——同一个模型打没打折、打了多少，用户完全看不出来，
 * 而按量/按次两条路早就有划线原价了。风格不一致本身就是问题：用户会以为视频模型
 * 不参与折扣。
 *
 * 单位也必须跟着 mode 走：per_second 的数是「每秒」单价，标成「/次」会让用户
 * 以为 10 秒的片子和 1 秒一个价。
 */

const t = (s, vars) =>
  vars ? s.replace(/\{\{(\w+)\}\}/g, (_, k) => vars[k]) : s;

const priceData = (mode, table, usedGroupRatio) => ({
  videoPricing: { mode, ...table },
  usedGroupRatio,
});

const perSecond = (ratio) =>
  priceData('per_second', { per_second: { '544p': 0.1, '1080p': 0.5 } }, ratio);

describe('formatVideoMatrixSummary', () => {
  beforeEach(() => {
    localStorage.clear(); // 默认 USD、rate=1，断言里直接用美元数
  });

  it('per_second 的单位是「秒」而不是「次」', () => {
    render(<div>{formatVideoMatrixSummary(perSecond(1), t)}</div>);
    expect(screen.getByText(/\/ 秒/)).toBeTruthy();
  });

  it('per_call 仍然是「次」，token 仍然是 1M tokens', () => {
    const { unmount } = render(
      <div>
        {formatVideoMatrixSummary(
          priceData('per_call', { per_call: { '720p': { 5: 0.2 } } }, 1),
          t,
        )}
      </div>,
    );
    expect(screen.getByText(/\/ 次/)).toBeTruthy();
    unmount();

    render(
      <div>
        {formatVideoMatrixSummary(
          priceData('token', { token: { '720p': { without_video: 8 } } }, 1),
          t,
        )}
      </div>,
    );
    expect(screen.getByText(/1M tokens/)).toBeTruthy();
  });

  it('打折时给出划线的折前区间', () => {
    const { container } = render(
      <div>{formatVideoMatrixSummary(perSecond(0.5), t)}</div>,
    );
    const struck = container.querySelector('.line-through');
    expect(struck).toBeTruthy();
    // 折前 0.1~0.5，折后 0.05~0.25
    expect(struck.textContent).toBe('$0.1 ~ $0.5');
    expect(container.textContent).toContain('$0.05 ~ $0.25');
  });

  // 不打折还划一道，用户会以为自己错过了什么
  it('无折扣时不划线', () => {
    const { container } = render(
      <div>{formatVideoMatrixSummary(perSecond(1), t)}</div>,
    );
    expect(container.querySelector('.line-through')).toBeNull();
  });

  // 倍率 > 1 是涨价，划线会显示成「原价更便宜」
  it('倍率大于 1 时不划线', () => {
    const { container } = render(
      <div>{formatVideoMatrixSummary(perSecond(1.5), t)}</div>,
    );
    expect(container.querySelector('.line-through')).toBeNull();
  });

  // 免费分组倍率为 0：折后全 0，折前仍要给，否则看不出免了多少
  it('免费分组显示折前价与 0', () => {
    const { container } = render(
      <div>{formatVideoMatrixSummary(perSecond(0), t)}</div>,
    );
    expect(container.querySelector('.line-through').textContent).toBe(
      '$0.1 ~ $0.5',
    );
    expect(container.textContent).toContain('$0');
  });

  it('矩阵为空时退回「场景计费」文案，不炸', () => {
    const { container } = render(
      <div>
        {formatVideoMatrixSummary(priceData('per_second', {}, 0.5), t)}
      </div>,
    );
    expect(container.textContent).toContain('场景计费');
  });
});
