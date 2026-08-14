"use client";

import { create } from "zustand";
import { persist, type PersistStorage, type StorageValue } from "zustand/middleware";

import { nanoid } from "nanoid";
import { localForageStorage } from "@/lib/localforage-storage";
import { cleanupUnusedImages, resolveImageUrl, uploadImage } from "@/services/image-storage";
import { cleanupUnusedMedia, resolveMediaUrl } from "@/services/file-storage";
import { deleteServerAsset, isServerAssetKey, serverAssetId } from "@/services/api/canvas-assets";

export type AssetKind = "text" | "image" | "video";

// 素材的语义角色:回答「这份素材在创作里担任什么」,与 kind(媒体类型)正交。
// 挂参考图时它决定每张图的职责——3 张图分别是角色、场景、道具,和 3 张都叫「参考图」
// 是两回事,后者只能靠猜。tags 是自由文本,承担不了这个判定,所以单独立字段。
export type AssetRole = "character" | "location" | "prop" | "style" | "start_frame" | "voice" | "reference";

export const ASSET_ROLES: Array<{ value: AssetRole; label: string; hint: string }> = [
    { value: "character", label: "角色", hint: "人物身份依据，跨镜头保持同一个人靠它" },
    { value: "location", label: "场景", hint: "环境、地点、背景" },
    { value: "prop", label: "道具", hint: "物件、产品、服装" },
    { value: "style", label: "风格", hint: "色调、材质、美术参考，不提供具体内容" },
    { value: "start_frame", label: "首帧", hint: "画面从这一帧开始动（flf2v）" },
    { value: "voice", label: "音色", hint: "语音克隆或情感参考音" },
    { value: "reference", label: "通用参考", hint: "没有更具体职责时的缺省值" },
];

export function assetRoleLabel(role?: AssetRole) {
    return ASSET_ROLES.find((item) => item.value === role)?.label || "";
}
export type TextAsset = AssetBase<"text"> & { data: { content: string } };
export type ImageAsset = AssetBase<"image"> & { data: { dataUrl: string; storageKey?: string; width: number; height: number; bytes: number; mimeType: string } };
export type VideoAsset = AssetBase<"video"> & { data: { url: string; storageKey?: string; width: number; height: number; bytes: number; mimeType: string } };
export type Asset = TextAsset | ImageAsset | VideoAsset;

type AssetBase<T extends AssetKind> = {
    id: string;
    kind: T;
    title: string;
    coverUrl: string;
    tags: string[];
    /** 语义角色;历史素材没有此字段,读取处一律按 "reference" 兜底 */
    role?: AssetRole;
    source?: string;
    note?: string;
    createdAt: string;
    updatedAt: string;
    metadata?: Record<string, unknown>;
};

type AssetStore = {
    hydrated: boolean;
    assets: Asset[];
    addAsset: (asset: Omit<Asset, "id" | "createdAt" | "updatedAt">) => string;
    updateAsset: (id: string, patch: Partial<Omit<Asset, "id" | "createdAt">>) => void;
    removeAsset: (id: string) => void;
    replaceAssets: (assets: Asset[]) => void;
    cleanupImages: (extra?: unknown) => void;
};

const ASSET_STORE_KEY = "infinite-canvas:asset_store";

const assetStorage: PersistStorage<AssetStore> = {
    getItem: async (name) => {
        const value = await localForageStorage.getItem(name);
        if (!value) return null;
        const parsed = JSON.parse(value) as StorageValue<AssetStore>;
        parsed.state.assets = await Promise.all(
            parsed.state.assets.map(async (asset) => {
                if (asset.kind === "video" && asset.data.storageKey) return { ...asset, data: { ...asset.data, url: await resolveMediaUrl(asset.data.storageKey, asset.data.url) } };
                if (asset.kind !== "image") return asset;
                if (asset.data.storageKey)
                    return {
                        ...asset,
                        coverUrl: asset.coverUrl.startsWith("blob:") ? await resolveImageUrl(asset.data.storageKey, asset.coverUrl) : asset.coverUrl,
                        data: { ...asset.data, dataUrl: await resolveImageUrl(asset.data.storageKey, asset.data.dataUrl) },
                    };
                if (!asset.data.dataUrl.startsWith("data:image/")) return asset;
                const image = await uploadImage(asset.data.dataUrl);
                return { ...asset, coverUrl: asset.coverUrl.startsWith("data:image/") ? image.url : asset.coverUrl, data: { ...asset.data, dataUrl: image.url, storageKey: image.storageKey, bytes: image.bytes, mimeType: image.mimeType } };
            }),
        );
        return parsed;
    },
    setItem: (name, value) => localForageStorage.setItem(name, JSON.stringify(value)),
    removeItem: (name) => localForageStorage.removeItem(name),
};

export const useAssetStore = create<AssetStore>()(
    persist(
        (set, get) => ({
            hydrated: false,
            assets: [],
            addAsset: (asset) => {
                const now = new Date().toISOString();
                const id = nanoid();
                set((state) => ({ assets: [{ ...asset, id, createdAt: now, updatedAt: now } as Asset, ...state.assets] }));
                return id;
            },
            updateAsset: (id, patch) =>
                set((state) => ({
                    assets: state.assets.map((asset) => (asset.id === id ? ({ ...asset, ...patch, updatedAt: new Date().toISOString() } as Asset) : asset)),
                })),
            removeAsset: (id) =>
                set((state) => {
                    const removed = state.assets.find((asset) => asset.id === id);
                    const assets = state.assets.filter((asset) => asset.id !== id);
                    // BUILTIN_MODE: 从素材库删除即释放服务端 OBS 对象与容量配额
                    // (项目删除不触发此逻辑,符合"仅素材库删除才释放"的设计)
                    if (removed && removed.kind !== "text" && isServerAssetKey(removed.data.storageKey)) {
                        void deleteServerAsset(serverAssetId(removed.data.storageKey as string)).catch((error) => {
                            console.warn("[canvas-assets] 删除服务端素材失败:", error);
                        });
                    }
                    get().cleanupImages({ assets });
                    return { assets };
                }),
            replaceAssets: (assets) => set({ assets }),
            cleanupImages: (extra) => {
                window.setTimeout(async () => {
                    const { useCanvasStore } = await import("@/app/(user)/canvas/stores/use-canvas-store");
                    await cleanupUnusedImages({ assets: get().assets, projects: useCanvasStore.getState().projects, extra });
                    await cleanupUnusedMedia({ assets: get().assets, projects: useCanvasStore.getState().projects, extra });
                }, 0);
            },
        }),
        {
            name: ASSET_STORE_KEY,
            storage: assetStorage,
            partialize: (state) => ({ assets: state.assets }) as StorageValue<AssetStore>["state"],
            onRehydrateStorage: () => () => {
                useAssetStore.setState({ hydrated: true });
            },
        },
    ),
);
