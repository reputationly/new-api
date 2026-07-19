"use client";

// 媒体模型配置 store(设计文档 §3.2)。
// 数据源与体验区 StatusContext 同源:
//   1. GET /api/status(公开)→ ImageModelSizeConfig / VideoModelConfig / AudioModelConfig /
//      MusicModelConfig 四份 JSON(运营设置维护,含能力标签 capabilities 与参数白名单);
//   2. GET /pg/models(登录)→ 当前用户可用模型集合;
// 交集 = 各能力节点的模型下拉列表。会话内缓存,进入编辑器拉一次,可手动刷新。

import axios from "axios";
import { create } from "zustand";

import { builtinHeaders } from "@/lib/builtin-auth";
import type { CapabilityModality, CapabilitySpec } from "@/services/capabilities/registry";

export type MediaModelEntry = {
    capabilities: string[];
    sizes: string[];
    durations: string[];
    maxChars?: number;
    refAudioMaxMB?: number;
};

export type MediaModelConfigs = {
    /**
     * 模态 → (模型名 → 配置)。按模态分桶,与体验区"各页独立解析各自配置"语义一致:
     * 同名模型可同时出现在多份配置(如同一模型既配语音合成又配文生音乐),互不覆盖。
     */
    models: Partial<Record<CapabilityModality, Record<string, MediaModelEntry>>>;
    /** 各模态 default 段(仅前端兜底展示,后端不校验 default) */
    defaults: Partial<Record<CapabilityModality, Partial<MediaModelEntry>>>;
};

type MediaConfigStore = {
    configs: MediaModelConfigs | null;
    availableModels: string[];
    loading: boolean;
    error: string | null;
    loadedAt: number;
    refresh: () => Promise<void>;
    ensureLoaded: () => Promise<void>;
};

const EMPTY_ENTRY: MediaModelEntry = { capabilities: [], sizes: [], durations: [] };

function toStringList(value: unknown): string[] {
    if (!Array.isArray(value)) return [];
    return value.map((item) => String(item).trim()).filter(Boolean);
}

function toOptionalNumber(value: unknown): number | undefined {
    const n = Number(value);
    return Number.isFinite(n) ? n : undefined;
}

function parseJsonConfig(raw: unknown): Record<string, unknown> | null {
    if (!raw) return null;
    try {
        const parsed = typeof raw === "string" ? JSON.parse(raw) : raw;
        return parsed && typeof parsed === "object" ? (parsed as Record<string, unknown>) : null;
    } catch {
        return null;
    }
}

// Image: { default:[sizes], models:{ name: {sizes,capabilities} | [sizes] } }
// Video: { default:{sizes,durations}, models:{ name:{sizes,durations,capabilities} } }
// Audio/Music: { default:{maxChars,refAudioMaxMB}, models:{ name:{capabilities,maxChars,refAudioMaxMB} } }
function mergeConfig(target: MediaModelConfigs, modality: CapabilityModality, raw: unknown) {
    const cfg = parseJsonConfig(raw);
    if (!cfg) return;
    const def = cfg.default;
    if (Array.isArray(def)) {
        target.defaults[modality] = { sizes: toStringList(def) };
    } else if (def && typeof def === "object") {
        const d = def as Record<string, unknown>;
        target.defaults[modality] = {
            sizes: toStringList(d.sizes),
            durations: toStringList(d.durations),
            maxChars: toOptionalNumber(d.maxChars),
            refAudioMaxMB: toOptionalNumber(d.refAudioMaxMB),
        };
    }
    const models = cfg.models;
    if (!models || typeof models !== "object") return;
    const bucket = (target.models[modality] ||= {});
    for (const [name, value] of Object.entries(models as Record<string, unknown>)) {
        const key = name.trim();
        if (!key) continue;
        if (Array.isArray(value)) {
            bucket[key] = { ...EMPTY_ENTRY, sizes: toStringList(value) };
            continue;
        }
        if (!value || typeof value !== "object") continue;
        const v = value as Record<string, unknown>;
        bucket[key] = {
            capabilities: toStringList(v.capabilities),
            sizes: toStringList(v.sizes),
            durations: toStringList(v.durations),
            maxChars: toOptionalNumber(v.maxChars),
            refAudioMaxMB: toOptionalNumber(v.refAudioMaxMB),
        };
    }
}

async function fetchMediaConfigs(): Promise<MediaModelConfigs> {
    const response = await axios.get<{ data?: Record<string, unknown> }>("/api/status");
    const data = response.data?.data || {};
    const configs: MediaModelConfigs = { models: {}, defaults: {} };
    mergeConfig(configs, "image", data.ImageModelSizeConfig);
    mergeConfig(configs, "video", data.VideoModelConfig);
    mergeConfig(configs, "audio", data.AudioModelConfig);
    mergeConfig(configs, "music", data.MusicModelConfig);
    return configs;
}

async function fetchAvailableModels(): Promise<string[]> {
    const response = await axios.get<{ data?: Array<{ id?: string }> }>("/pg/models", { headers: builtinHeaders() });
    return (response.data?.data || []).map((item) => (item.id || "").trim()).filter(Boolean);
}

export const useMediaConfigStore = create<MediaConfigStore>()((set, get) => ({
    configs: null,
    availableModels: [],
    loading: false,
    error: null,
    loadedAt: 0,
    refresh: async () => {
        if (get().loading) return;
        set({ loading: true, error: null });
        try {
            const [configs, availableModels] = await Promise.all([fetchMediaConfigs(), fetchAvailableModels()]);
            set({ configs, availableModels, loading: false, loadedAt: Date.now() });
        } catch (error) {
            set({ loading: false, error: error instanceof Error ? error.message : "获取模型配置失败" });
        }
    },
    ensureLoaded: async () => {
        if (get().loadedAt || get().loading) return;
        await get().refresh();
    },
}));

/** 能力 X 节点的模型下拉:可用模型 ∩ 该能力所属模态配置中声明了该能力标签的模型 */
export function modelsForCapability(store: Pick<MediaConfigStore, "configs" | "availableModels">, spec: CapabilitySpec): string[] {
    const { configs, availableModels } = store;
    if (!configs) return [];
    const bucket = configs.models[spec.modality] || {};
    return availableModels.filter((model) => (bucket[model]?.capabilities || []).includes(spec.label));
}

/** 某模型在某模态下的参数白名单(模型未配置时回退该模态 default 段) */
export function paramOptionsForModel(configs: MediaModelConfigs | null, modality: CapabilityModality, model: string): MediaModelEntry {
    if (!configs) return EMPTY_ENTRY;
    const entry = configs.models[modality]?.[model];
    const fallback = configs.defaults[modality];
    return {
        capabilities: entry?.capabilities || [],
        sizes: entry?.sizes.length ? entry.sizes : toStringList(fallback?.sizes),
        durations: entry?.durations.length ? entry.durations : toStringList(fallback?.durations),
        maxChars: entry?.maxChars ?? fallback?.maxChars,
        refAudioMaxMB: entry?.refAudioMaxMB ?? fallback?.refAudioMaxMB,
    };
}
