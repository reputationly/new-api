// 「体验区管理」页的导航位置 ⇄ URL query。
//
// 为什么要有它：这个页面的导航是 useState 里的局部状态，**刷新一次必然回到「通用设置」**——
// 而改一次配置往往要来回切好几个位置（超分档位在生成模型卡上、尺寸在分类页、模板在 tab 页），
// 每次刷新都从头找一遍。
//
// 约定（只用三个键，其余 query 原样保留 —— 这个页面可能被带参数打开）：
//   （无）                → 通用设置
//   ?panel=sidebar       → 分类显示
//   ?cat=video           → 某个分类页（按模型交叉检查）
//   ?cat=video&tab=flf2v → 某个玩法页
//
// 只做读写，不碰 history 语义：调用方用 replaceState，避免把每次点击都塞进后退栈。
const PANEL_KEY = 'panel';
const CATEGORY_KEY = 'cat';
const TAB_KEY = 'tab';

export const parseNavFromSearch = (search) => {
  const q = new URLSearchParams(search || '');
  const panel = (q.get(PANEL_KEY) || '').trim();
  const category = (q.get(CATEGORY_KEY) || '').trim();
  const tabKey = (q.get(TAB_KEY) || '').trim();
  if (panel === 'sidebar') return { type: 'sidebar' };
  if (category && tabKey) return { type: 'tab', category, tabKey };
  if (category) return { type: 'category', category };
  return { type: 'global' };
};

export const navToSearch = (search, nav) => {
  const q = new URLSearchParams(search || '');
  q.delete(PANEL_KEY);
  q.delete(CATEGORY_KEY);
  q.delete(TAB_KEY);
  if (nav?.type === 'sidebar') {
    q.set(PANEL_KEY, 'sidebar');
  } else if (nav?.type === 'category') {
    q.set(CATEGORY_KEY, nav.category);
  } else if (nav?.type === 'tab') {
    q.set(CATEGORY_KEY, nav.category);
    q.set(TAB_KEY, nav.tabKey);
  }
  const s = q.toString();
  return s ? `?${s}` : '';
};

// 校验：URL 是外部输入（书签、聊天里粘的链接），指向已下线的方法/玩法时不能把页面
// 渲染成空白 —— 退回「通用设置」，不报错。
export const resolveNav = (nav, categories) => {
  if (!nav || nav.type === 'global' || nav.type === 'sidebar') {
    return nav || { type: 'global' };
  }
  const cat = (categories || []).find((c) => c.key === nav.category);
  if (!cat) return { type: 'global' };
  if (nav.type === 'category') return nav;
  if (!cat.tabs.some((tb) => tb.key === nav.tabKey)) return { type: 'global' };
  return nav;
};
