/// <reference types="vite/client" />

declare const __APP_VERSION__: string;
declare const __APP_RELEASES__: import("@/lib/release").ReleaseInfo[];
// BUILTIN_MODE: new-api 内置模式开关(vite.config.ts define,构建期常量)。
// 所有内置版专有改动都以它收敛,可用 __BUILTIN_MODE__ / BUILTIN_MODE 关键字 grep 定位。
declare const __BUILTIN_MODE__: boolean;

interface ImportMetaEnv {
    // Optional build-time analytics configuration, with one independent variable per provider.
    // GA4 measurement ID (G-XXXX)
    readonly VITE_ANALYTICS_GA4_ID?: string;
    // Baidu Analytics site ID
    readonly VITE_ANALYTICS_BAIDU_ID?: string;
}
