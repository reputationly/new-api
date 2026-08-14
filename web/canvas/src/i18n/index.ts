import i18n from "i18next";
import { initReactI18next } from "react-i18next";

import enUS from "@/i18n/locales/en-US";
import zhCN from "@/i18n/locales/zh-CN";

export type AppLocale = "zh-CN" | "en-US";

const LOCALE_STORAGE_KEY = "infinite-canvas:locale";

// BUILTIN_MODE: 内置模式固定简体中文,不读也不写 localStorage 里的语言偏好
// (老用户可能存着 en-US),supportedLngs 一并收窄。en-US 词表保留不删:
// fallbackLng 仍指向 zh-CN,删掉反而要动一堆引用。
const BUILTIN = __BUILTIN_MODE__;

i18n.use(initReactI18next).init({
    resources: {
        "zh-CN": { translation: zhCN },
        "en-US": { translation: enUS },
    },
    lng: BUILTIN ? "zh-CN" : (localStorage.getItem(LOCALE_STORAGE_KEY) as AppLocale) || "zh-CN",
    fallbackLng: "zh-CN",
    supportedLngs: BUILTIN ? ["zh-CN"] : ["zh-CN", "en-US"],
    initAsync: false,
    interpolation: { escapeValue: false },
    react: { useSuspense: false },
});

export function changeAppLocale(locale: AppLocale) {
    if (BUILTIN) return Promise.resolve(i18n.t.bind(i18n));
    localStorage.setItem(LOCALE_STORAGE_KEY, locale);
    return i18n.changeLanguage(locale);
}

export default i18n;
