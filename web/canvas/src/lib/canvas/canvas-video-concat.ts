"use client";

// 多段视频拼成一条成片。**stream copy(remux),不重编码** —— 画质零损失、秒级完成、
// 后端零改动。代价是各段的编码参数必须一致:分辨率、编码器、时基结构不同的片段
// 没法直接拼帧,遇到就如实报错让用户先统一(通常过一遍超分即可)。
//
// 为什么不做转场:下一段本来就该从上一段的尾帧接上(见续接技能),再叠 xfade 等于
// 二次混合;而且各段音视频时长常有细微差,xfade 会把后段卡在最后一帧而音频继续走。
// 直接硬拼是确定的、视觉也连续的。
//
// mp4box 在浏览器里跑,动态 import 避免进首屏 chunk。

import type { CanvasNodeData } from "@/types/canvas";
import { getMediaBlob } from "@/services/file-storage";

export type ConcatSource = { nodeId: string; title: string; content?: string; storageKey?: string };
export type ConcatResult = { blob: Blob; durationSeconds: number; width: number; height: number; clips: number };

type TrackPlan = {
    id: number;
    kind: "video" | "audio";
    codec: string;
    timescale: number;
    width?: number;
    height?: number;
    channelCount?: number;
    sampleRate?: number;
};

/** 拼接。按传入顺序首尾相接,返回一个可直接建视频节点的 Blob */
export async function concatVideos(sources: ConcatSource[]): Promise<ConcatResult> {
    if (sources.length < 2) throw new Error("至少需要 2 个视频节点才能拼接。");
    const { createFile } = await import("mp4box");

    const parsed = [];
    for (const source of sources) {
        const buffer = await readSourceBuffer(source);
        parsed.push({ source, ...(await parseMp4(createFile, buffer)) });
    }

    // 兼容性闸门:参数不一致时直接拒,不要拼出一个播到一半花屏的文件
    const first = parsed[0];
    const baseVideo = first.video;
    if (!baseVideo) throw new Error(`「${first.source.title}」里没有视频轨，无法拼接。`);
    for (const item of parsed.slice(1)) {
        if (!item.video) throw new Error(`「${item.source.title}」里没有视频轨，无法拼接。`);
        if (item.video.width !== baseVideo.width || item.video.height !== baseVideo.height) {
            throw new Error(`片段分辨率不一致：「${first.source.title}」是 ${baseVideo.width}×${baseVideo.height}，「${item.source.title}」是 ${item.video.width}×${item.video.height}。请先把它们统一过一遍视频超分再拼接。`);
        }
        if (baseCodec(item.video.codec) !== baseCodec(baseVideo.codec)) {
            throw new Error(`片段编码不一致：「${first.source.title}」是 ${baseVideo.codec}，「${item.source.title}」是 ${item.video.codec}。请先统一编码（过一遍超分即可）再拼接。`);
        }
    }
    // 音轨要么都有要么都没有:部分有会导致成片后半段突然静音且时长错位
    const withAudio = parsed.filter((item) => item.audio).length;
    const useAudio = withAudio === parsed.length;
    if (withAudio && !useAudio) throw new Error(`有 ${withAudio}/${parsed.length} 个片段带音轨。请先给缺音轨的片段配音（v2a），或去掉带音轨的那些，再拼接。`);

    const out = createFile();
    const videoTrackId = out.addTrack({
        type: baseVideo.type,
        timescale: baseVideo.timescale,
        width: baseVideo.width,
        height: baseVideo.height,
        description: baseVideo.description,
    } as never);
    const audioTrackId = useAudio
        ? out.addTrack({
              type: first.audio!.type,
              timescale: first.audio!.timescale,
              channel_count: first.audio!.channelCount,
              samplerate: first.audio!.sampleRate,
              description: first.audio!.description,
          } as never)
        : 0;

    let videoOffset = 0;
    let audioOffset = 0;
    for (const item of parsed) {
        // 各段时基可能不同,统一缩放到输出轨的时基上,否则第二段起时间轴就漂了
        const vScale = baseVideo.timescale / item.video!.timescale;
        let vEnd = 0;
        for (const sample of item.videoSamples) {
            out.addSample(videoTrackId, sample.data, {
                duration: Math.round(sample.duration * vScale),
                dts: videoOffset + Math.round(sample.dts * vScale),
                cts: videoOffset + Math.round(sample.cts * vScale),
                is_sync: sample.is_sync,
            });
            vEnd = Math.max(vEnd, Math.round((sample.dts + sample.duration) * vScale));
        }
        videoOffset += vEnd;

        if (useAudio && audioTrackId) {
            const aScale = first.audio!.timescale / item.audio!.timescale;
            let aEnd = 0;
            for (const sample of item.audioSamples) {
                out.addSample(audioTrackId, sample.data, {
                    duration: Math.round(sample.duration * aScale),
                    dts: audioOffset + Math.round(sample.dts * aScale),
                    cts: audioOffset + Math.round(sample.cts * aScale),
                    is_sync: sample.is_sync,
                });
                aEnd = Math.max(aEnd, Math.round((sample.dts + sample.duration) * aScale));
            }
            audioOffset += aEnd;
        }
    }

    const stream = out.getBuffer();
    const buffer = stream instanceof ArrayBuffer ? stream : (stream as { buffer: ArrayBuffer }).buffer;
    return {
        blob: new Blob([buffer], { type: "video/mp4" }),
        durationSeconds: videoOffset / baseVideo.timescale,
        width: baseVideo.width || 0,
        height: baseVideo.height || 0,
        clips: parsed.length,
    };
}

