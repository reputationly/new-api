import { create } from "zustand";
import { persist } from "zustand/middleware";

export type ThemeName = "light" | "dark";

type ThemeStore = {
    theme: ThemeName;
    setTheme: (theme: ThemeName) => void;
};

// BUILTIN_MODE: 内置模式固定浅色。
// 上游默认 dark 且可切换;new-api 内置版只保留浅色并隐藏所有切换入口。
// 这里做三件事,缺一不可:
//   1. 默认值改 light;
//   2. setTheme 短路为 no-op —— 万一还有没摘干净的入口,也改不动;
//   3. merge 忽略已持久化的旧值 —— 老用户 localStorage 里可能存着 dark,
//      不覆盖的话 rehydrate 之后又变回深色。
const BUILTIN = __BUILTIN_MODE__;

export const useThemeStore = create<ThemeStore>()(
    persist(
        (set) => ({
            theme: BUILTIN ? "light" : "dark",
            setTheme: (theme) => {
                if (BUILTIN) return;
                set({ theme });
            },
        }),
        {
            name: "infinite-canvas:theme_store",
            merge: (persisted, current) => (BUILTIN ? { ...current, theme: "light" } : { ...current, ...(persisted as Partial<ThemeStore>) }),
        },
    ),
);
