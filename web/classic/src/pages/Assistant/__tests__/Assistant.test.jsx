import React from 'react';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { StatusContext } from '../../../context/Status';
import Assistant from '../index';

const renderPage = (status) =>
  render(
    <StatusContext.Provider value={[{ status }, () => {}]}>
      <Assistant />
    </StatusContext.Provider>,
  );

describe('Assistant 页面', () => {
  beforeEach(() => {
    vi.useFakeTimers();
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it('配置了页面地址时用 iframe 展示该页面，且默认不显示兜底提示', () => {
    renderPage({ assistant_link: 'https://example.com/assistant' });

    expect(screen.getByTitle('蒜狸小助手')).toHaveAttribute(
      'src',
      'https://example.com/assistant',
    );
    // 兜底提示常驻会让页面看起来像坏了，所以只在加载超时后才出现。
    expect(
      screen.queryByRole('link', { name: '页面无法显示？在新标签页打开' }),
    ).toBeNull();
    act(() => {
      vi.advanceTimersByTime(6000);
    });
    expect(
      screen.getByRole('link', { name: '页面无法显示？在新标签页打开' }),
    ).toHaveAttribute('href', 'https://example.com/assistant');
  });

  it('iframe 加载完成后不再显示兜底提示', () => {
    renderPage({ assistant_link: 'https://example.com/assistant' });
    fireEvent.load(screen.getByTitle('蒜狸小助手'));
    act(() => {
      vi.advanceTimersByTime(6000);
    });
    expect(
      screen.queryByRole('link', { name: '页面无法显示？在新标签页打开' }),
    ).toBeNull();
  });

  it('未配置页面地址时展示空状态', () => {
    renderPage({});
    expect(
      screen.getByText('管理员暂未配置蒜狸小助手页面地址'),
    ).toBeInTheDocument();
  });
});
