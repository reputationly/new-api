import { renderHook } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { useNavigation } from '../useNavigation';

const t = (key) => key;

const baseModules = {
  home: true,
  console: true,
  pricing: { enabled: true, requireAuth: false },
  docs: true,
  about: true,
};

const assistantLink = 'https://example.com/download';

const findAssistant = (links) =>
  links.find((link) => link.itemKey === 'assistant');

describe('useNavigation', () => {
  it('配置了地址且模块开启时显示蒜狸小助手入口', () => {
    const { result } = renderHook(() =>
      useNavigation(t, '', { ...baseModules, assistant: true }, assistantLink),
    );

    expect(findAssistant(result.current.mainNavLinks)).toEqual({
      text: '蒜狸小助手',
      itemKey: 'assistant',
      to: '/assistant',
    });
  });

  it('未配置地址时不显示蒜狸小助手入口', () => {
    const { result } = renderHook(() =>
      useNavigation(t, '', { ...baseModules, assistant: true }, ''),
    );

    expect(findAssistant(result.current.mainNavLinks)).toBeUndefined();
  });

  it('显式关闭时不显示蒜狸小助手入口', () => {
    const { result } = renderHook(() =>
      useNavigation(t, '', { ...baseModules, assistant: false }, assistantLink),
    );

    expect(findAssistant(result.current.mainNavLinks)).toBeUndefined();
  });

  it('旧配置缺少 assistant 键时默认显示蒜狸小助手入口', () => {
    const { result } = renderHook(() =>
      useNavigation(t, '', baseModules, assistantLink),
    );

    expect(findAssistant(result.current.mainNavLinks)).toBeDefined();
  });
});
