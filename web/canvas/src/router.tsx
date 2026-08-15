import { createBrowserRouter, Outlet } from "react-router-dom";

import { AnalyticsTracker } from "@/components/layout/analytics-tracker";
import UserLayout from "@/layouts/user-layout";
import AssetsPage from "@/pages/assets";
import CanvasPage from "@/pages/canvas";
import CanvasProjectPage from "@/pages/canvas/project";
import ConfigPage from "@/pages/config";
import HomePage from "@/pages/home";
import ImagePage from "@/pages/image";
import NotFound from "@/pages/not-found";
import PromptsPage from "@/pages/prompts";
import VideoPage from "@/pages/video";
import WorkflowsPage from "@/pages/workflows";

// BUILTIN_MODE: 画布挂在 /canvas-app/ 下。Vite 的 base 只改静态资源 URL,不影响
// 路由匹配——不给 React Router 配 basename 的话,/canvas-app/ 匹配不到任何路由,
// 整个应用会渲染成 404。旧版是 Next.js 的 basePath 顺带处理了这件事。
// import.meta.env.BASE_URL 即构建期的 VITE_BASE,末尾斜杠要去掉(basename 不吃)。
const basename = (import.meta.env.BASE_URL || "/").replace(/\/$/, "");

export const router = createBrowserRouter(
    [
        {
            element: (
                <UserLayout>
                    <AnalyticsTracker />
                    <Outlet />
                </UserLayout>
            ),
            children: [
                { path: "/", element: <HomePage /> },
                { path: "/image", element: <ImagePage /> },
                { path: "/video", element: <VideoPage /> },
                { path: "/assets", element: <AssetsPage /> },
                { path: "/prompts", element: <PromptsPage /> },
                { path: "/canvas", element: <CanvasPage /> },
                { path: "/canvas/:id", element: <CanvasProjectPage /> },
                { path: "/workflows", element: <WorkflowsPage /> },
                { path: "/config", element: <ConfigPage /> },
            ],
        },
        { path: "*", element: <NotFound /> },
    ],
    { basename },
);
