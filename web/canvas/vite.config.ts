import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

import { parseChangelog } from "./src/lib/release";

const webDir = dirname(fileURLToPath(import.meta.url));
// BUILTIN_MODE: vendor 布局下 VERSION/CHANGELOG 与本文件同级(上游是在仓库根),
// 故读 ./ 而非 ../。
const localVersion = readFileSync(resolve(webDir, "./VERSION"), "utf8").trim() || "dev";
const localChangelog = readFileSync(resolve(webDir, "./CHANGELOG.md"), "utf8");

// BUILTIN_MODE: 上游的 localPluginsManifest() 插件已移除 —— 内置模式不提供节点插件系统
// (远程插件在画布同源执行,能读 session cookie 与 localStorage['uid'],等于给第三方
// 代码开账号权限)。相关前端模块一并删除,详见 NOTICE.md。

export default defineConfig({
    // /canvas-app/ 由 Go 单二进制伺服;不传时保持上游默认根路径,便于独立起 dev server
    base: process.env.VITE_BASE || "/",
    plugins: [react()],
    resolve: {
        alias: {
            "@": resolve(webDir, "src"),
        },
    },
    define: {
        __APP_VERSION__: JSON.stringify(localVersion),
        __APP_RELEASES__: JSON.stringify(parseChangelog(localChangelog)),
        // BUILTIN_MODE: 内置模式开关。所有 new-api 专有改动都以此收敛,可 grep 定位
        __BUILTIN_MODE__: JSON.stringify(process.env.VITE_BUILTIN_MODE === "1"),
    },
});
