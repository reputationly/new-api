"use client";

// 从视频节点抽取单帧,产出可直接建图片节点的 dataUrl。
//
// 主要用途是「视频续接」:取上一段的尾帧作为下一段 flf2v 的首帧,这是连贯性最好的接法。
//
// 两个坑:
// 1. canvas 污染。视频源可能是服务端签名 URL(跨域),直接喂给 <video> 再 drawImage 会
//    把 canvas 标记为 tainted,toDataURL 抛 SecurityError。所以一律先拿到 Blob 再造
//    同源 blob: URL——本地 IndexedDB 有就直接用,没有(换设备)才 fetch 签名 URL 转 Blob。
// 2. 尾帧黑帧。seek 到 duration 精确位置,多数浏览器给不出画面(或 seeked 不触发)。
//    统一回退一个极小量,并对 duration 非有限的情况(部分 webm)做探测。

import { getMediaBlob } from "@/services/file-storage";

// 尾帧回退量(秒)。seek 到精确 duration 往往拿不到画面,但只退一帧也不够:
// 生成式视频的最后几帧常有降质/拖影,编码末尾也可能落在不完整的 GOP 上,
// 拿去当下一段的首帧会把瑕疵带进整条续接链。退 0.25s(24fps 约 6 帧)落在稳定区,
// 与上一段的观感差异可以忽略。对齐 nautilus-studio 的 ffmpeg -sseof -0.25 取值。
const TAIL_EPSILON = 0.25;
const LOAD_TIMEOUT_MS = 20000;

export type VideoFrameResult = {
    dataUrl: string;
    width: number;
    height: number;
    /** 实际截到的时间点(秒) */
    time: number;
    /** 视频总时长(秒);探测不到时为 0 */
    duration: number;
};

export type VideoFrameSource = {
    /** 节点 metadata.content:blob: URL 或服务端签名 URL */
    content?: string;
    /** 节点 metadata.storageKey:优先用它取本地 Blob */
    storageKey?: string;
};

/**
 * 取视频某一帧。
 * @param at "last" 取尾帧、"first" 取首帧,或秒数(会被夹到 [0, duration-ε])
 */
export async function extractVideoFrame(source: VideoFrameSource, at: "last" | "first" | number = "last"): Promise<VideoFrameResult> {
    const { url, revoke } = await resolveSameOriginUrl(source);
    const video = document.createElement("video");
    video.preload = "auto";
    video.muted = true;
    video.playsInline = true;
    // 源已是同源 blob:,crossOrigin 无副作用;万一走到直连 URL 时能争取一次 CORS 放行
    video.crossOrigin = "anonymous";

    try {
        video.src = url;
        const duration = await loadDuration(video);
        const target = at === "first" ? 0 : at === "last" ? Math.max(0, duration - TAIL_EPSILON) : Math.min(Math.max(0, at), Math.max(0, duration - TAIL_EPSILON));
        await seekTo(video, target);

        const width = video.videoWidth;
        const height = video.videoHeight;
        if (!width || !height) throw new Error("读取不到视频画面尺寸，可能是编码不受浏览器支持");

        const canvas = document.createElement("canvas");
        canvas.width = width;
        canvas.height = height;
        const context = canvas.getContext("2d");
        if (!context) throw new Error("无法创建画布上下文");
        context.drawImage(video, 0, 0, width, height);

        let dataUrl: string;
        try {
            dataUrl = canvas.toDataURL("image/png");
        } catch {
            // 兜底路径也没能拿到同源资源时会走到这
            throw new Error("视频跨域受限，无法截帧。请先把该视频下载后重新上传到画布再试。");
        }
        return { dataUrl, width, height, time: target, duration };
    } finally {
        video.removeAttribute("src");
        video.load();
        revoke?.();
    }
}

/** 拿到一个同源可用的 URL,避免 canvas 被跨域源污染 */
async function resolveSameOriginUrl(source: VideoFrameSource): Promise<{ url: string; revoke?: () => void }> {
    if (source.storageKey) {
        const blob = await getMediaBlob(source.storageKey).catch(() => null);
        if (blob) {
            const url = URL.createObjectURL(blob);
            return { url, revoke: () => URL.revokeObjectURL(url) };
        }
    }
    const content = (source.content || "").trim();
    if (!content) throw new Error("该视频节点没有可用的视频内容");
    // 已经是同源的 blob:/data: 直接用,不必再绕一圈
    if (content.startsWith("blob:") || content.startsWith("data:")) return { url: content };

    // 换设备等本地缓存缺失的情况:把远端拉成 Blob 再造同源 URL。
    // 拉不动(对象存储没开 CORS)时如实报错,不要留一个会抛 SecurityError 的源。
    const response = await fetch(content).catch(() => null);
    if (!response?.ok) throw new Error("取不到视频文件，无法截帧。请确认视频仍可访问，或重新上传到画布。");
    const blob = await response.blob();
    const url = URL.createObjectURL(blob);
    return { url, revoke: () => URL.revokeObjectURL(url) };
}

function loadDuration(video: HTMLVideoElement): Promise<number> {
    return new Promise((resolve, reject) => {
        const timer = window.setTimeout(() => finish(new Error("视频加载超时，无法截帧")), LOAD_TIMEOUT_MS);
        const finish = (error?: Error, duration?: number) => {
            window.clearTimeout(timer);
            video.removeEventListener("loadedmetadata", onMeta);
            video.removeEventListener("durationchange", onMeta);
            video.removeEventListener("error", onError);
            if (error) reject(error);
            else resolve(duration ?? 0);
        };
        const onMeta = () => {
            // 部分 webm/流式封装首次给 Infinity:seek 到极大值可逼出真实 duration
            if (!Number.isFinite(video.duration)) {
                if (video.currentTime < 1e5) {
                    video.currentTime = 1e6;
                    return;
                }
                finish(undefined, Number.isFinite(video.duration) ? video.duration : 0);
                return;
            }
            finish(undefined, video.duration);
        };
        const onError = () => finish(new Error("视频解码失败，浏览器不支持该编码"));
        video.addEventListener("loadedmetadata", onMeta);
        video.addEventListener("durationchange", onMeta);
        video.addEventListener("error", onError);
        video.load();
    });
}

function seekTo(video: HTMLVideoElement, time: number): Promise<void> {
    return new Promise((resolve, reject) => {
        const timer = window.setTimeout(() => finish(new Error("视频定位超时，无法截帧")), LOAD_TIMEOUT_MS);
        const finish = (error?: Error) => {
            window.clearTimeout(timer);
            video.removeEventListener("seeked", onSeeked);
            video.removeEventListener("error", onError);
            if (error) reject(error);
            else resolve();
        };
        // HAVE_CURRENT_DATA 之前 drawImage 会画出空帧,等到有帧数据再返回
        const onSeeked = () => {
            if (video.readyState >= 2) finish();
            else video.addEventListener("loadeddata", () => finish(), { once: true });
        };
        const onError = () => finish(new Error("视频定位失败"));
        video.addEventListener("seeked", onSeeked);
        video.addEventListener("error", onError);
        // 已经停在目标位置时 seeked 不会触发,直接判定完成
        if (Math.abs(video.currentTime - time) < 1e-3 && video.readyState >= 2) finish();
        else video.currentTime = time;
    });
}
