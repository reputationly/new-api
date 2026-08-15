import type { ReactNode } from "react";
import { useEffect, useRef } from "react";
import { App } from "antd";
import { useTranslation } from "react-i18next";

import { BUILTIN_MODE, createModelChannel, useConfigStore } from "@/stores/use-config-store";
import { usePromptSourceScheduler } from "@/hooks/use-prompt-source-scheduler";
import { initCanvasServerSync } from "@/services/canvas-server-sync";
import { syncBuiltinModels } from "@/services/builtin-model-sync";

export function ClientRootInit({ children }: { children: ReactNode }) {
    const { message } = App.useApp();
    const { t } = useTranslation();
    const handledConfigParams = useRef(false);
    const updateConfig = useConfigStore((state) => state.updateConfig);
    const config = useConfigStore((state) => state.config);
    const openConfigDialog = useConfigStore((state) => state.openConfigDialog);

    usePromptSourceScheduler();

    // BUILTIN_MODE: 画布项目「服务端为准、本地为缓存」同步。非内置模式下 initCanvasServerSync
    // 自身早退,上游行为不变。
    useEffect(() => {
        initCanvasServerSync();
    }, []);

    // BUILTIN_MODE: 站内渠道的模型列表来自服务端,进应用时拉一次。
    // 上游要用户手动「获取模型」是 BYO-key 的设计,内置模式下渠道就是平台本身,
    // 可用模型随分组权限变,不该让用户自己去点。
    useEffect(() => {
        if (!BUILTIN_MODE) return;
        void syncBuiltinModels();
    }, []);

    useEffect(() => {
        // BUILTIN_MODE: 上游支持用 ?baseUrl=&apiKey= 从 URL 导入渠道配置(README 的
        // 「New API 自动配置」)。内置模式必须禁掉 —— 那是一条绕过站内渠道锁定、
        // 往配置里注入任意 baseUrl/apiKey 的路径。
        if (BUILTIN_MODE) return;
        if (handledConfigParams.current) return;
        const searchParams = new URLSearchParams(window.location.search);
        const baseUrl = searchParams.get("baseUrl") || searchParams.get("baseurl");
        const apiKey = searchParams.get("apiKey") || searchParams.get("apikey");
        if (!baseUrl && !apiKey) return;
        handledConfigParams.current = true;
        searchParams.delete("baseUrl");
        searchParams.delete("baseurl");
        searchParams.delete("apiKey");
        searchParams.delete("apikey");
        window.history.replaceState(null, "", `${window.location.pathname}${searchParams.size ? `?${searchParams}` : ""}${window.location.hash}`);
        const firstChannel = config.channels[0];
        updateConfig(
            "channels",
            firstChannel
                ? config.channels.map((channel, index) =>
                      index === 0
                          ? {
                                ...channel,
                                ...(baseUrl ? { baseUrl } : {}),
                                ...(apiKey ? { apiKey } : {}),
                            }
                          : channel,
                  )
                : [createModelChannel({ id: "default", name: t("config.channels.defaultName"), baseUrl: baseUrl || undefined, apiKey: apiKey || "" })],
        );
        if (baseUrl) updateConfig("baseUrl", baseUrl);
        if (apiKey) updateConfig("apiKey", apiKey);
        openConfigDialog(false);
        message.success(t("config.importedDirectConfig"));
    }, [config.channels, message, openConfigDialog, t, updateConfig]);

    return <>{children}</>;
}
