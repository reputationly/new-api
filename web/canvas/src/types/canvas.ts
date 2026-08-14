export type Position = {
    x: number;
    y: number;
};

export type ViewportTransform = {
    x: number;
    y: number;
    k: number;
};

export enum CanvasNodeType {
    Image = "image",
    Text = "text",
    Config = "config",
    Video = "video",
    Audio = "audio",
    Group = "group",
}

// Node types are open strings: built-ins use CanvasNodeType and plugins use "<pluginId>:<name>".
export type CanvasNodeTypeId = CanvasNodeType | (string & {});

// stalled:能力任务轮询超时但任务仍在服务端运行(≠error),节点可「继续等待」恢复
export type CanvasNodeStatus = "idle" | "success" | "loading" | "error" | "stalled";
export type CanvasGenerationMode = "text" | "image" | "video" | "audio";
export type CanvasImageGenerationType = "generation" | "edit";

export type CanvasNodeImage = {
    id: string;
    status: CanvasNodeStatus;
    errorDetails?: string;
    content: string;
    storageKey: string;
    naturalWidth: number;
    naturalHeight: number;
    bytes: number;
    mimeType: string;
};

export type CanvasNodeMetadata = {
    content?: string;
    composerContent?: string;
    prompt?: string;
    status?: CanvasNodeStatus;
    errorDetails?: string;
    fontSize?: number;
    generationMode?: CanvasGenerationMode;
    generationType?: CanvasImageGenerationType;
    model?: string;
    reasoningEffort?: "auto" | "low" | "medium" | "high" | "xhigh";
    size?: string;
    quality?: string;
    background?: string;
    count?: number;
    seconds?: string;
    vquality?: string;
    generateAudio?: string;
    watermark?: string;
    audioVoice?: string;
    audioFormat?: string;
    audioSpeed?: string;
    audioInstructions?: string;
    references?: string[];
    naturalWidth?: number;
    naturalHeight?: number;
    freeResize?: boolean;
    images?: CanvasNodeImage[];
    primaryImageId?: string;
    storageKey?: string;
    mimeType?: string;
    bytes?: number;
    durationMs?: number;
    groupId?: string;
    interactive?: boolean; // Plugin node interaction/move state; see CanvasNodeDefinition.interactionToggle.

    // ── BUILTIN_MODE: 能力编排(见 docs/canvas-orchestration-design.md) ──
    // capability = 能力注册表 key(t2i/i2v/flf2v/sr/tts_synth/...);缺省 = 上游原有的
    // 「生成配置节点」行为。能力节点的媒体类型 = 该能力的产物类型。
    capability?: string;
    capabilityParams?: Record<string, string | number>;
    // 该节点请求使用的 new-api 分组;缺省不下发,由 Distribute 回落用户默认分组
    group?: string;
    // 同类多输入槽位指定:InputSlot.key → 上游节点 id 列表;未绑定的上游按连线顺序自动分配。
    // 例如双人对话要区分「说话人1/2」的参考音,关键帧要区分首帧/尾帧。
    slotBindings?: Record<string, string[]>;
    // gpustackplus 异步任务 id;下游节点以 task:<id> 引用(后端 NFS 直读,前端零搬运),
    // 刷新后据此恢复轮询
    taskId?: string;
    // 任务产物落 IndexedDB 时的 storageKey:与 storageKey 一致才允许 task: 引用。
    // 节点媒体被上传/替换后 storageKey 变化即失配,防止下游消费旧任务产物。
    taskMediaKey?: string;
    // 素材语义角色(角色/场景/道具/风格/首帧/音色),从素材库插入时带入。
    // 决定这份素材挂到下游时担任什么职责,Agent 据此判断该填进哪个输入槽位。
    assetRole?: string;
};

export type CanvasNodeData = {
    id: string;
    type: CanvasNodeTypeId;
    title: string;
    position: Position;
    width: number;
    height: number;
    metadata?: CanvasNodeMetadata;
};

export type CanvasConnection = {
    id: string;
    fromNodeId: string;
    toNodeId: string;
};

export type CanvasAssistantReference = {
    id: string;
    type: CanvasNodeTypeId;
    title: string;
    dataUrl?: string;
    storageKey?: string;
    text?: string;
};

export type CanvasAssistantImage = {
    id: string;
    dataUrl: string;
    storageKey?: string;
    prompt: string;
};

export type CanvasAssistantMessage = {
    id: string;
    role: "user" | "assistant" | "system" | "tool" | "error";
    title?: string;
    text: string;
    meta?: string;
    detail?: unknown;
    references?: CanvasAssistantReference[];
};

export type CanvasAssistantSession = {
    id: string;
    title: string;
    messages: CanvasAssistantMessage[];
    createdAt: string;
    updatedAt: string;
};

export type ConnectionHandle = {
    nodeId: string;
    handleType: "source" | "target";
};

export type SelectionBox = {
    startWorldX: number;
    startWorldY: number;
    currentWorldX: number;
    currentWorldY: number;
    additive: boolean;
    initialSelectedNodeIds: string[];
};

export type ContextMenuState =
    | {
          type: "node";
          x: number;
          y: number;
          nodeId: string;
      }
    | {
          type: "connection";
          x: number;
          y: number;
          connectionId: string;
      };
