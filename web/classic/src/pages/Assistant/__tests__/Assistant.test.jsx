import React from 'react';
import { render, screen } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { StatusContext } from '../../../context/Status';
import Assistant from '../index';

const renderPage = (status) =>
  render(
    <StatusContext.Provider value={[{ status }, () => {}]}>
      <Assistant />
    </StatusContext.Provider>,
  );

describe('Assistant 页面', () => {
  it('配置了页面地址时用 iframe 展示该页面', () => {
    renderPage({ assistant_link: 'https://example.com/assistant' });

    expect(screen.getByTitle('蒜狸小助手')).toHaveAttribute(
      'src',
      'https://example.com/assistant',
    );
    expect(
      screen.getByRole('link', { name: '页面无法显示？在新标签页打开' }),
    ).toHaveAttribute('href', 'https://example.com/assistant');
  });

  it('未配置页面地址时展示空状态', () => {
    renderPage({});
    expect(
      screen.getByText('管理员暂未配置蒜狸小助手页面地址'),
    ).toBeInTheDocument();
  });
});
