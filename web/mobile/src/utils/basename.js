// 移动端 SPA 挂在 /m 下（后端由 router/mobile-router.go 的 mobileGroup 提供）。
//
// 单独成一个常量而不是在 router.jsx 里写死字面量：需要它的不止路由本身——
// 登录页要用它把 ?redirect= 里的完整浏览器路径（/m/console）剥成 react-router 的
// 路径（/console），漏剥就会 navigate 成 /m/m/console。两处各写一个 '/m' 迟早分叉。
//
// 不从 router.jsx 导出：router.jsx 静态 import 了 Login，反向 import 会成环。
export const MOBILE_BASENAME = '/m';
