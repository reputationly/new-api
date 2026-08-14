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
    // 分组:只表达「同属一个角色/场景/章节」,不参与生成也不传递输入(生产依赖用连线)
    Group = "group",
}

// stalled:能力任务轮询超时但任务仍在服务端运行(≠error),节点可「继续等待」恢复(§3.4)
export type CanvasNodeStatus = "idle" | "success" | "loading" | "error" | "stalled";
export type CanvasGenerationMode = "text" | "image" | "video" | "audio";
export type CanvasImageGenerationType = "generation" | "edit";

// 节点级摄像机配置:机身/镜头/焦距/光圈,启用后由 appendCameraPrompt() 拼进生成提示词
// (utils/canvas-camera.ts)。导演台机位面板复用同一套字段,截图节点继承所属机位的配置。
export type CameraControlOptions = {
    enabled: boolean;
    /** CAMERA_PROFILES[].id */
    camera: string;
    /** LENS_PROFILES[].id */
    lens: string;
    /** 焦距(mm),取值见 FOCAL_LENGTHS */
    focalLength: number;
    /** 光圈 f 值,取值见 APERTURES */
    aperture: number;
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
    size?: string;
    quality?: string;
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
    isBatchRoot?: boolean;
    batchRootId?: string;
    batchChildIds?: string[];
    batchUsesReferenceImages?: boolean;
    primaryImageId?: string;
    imageBatchExpanded?: boolean;
    storageKey?: string;
    mimeType?: string;
    bytes?: number;
    durationMs?: number;
    // 能力节点(编排,见 docs/canvas-orchestration-design.md §3.3):
    // capability = 能力注册表 key(t2i/i2v/tts/...);缺省 = 旧节点行为
    capability?: string;
    capabilityParams?: Record<string, string | number>;
    // 该节点请求使用的分组;缺省不下发,由 Distribute 回落用户默认分组(§3.2)
    group?: string;
    // 节点级摄像机配置;enabled 时在生成前拼进提示词,复制节点与失败重试均继承
    camera?: CameraControlOptions;
    // 所属分组节点 id。几何包含判定(节点中心落在分组矩形内)的结果缓存,拖拽后重算;
    // 真值是几何位置,这里只为渲染标注与快速查询(见 utils/canvas-group.ts)
    groupId?: string;
    // 分组节点自身的配色(仅 CanvasNodeType.Group 使用)
    groupColor?: string;
    // 素材语义角色(角色/场景/道具/风格/首帧/音色),从素材库插入时带入。
    // 决定这份素材挂到下游时担任什么职责,Agent 据此判断该把它填进哪个槽位。
    assetRole?: string;
    // 同类多输入槽位指定:InputSlot.key → 上游节点 id 列表(§3.5);未绑定的上游按连线顺序自动分配
    slotBindings?: Record<string, string[]>;
    // 异步任务 id(gpustackplus 产物;下游节点以 task:<id> 引用,刷新恢复轮询用)
    taskId?: string;
    // 任务产物落 IndexedDB 时的 storageKey:与 storageKey 一致才允许 task: 引用,
    // 节点媒体被上传/替换后(storageKey 变化)自动失配,防止下游消费旧任务产物
    taskMediaKey?: string;
};

export type CanvasNodeData = {
    id: string;
    type: CanvasNodeType;
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
    type: CanvasNodeType;
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
