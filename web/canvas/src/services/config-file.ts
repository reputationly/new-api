import { saveAs } from "file-saver";

import i18n from "@/i18n";
import { BUILTIN_MODE, normalizeChannels, useConfigStore, type AiConfig, type WebdavSyncConfig } from "@/stores/use-config-store";
import { usePromptSourceStore, type PromptSourceSchedule } from "@/stores/use-prompt-source-store";
import type { PromptSource } from "@/services/api/prompt-source-presets";

type AppConfigFile = {
    app: "infinite-canvas";
    version: 1;
    exportedAt: string;
    config: AiConfig;
    webdav: WebdavSyncConfig;
    promptSources: {
        sources: PromptSource[];
        schedule: PromptSourceSchedule;
    };
};

export function exportAppConfig() {
    const { config, webdav } = useConfigStore.getState();
    const { sources, schedule } = usePromptSourceStore.getState();
    const data: AppConfigFile = { app: "infinite-canvas", version: 1, exportedAt: new Date().toISOString(), config, webdav, promptSources: { sources, schedule } };
    saveAs(new Blob([JSON.stringify(data, null, 2)], { type: "application/json;charset=utf-8" }), "infinite-canvas-config.json");
}

export async function importAppConfig(file: File) {
    let data: AppConfigFile;
    try {
        data = JSON.parse(await file.text()) as AppConfigFile;
    } catch {
        throw new Error(i18n.t("config.invalidFile"));
    }
    if (data.app !== "infinite-canvas" || data.version !== 1 || !data.config || !data.webdav || !data.promptSources) throw new Error(i18n.t("config.invalidFile"));
    // BUILTIN_MODE: 导入的 JSON 完全不可信。直接 setState 会绕过 normalizeChannels
    // ——那正是内置版丢弃外部渠道的地方——构造一份带 baseUrl + apiKey 的配置就能把
    // 画布指向站外,绕过 /pg 即绕过计费与限流。这里强制过一遍同一套规范化。
    const config = BUILTIN_MODE ? { ...data.config, channelMode: "local" as const, channels: normalizeChannels(data.config) } : data.config;
    useConfigStore.setState({ config, webdav: BUILTIN_MODE ? useConfigStore.getState().webdav : data.webdav });
    usePromptSourceStore.setState(data.promptSources);
}
