import { useEffect, useRef } from 'react';

// 各体验区 *_MEDIA_SCHEMA 的 conv 级媒体字段合集(video/image/music/audio)。
// 这些字段落盘时被换成 idb-media: 引用,读回来先经 stripUnresolvedMediaRefs 剥成空值,
// 所以「出现非空值」= IDB 媒体已还原,可作为 hydrate 完成的判据。
// 新增体验区/新增上传字段时需同步这里,漏了只会退化成不补媒体,不会出错。
const CONV_MEDIA_FIELDS = [
  'images',
  'refImages',
  'audioData',
  'sourceVideo',
  'srcVideo',
  'srcVideo2', // 只有收口前的老双视频会话有(仍在 VIDEO_MEDIA_SCHEMA 里,会被正常 hydrate)
  'videoData',
  'promptAudioData',
  'targetAudioData',
  'voiceData',
  'emotionAudioData',
  'refAudioData',
  'refAudio2Data',
];

const hasRestoredMedia = (conv) =>
  CONV_MEDIA_FIELDS.some((f) => {
    const v = conv[f];
    return Array.isArray(v) ? v.some(Boolean) : !!v;
  });

// 挂载时自动选中最近一条会话。
//
// 四个媒体体验区的会话本身是持久化的(元数据 localStorage + 媒体 IDB),但 hook 里的
// currentConvId 每次挂载都从 null 开始,messages 又是按它从会话列表里查的——桌面端靠
// 常驻历史面板点一条恢复,移动端没有该入口,于是刷新后表现为"历史全丢"。这里在挂载时
// 把最近一条选回来,等价于替用户点了一次历史。
//
// 两段式:conversations 的初值是同步从 localStorage 读出的,挂载即可选中,用户马上看到
// 内容;媒体从 IDB 还原(hydrate)是异步的,等还原完再 open 一次,把输入媒体(首帧图/参考图/
// 参考音色等)补进 inputs,否则锁定态下的输入预览会一直空着。
// 二次 open 只在用户尚未切走(currentConvId 仍是自动选中的那条)时执行。
export const useAutoOpenLatest = (
  conversations,
  currentConvId,
  openHistoryItem,
) => {
  const autoIdRef = useRef(null);
  const refreshedRef = useRef(false);

  useEffect(() => {
    const latest = conversations[0];
    if (!latest) return;
    autoIdRef.current = latest.id;
    openHistoryItem(latest);
    // 只在挂载时跑:此后用户点「新建会话」不该被抢回。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // 判据必须是「媒体真的还原了」,不能是「conv 引用变了」:最近一条会话若有进行中任务,
  // 上面第一次 open 就会 resumePoll,而 resumePoll 开头的 patchConvMessage 会立刻换掉
  // conv 引用 —— 用引用比对的话,这次补媒体的机会会被白白消耗在媒体尚未还原的对象上,
  // 等真正 hydrate 完成时反而被 refreshedRef 挡住,输入媒体预览就永久空着了。
  // patch 只动 messages,不碰下面这些 conv 级媒体字段,故不会误触发。
  useEffect(() => {
    if (refreshedRef.current || autoIdRef.current == null) return;
    if (currentConvId !== autoIdRef.current) return; // 用户已新建/切走,不抢
    const conv = conversations.find((c) => c.id === autoIdRef.current);
    // 尚未还原,或该会话本就没有输入媒体(纯文生)——后者无媒体可补,保持待命即可。
    if (!conv || !hasRestoredMedia(conv)) return;
    refreshedRef.current = true;
    openHistoryItem(conv);
  }, [conversations, currentConvId, openHistoryItem]);
};
