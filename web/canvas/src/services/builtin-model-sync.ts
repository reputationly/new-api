// BUILTIN_MODE: 站内渠道的模型列表自动同步。
//
// 上游是 BYO-key 应用:用户自己填渠道,再手动点「获取模型」把列表拉下来、勾选想用的。
// 内置模式下渠道就是平台本身 —— 可用模型由用户所属分组决定,管理员改渠道/改分组即变。
// 让用户钻进「配置 → 渠道 → 编辑 → 获取模型 → 勾选 → 确认」才能看到模型是错的:
// 不点就是所有下拉全空,点了也会和服务端的真实可用集合脱节。
//
// 所以这里在进入应用时拉一次 /pg/models 并整份覆盖站内渠道的模型列表(而不是并集),
// 服务端摘掉的模型要跟着消失。持久化的那份只当离线缓存,不作为真值。

import { fetchImageModels } from "@/services/api/image";
import { BUILTIN_MODE, builtinChannel, normalizeChannelModels, useConfigStore, withChannels } from "@/stores/use-config-store";

let inflight: Promise<{ ok: boolean; count: number }> | null = null;

/**
 * 同步一次。失败不抛 —— 拿不到列表时保留上次缓存,让用户还能用着,
 * 而不是把画布打成不可用状态。
 *
 * 已有一次在飞就复用它:启动时的自动同步还没回来、用户又点了配置页的刷新,
 * 这时该等那次的真实结果,而不是回一个 ok:false 让 UI 弹「同步失败」。
 */
export function syncBuiltinModels(): Promise<{ ok: boolean; count: number }> {
    if (!BUILTIN_MODE) return Promise.resolve({ ok: false, count: 0 });
    inflight =
        inflight ||
        runSync().finally(() => {
            inflight = null;
        });
    return inflight;
}

async function runSync(): Promise<{ ok: boolean; count: number }> {
    try {
        // fetchImageModels 顺带把 supported_endpoint_types 登记进能力映射,
        // 所以随后的 normalizeChannelModels 能拿到真实能力而不是靠模型名猜。
        const names = await fetchImageModels({ baseUrl: "/pg", apiKey: "", apiFormat: "openai" });
        const models = normalizeChannelModels(names);
        const { config } = useConfigStore.getState();
        const previous = config.channels.find((channel) => channel.models.length)?.models || [];
        // 用户给模型挂过的自定义脚本按模型名保留,别因为一次同步就丢了。
        // 注:内置模式已经没有编辑脚本的入口(渠道编辑抽屉不可达),这里只是不动历史数据。
        const scripts = new Map(previous.filter((model) => model.script).map((model) => [model.name, model.script]));
        const next = models.map((model) => (scripts.has(model.name) ? { ...model, script: scripts.get(model.name) } : model));
        // 走 withChannels 而不是只 set channels:models 列表与各能力的默认模型都得跟着重算,
        // 否则下拉是新的、默认选中还是旧的(甚至指向已被服务端摘掉的模型)。
        const nextConfig = withChannels(config, [builtinChannel(next)]);
        for (const key of Object.keys(nextConfig) as Array<keyof typeof nextConfig>) {
            useConfigStore.getState().updateConfig(key, nextConfig[key]);
        }
        return { ok: true, count: next.length };
    } catch {
        return { ok: false, count: 0 };
    }
}
