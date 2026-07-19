// BUILTIN_MODE: new-api 内置模式登录态辅助。
// 画布与 new-api SPA 同源部署,session cookie 自动携带;
// new-api 的 UserAuth 中间件额外要求 `New-Api-User: <uid>` 头,uid 由 SPA 登录后写入 localStorage['uid']。

import axios from "axios";

// 直接读环境变量,避免与 use-config-store 形成循环依赖
const BUILTIN = process.env.NEXT_PUBLIC_BUILTIN_MODE === "1";

// 内置模式下全局兜底:站内 API 的 401 统一跳登录重建会话
if (BUILTIN && typeof window !== "undefined") {
    axios.interceptors.response.use(undefined, (error) => {
        const status = (error as { response?: { status?: number } })?.response?.status;
        const url = String((error as { config?: { url?: string } })?.config?.url || "");
        if (status === 401 && (url.startsWith("/pg") || url.startsWith("/api/"))) {
            window.location.href = "/login?expired=true";
        }
        return Promise.reject(error);
    });
}

export function builtinHeaders(): Record<string, string> {
    if (typeof localStorage === "undefined") return {};
    // default 前端登录后写 localStorage['uid'];classic 前端只写 localStorage['user'](JSON,含 id)。
    // 两者都兜底,避免在 classic 部署下缺少 New-Api-User 头导致站内 API 401。
    let uid = localStorage.getItem("uid");
    if (!uid) {
        try {
            const raw = localStorage.getItem("user");
            const id = raw ? (JSON.parse(raw) as { id?: number | string })?.id : undefined;
            if (id !== undefined && id !== null) uid = String(id);
        } catch {
            // localStorage['user'] 非法 JSON,忽略,按未登录处理
        }
    }
    return uid ? { "New-Api-User": uid } : {};
}

export function handleBuiltinAuthError(status: number | undefined, isBuiltinChannel: boolean) {
    if (isBuiltinChannel && status === 401 && typeof window !== "undefined") {
        window.location.href = "/login?expired=true";
    }
}
