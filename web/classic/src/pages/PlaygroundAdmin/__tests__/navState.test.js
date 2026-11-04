import { describe, it, expect } from 'vitest';
import { navToSearch, parseNavFromSearch, resolveNav } from '../navState';

// 「体验区管理」刷新后回「通用设置」是长期抱怨：改一次配置要来回切好几个位置，
// 刷新一次就得从头找。导航位置现在跟着 URL 走，这一组守的就是编码/解码与校验。

describe('导航位置 ⇄ URL', () => {
  it('四种位置编解码一致', () => {
    const cases = [
      [{ type: 'global' }, ''],
      [{ type: 'sidebar' }, '?panel=sidebar'],
      [{ type: 'category', category: 'video' }, '?cat=video'],
      [
        { type: 'tab', category: 'video', tabKey: 'flf2v' },
        '?cat=video&tab=flf2v',
      ],
    ];
    for (const [nav, search] of cases) {
      expect(navToSearch('', nav)).toBe(search);
      expect(parseNavFromSearch(search)).toEqual(nav);
    }
  });

  // 这个页面可能被带参数打开（埋点、来源标记），不能因为写导航把它们抹掉。
  it('保留与导航无关的 query', () => {
    const search = navToSearch('?from=email', {
      type: 'tab',
      category: 'video',
      tabKey: 'r2va',
    });
    const q = new URLSearchParams(search);
    expect(q.get('from')).toBe('email');
    expect(q.get('cat')).toBe('video');
    expect(q.get('tab')).toBe('r2va');

    // 切回「通用设置」时只清自己的键
    expect(navToSearch(search, { type: 'global' })).toBe('?from=email');
  });
});

describe('外部链接可能指向已下线的方法/玩法', () => {
  const categories = [
    {
      key: 'video',
      tabs: [{ key: 'text2video' }, { key: 'flf2v' }],
    },
  ];

  it('合法位置原样返回', () => {
    const nav = { type: 'tab', category: 'video', tabKey: 'flf2v' };
    expect(resolveNav(nav, categories)).toEqual(nav);
  });

  it('不存在的分类 / 方法回「通用设置」，不渲染空白', () => {
    expect(
      resolveNav({ type: 'category', category: 'gone' }, categories),
    ).toEqual({
      type: 'global',
    });
    expect(
      resolveNav({ type: 'tab', category: 'video', tabKey: 'sr' }, categories),
    ).toEqual({ type: 'global' });
  });

  it('空搜索串回「通用设置」', () => {
    expect(parseNavFromSearch('')).toEqual({ type: 'global' });
    expect(parseNavFromSearch('?cat=&tab=')).toEqual({ type: 'global' });
  });
});
