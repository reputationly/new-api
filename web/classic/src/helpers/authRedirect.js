// 「被踢回登录页 → 登录后回到原来那页」用到的两个纯函数。
//
// 单独成文件而不是塞进 helpers/utils.jsx：那个文件引了 Semi / react-toastify 等桌面
// 依赖，手机端整模块替换成了 shims/classic-utils.jsx（见 mobile 的 vite.config.js
// SHIM_MAP）。放这里两端可以直接 import 同一份，不必在 shim 里再抄一遍抄到分叉——
// 与 ./priceFormat、./videoMatrix 的拆分理由相同。

// 会话过期跳登录时，把当前地址放进 query 带过去。
//
// **只能用 query，不能用 react-router 的 location.state**：这里是整页跳转
// (window.location.href)，state 是内存态，跳完就没了。
export const LOGIN_REDIRECT_PARAM = 'redirect';

// 校验回跳目标：只接受站内相对路径（单个 / 开头，且第二个字符不是 / 或 \）。
//
// 必须挡住三种写法，放行任意一种都是开放重定向 —— 攻击者能构造一条「登录后自动跳去
// 钓鱼站」的链接，而用户从头到尾看到的域名都是我们的，正是钓鱼最想要的形态：
//   https://evil.com  绝对地址，不以 / 开头，第一条就挡掉；
//   //evil.com        协议相对 URL，浏览器当绝对地址处理；
//   /\evil.com        浏览器会把 \ 规范化成 /，等价于上一条，是常见的绕过写法。
// URLSearchParams.get() 已做过一次百分号解码，故 %2f%2fevil.com 到这里就是 //evil.com。
export const safeRedirectTarget = (value) =>
  typeof value === 'string' &&
  value.startsWith('/') &&
  value[1] !== '/' &&
  value[1] !== '\\'
    ? value
    : '';

// 构造带回跳与过期标记的登录地址。loginPath 由调用方给：桌面端 /login，
// 手机端 /m/login（手机端本就在 /m 下，直接给终点省掉 mobile-router 的 UA 跳转）。
export const buildLoginUrl = (loginPath) => {
  const from = window.location.pathname + window.location.search;
  const params = new URLSearchParams({ expired: 'true' });
  // 首页不必回跳，登录后本来就落在那儿；带上只会让地址栏更长。
  if (from && from !== '/') {
    params.set(LOGIN_REDIRECT_PARAM, from);
  }
  return `${loginPath}?${params.toString()}`;
};
