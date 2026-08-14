// 用户自定义工作流的存储。**只存在本机 IndexedDB**——工作流是纯 JSON,但服务端
// 目前没有对应的存放位置(画布项目表存的是项目,素材表存的是媒体)。跨设备同步需要
// 后端加一张表,那是独立的一步;在此之前 UI 上要如实告诉用户这一点。

import { create } from "zustand";
import { persist, type PersistStorage, type StorageValue } from "zustand/middleware";

import { localForageStorage } from "@/lib/localforage-storage";
import type { CanvasWorkflow } from "@/lib/canvas/canvas-workflow";

type WorkflowStore = {
    hydrated: boolean;
    workflows: CanvasWorkflow[];
    saveWorkflow: (workflow: CanvasWorkflow) => void;
    renameWorkflow: (id: string, title: string, description?: string) => void;
    removeWorkflow: (id: string) => void;
    replaceWorkflows: (workflows: CanvasWorkflow[]) => void;
};

const WORKFLOW_STORE_KEY = "infinite-canvas:workflow_store";

const workflowStorage: PersistStorage<WorkflowStore> = {
    getItem: async (name) => {
        const value = await localForageStorage.getItem(name);
        return value ? (JSON.parse(value) as StorageValue<WorkflowStore>) : null;
    },
    setItem: (name, value) => localForageStorage.setItem(name, JSON.stringify(value)),
    removeItem: (name) => localForageStorage.removeItem(name),
};

export const useWorkflowStore = create<WorkflowStore>()(
    persist(
        (set) => ({
            hydrated: false,
            workflows: [],
            // 同名覆盖:用户改完再存一次是常见操作,不该攒出一堆同名副本
            saveWorkflow: (workflow) =>
                set((state) => {
                    const existing = state.workflows.findIndex((item) => item.id === workflow.id || item.title === workflow.title);
                    if (existing < 0) return { workflows: [workflow, ...state.workflows] };
                    const next = [...state.workflows];
                    next[existing] = { ...workflow, id: next[existing].id, createdAt: next[existing].createdAt, updatedAt: new Date().toISOString() };
                    return { workflows: next };
                }),
            renameWorkflow: (id, title, description) =>
                set((state) => ({
                    workflows: state.workflows.map((item) => (item.id === id ? { ...item, title: title.trim() || item.title, description: description ?? item.description, updatedAt: new Date().toISOString() } : item)),
                })),
            removeWorkflow: (id) => set((state) => ({ workflows: state.workflows.filter((item) => item.id !== id) })),
            replaceWorkflows: (workflows) => set({ workflows }),
        }),
        {
            name: WORKFLOW_STORE_KEY,
            storage: workflowStorage,
            partialize: (state) => ({ workflows: state.workflows }) as StorageValue<WorkflowStore>["state"],
            onRehydrateStorage: () => () => useWorkflowStore.setState({ hydrated: true }),
        },
    ),
);