/** 取源文件字节。本地 IndexedDB 优先,缺失时拉远端(与截帧同策略) */
async function readSourceBuffer(source: ConcatSource): Promise<ArrayBuffer> {
    if (source.storageKey) {
        const blob = await getMediaBlob(source.storageKey).catch(() => null);
        if (blob) return blob.arrayBuffer();
    }
    const content = (source.content || "").trim();
    if (!content) throw new Error(`「${source.title}」没有可用的视频内容。`);
    const response = await fetch(content).catch(() => null);
    if (!response?.ok) throw new Error(`取不到「${source.title}」的视频文件，无法拼接。`);
    return response.arrayBuffer();
}

type ParsedTrack = TrackPlan & { type: string; description: unknown };
type ParsedFile = { video: ParsedTrack | null; audio: ParsedTrack | null; videoSamples: Mp4Sample[]; audioSamples: Mp4Sample[] };
type Mp4Sample = { data: Uint8Array<ArrayBuffer>; duration: number; dts: number; cts: number; is_sync: boolean };

function parseMp4(createFile: () => any, buffer: ArrayBuffer): Promise<ParsedFile> {
    return new Promise((resolve, reject) => {
        const file = createFile();
        const collected = new Map<number, Mp4Sample[]>();
        let plan: { video: ParsedTrack | null; audio: ParsedTrack | null } = { video: null, audio: null };
        let expected = 0;

        file.onError = (error: unknown) => reject(new Error(`解析 MP4 失败：${String(error)}`));
        file.onReady = (info: any) => {
            const videoTrack = info.videoTracks?.[0];
            const audioTrack = info.audioTracks?.[0];
            plan = { video: videoTrack ? describeTrack(file, videoTrack, "video") : null, audio: audioTrack ? describeTrack(file, audioTrack, "audio") : null };
            expected = (videoTrack ? 1 : 0) + (audioTrack ? 1 : 0);
            if (!expected) {
                reject(new Error("这个文件里没有可用的音视频轨。"));
                return;
            }
            for (const track of [videoTrack, audioTrack].filter(Boolean)) {
                collected.set(track.id, []);
                file.setExtractionOptions(track.id, null, { nbSamples: Number.MAX_SAFE_INTEGER });
            }
            file.start();
        };
        file.onSamples = (trackId: number, _user: unknown, samples: any[]) => {
            const bucket = collected.get(trackId);
            if (!bucket) return;
            for (const sample of samples) {
                bucket.push({ data: sample.data, duration: sample.duration, dts: sample.dts, cts: sample.cts, is_sync: Boolean(sample.is_sync) });
            }
            // 每条轨的样本一次性回调完毕即视为读完(nbSamples 给到最大值)
            if (bucket.length && [...collected.values()].filter((items) => items.length).length === expected) {
                file.flush();
                resolve({
                    video: plan.video,
                    audio: plan.audio,
                    videoSamples: plan.video ? collected.get(plan.video.id) || [] : [],
                    audioSamples: plan.audio ? collected.get(plan.audio.id) || [] : [],
                });
            }
        };

        const view = buffer as ArrayBuffer & { fileStart?: number };
        view.fileStart = 0;
        file.appendBuffer(view);
        file.flush();
    });
}

function describeTrack(file: any, track: any, kind: "video" | "audio"): ParsedTrack {
    const trak = file.getTrackById(track.id);
    const entry = trak?.mdia?.minf?.stbl?.stsd?.entries?.[0];
    return {
        id: track.id,
        kind,
        codec: track.codec,
        timescale: track.timescale,
        type: entry?.type || (kind === "video" ? "avc1" : "mp4a"),
        // 直接复用源轨的 sample description box,省去手工拆 avcC/esds
        description: entry,
        width: track.video?.width,
        height: track.video?.height,
        channelCount: track.audio?.channel_count,
        sampleRate: track.audio?.sample_rate,
    };
}

/** "avc1.64001f" → "avc1":只比较编码族,profile/level 的细微差异不阻断拼接 */
function baseCodec(codec: string) {
    return String(codec || "").split(".")[0];
}

/** 从画布节点组装拼接输入,顺序即拼接顺序 */
export function toConcatSources(nodes: CanvasNodeData[]): ConcatSource[] {
    return nodes.map((node) => ({ nodeId: node.id, title: node.title || node.id, content: node.metadata?.content, storageKey: node.metadata?.storageKey }));
}
