import React, { Suspense, lazy } from 'react';
import { createBrowserRouter } from 'react-router-dom';
import { SpinLoading } from 'antd-mobile';

import Root from './Root';
import AuthRoute from './components/AuthRoute';
import TabLayout from './layouts/TabLayout';
import Experience from './pages/Experience';
import Home from './pages/Home';
import Login from './pages/Login';
import Profile from './pages/Profile';
import Register from './pages/Register';

// 首屏必须同步加载的页面：
//   Login / Register —— 注册页是邀请链接 /register?aff=xxx 的落地页，被邀请的人必然
//     首次访问、零缓存，多切一个 chunk 就多一个往返，对最该快的那条路径反成负优化；
//   Home / Experience / Profile —— 底部 tab 三兄弟，切换要即时，且本身很小。
// 其余按需加载：体验区各页会牵出 classic 的整套生成 hooks（视频/音频/音乐/图片），
// admin 页普通用户一辈子点不到 —— 这些以前全被静态 import 塞进首屏包。
const Chat = lazy(() => import('./pages/Chat'));
const Video = lazy(() => import('./pages/Video'));
const Music = lazy(() => import('./pages/Music'));
const Audio = lazy(() => import('./pages/Audio'));
const ImagePage = lazy(() => import('./pages/Image'));
const Models = lazy(() => import('./pages/Models'));
const Tokens = lazy(() => import('./pages/Tokens'));
const Logs = lazy(() => import('./pages/Logs'));
const Setting = lazy(() => import('./pages/Setting'));
const Tickets = lazy(() => import('./pages/Tickets'));
const TicketDetail = lazy(() => import('./pages/TicketDetail'));
const AdminTickets = lazy(() => import('./pages/admin/AdminTickets'));
const AdminTicketDetail = lazy(() => import('./pages/admin/AdminTicketDetail'));
const AdminKyc = lazy(() => import('./pages/admin/AdminKyc'));
const AdminEnterprise = lazy(() => import('./pages/admin/AdminEnterprise'));
const AdminTransfers = lazy(() => import('./pages/admin/AdminTransfers'));
const AdminInvoices = lazy(() => import('./pages/admin/AdminInvoices'));

const chunkFallback = (
  <div
    style={{
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
      minHeight: '60vh',
    }}
  >
    <SpinLoading style={{ '--size': '32px' }} />
  </div>
);

// 懒加载页面统一挂 Suspense：没有 fallback 的话点进去是一片空白，弱网下这"一瞬间"
// 可能是好几秒 —— 那就等于把首屏白屏搬到了二级页。
const lazyEl = (Component) => (
  <Suspense fallback={chunkFallback}>
    <Component />
  </Suspense>
);

const guarded = (element) => <AuthRoute>{element}</AuthRoute>;

const router = createBrowserRouter(
  [
    {
      element: <Root />,
      children: [
        { path: '/login', element: <Login /> },
        { path: '/register', element: <Register /> },
        {
          element: guarded(<TabLayout />),
          children: [
            { path: '/', element: <Home /> },
            { path: '/experience', element: <Experience /> },
            { path: '/profile', element: <Profile /> },
          ],
        },
        { path: '/models', element: guarded(lazyEl(Models)) },
        { path: '/tokens', element: guarded(lazyEl(Tokens)) },
        { path: '/logs', element: guarded(lazyEl(Logs)) },
        { path: '/setting', element: guarded(lazyEl(Setting)) },
        { path: '/tickets', element: guarded(lazyEl(Tickets)) },
        { path: '/tickets/:id', element: guarded(lazyEl(TicketDetail)) },
        { path: '/admin/tickets', element: guarded(lazyEl(AdminTickets)) },
        {
          path: '/admin/tickets/:id',
          element: guarded(lazyEl(AdminTicketDetail)),
        },
        { path: '/admin/kyc', element: guarded(lazyEl(AdminKyc)) },
        {
          path: '/admin/enterprise',
          element: guarded(lazyEl(AdminEnterprise)),
        },
        { path: '/admin/transfers', element: guarded(lazyEl(AdminTransfers)) },
        { path: '/admin/invoices', element: guarded(lazyEl(AdminInvoices)) },
        { path: '/chat', element: guarded(lazyEl(Chat)) },
        { path: '/video', element: guarded(lazyEl(Video)) },
        { path: '/music', element: guarded(lazyEl(Music)) },
        { path: '/audio', element: guarded(lazyEl(Audio)) },
        { path: '/image', element: guarded(lazyEl(ImagePage)) },
      ],
    },
  ],
  { basename: '/m' },
);

export default router;
