// lazy import 失败时自动重载一次页面。
//
// 为什么需要：index.html 里写死了带内容 hash 的 chunk 文件名，而新部署会把上一版的
// chunk 文件删掉。任何拿着旧 HTML 的客户端去请求旧 chunk 都是 404 → dynamic import
// 抛错 → React 整棵树崩掉，用户看到白屏，只能自己清缓存。
//
// 服务端已把 HTML 改成 no-store（router/mobile-router.go、router/web-router.go），
// 但那只管以后：**此刻缓存里已经躺着旧 HTML 的人救不到**，微信 X5 那种内核尤其顽固。
// 所以还得有客户端自愈——一次重载就能拿到新 HTML 与新 chunk 名。
//
// ── 刹车必须是单调的 ────────────────────────────────────────────────────────
// 计数器只增不减、会话内绝不重置，到上限就让错误照常抛出去。这是唯一能证明「不会
// 无限刷新」的形态，而无限刷新比白屏更糟——用户连报错都看不到，页面还一直在闪。
//
// 曾经写成「布尔标记 + 应用启动时清掉」，那是错的：清标记挂在 Root 的 useEffect 上，
// 而它在 commit 后同步跑，chunk 的 404 要等一个网络往返才回来，于是每次重载都先把
// 标记清了，再失败、再置位、再重载——刹车正好被拆在了它该起作用的那一刻。
//
// 「加载成功时清零」看着更精准，同样挡不住：同一次渲染里 A chunk 成功清零、B chunk
// 404 置位重载，下一轮又是 A 清零 B 失败，照样无限。只有单调量才是真刹车。
//
// 上限取 2 而不是 1：一个标签页跨越两次部署是可能的（自愈过一次，过会儿又发了一版），
// 跨越三次基本不可能。注意这是**上限**不是重试次数，正常情况第一次就好了。
const RELOAD_COUNT_KEY = 'chunk-reload-count';
const MAX_RELOADS = 2;

// 无痕模式下 sessionStorage 可能直接抛。读不到就当 0（放弃自愈的记账，最坏是多重载
// 一次）；写不进就当已达上限（宁可白屏也不能无限刷）。
const readReloadCount = () => {
  try {
    return Number(sessionStorage.getItem(RELOAD_COUNT_KEY)) || 0;
  } catch (e) {
    return 0;
  }
};

const bumpReloadCount = (next) => {
  try {
    sessionStorage.setItem(RELOAD_COUNT_KEY, String(next));
    return true;
  } catch (e) {
    return false;
  }
};

export const lazyWithReload = (factory) => () =>
  factory().catch((err) => {
    const count = readReloadCount();
    if (count >= MAX_RELOADS || !bumpReloadCount(count + 1)) {
      // 重载过还是不行，说明不是「旧 HTML 指向已删除的 chunk」这个问题
      // （真·网络故障、服务端挂了……），把错误交出去，别再刷了。
      throw err;
    }
    window.location.reload();
    // 重载在途，返回一个永不 resolve 的 promise，免得 React 在这一帧里先渲染出报错。
    return new Promise(() => {});
  });
