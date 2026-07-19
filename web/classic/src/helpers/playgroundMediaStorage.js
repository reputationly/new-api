// 体验区媒体持久化:localStorage 只存文本 + 短引用,base64 媒体 Blob 存 IndexedDB
// (localforage,配额按磁盘计)。刷新/重启后媒体可恢复,历史会话可续问、可回看上传/生成物。
// 设计见 docs/playground-idb-media-design.md。仅 classic 主题、不改后端。
//
// 引用格式:idb-media:pm-<Date.now()>-<seq>-<rand8>。时间戳前置供孤儿清理判龄;rand8 防
// 多 tab 同毫秒撞 key。硬规则:hydrate 输出绝不残留 idb-media: 前缀(miss 一律剥掉),
// 否则裸引用会被当作媒体参数发给后端。

import localforage from 'localforage';

const MEDIA_PREFIX = 'idb-media:';

const store = localforage.createInstance({
  name: 'new-api-playground',
  storeName: 'media_files',
});
let idbUnavailable = false;
// 必须显式锁定 INDEXEDDB:localforage 默认 driver 链在 IDB 不可用(隐私模式)时会降级
// localStorage,把 Blob 序列化成 base64 写回——原样复活配额问题。锁定后不可用即 reject,
// 走"失败即剥弃"兜底,行为等价现状。
store.setDriver(localforage.INDEXEDDB).catch(() => {
  idbUnavailable = true;
});

// 模块级缓存(重挂载复用)
const objectUrls = new Map(); // key -> objectURL(消息展示用)
const resolvedUrlToKey = new Map(); // blob:/data: URL -> key(落盘换回引用)
const dataUrlToKey = new Map(); // data: 内容去重 -> key(同一 base64 只入库一次)
const persistSeq = new Map(); // storageKey -> seq(异步落盘 latest-wins)

// 已知 localStorage 会话 key(孤儿清理时扫其原始串)。新体验区接入时在此登记。
const KNOWN_STORAGE_KEYS = [
  'image_playground_conversations',
  'image_playground_conversations_i2i',
  'video_playground_conversations',
  'video_playground_conversations_i2v',
  'video_playground_conversations_flf2v',
  'video_playground_conversations_s2v',
  'video_playground_conversations_sr',
  'video_playground_conversations_vace',
  // 语音体验区:旧单键(遗留数据)+ 拆细后按 mode 分键(audioPlayground.constants.js
  // audioHistoryStorageKey;AUDIO_TAB_ORDER=emotion/synthesis/dialogue/design)。
  // 缺任一 key,孤儿清理会误删这些历史仍引用的上传/生成音频。
  'audio_playground_conversations',
  'audio_playground_conversations_emotion',
  'audio_playground_conversations_synthesis',
  'audio_playground_conversations_dialogue',
  'audio_playground_conversations_design',
  // 音乐体验区按 mode 分键(musicPlayground.constants.js musicHistoryStorageKey):
  // 原 ACE-Step t2m/cover/repaint + 新 AudioX/SoulX t2a/v2a/v2m/svs。
  'music_playground_conversations_t2m',
  'music_playground_conversations_cover',
  'music_playground_conversations_repaint',
  'music_playground_conversations_t2a',
  'music_playground_conversations_v2a',
  'music_playground_conversations_v2m',
  'music_playground_conversations_svs',
];

let rndSeq = 0;
const genKey = () => {
  const rand8 = Math.random().toString(36).slice(2, 10).padEnd(8, '0');
  return `pm-${Date.now()}-${rndSeq++}-${rand8}`;
};

export const isMediaRef = (s) =>
  typeof s === 'string' && s.startsWith(MEDIA_PREFIX);
const isDataUrl = (s) => typeof s === 'string' && s.startsWith('data:');
const isBlobUrl = (s) => typeof s === 'string' && s.startsWith('blob:');
// http(s) OR a root-relative URL (e.g. /v1/videos/{id}/content from
// buildVideoContentUrl) — both render directly as a media src and are safe to
// persist verbatim. Must be preserved by sync-persist/hydrate; otherwise a
// completed result whose async IDB cache hasn't finished is stored as '' and
// vanishes from history if the tab closes first.
const isDirectUrl = (s) =>
  typeof s === 'string' && (/^https?:/.test(s) || s.startsWith('/'));
const keyFromRef = (ref) => ref.slice(MEDIA_PREFIX.length);
const refFromKey = (key) => `${MEDIA_PREFIX}${key}`;

// 媒体字段 schema(哪些字段是媒体):
//   convArrayFields  conv 级 data-url 数组(续问要发后端 → hydrate 成 data:)
//   convStringFields conv 级单个 data-url(同上)
//   msgArrayFields   消息级 data-url 数组(仅展示 → hydrate 成 objectURL,省内存)
//   msgMediaFields   消息级单值「生成结果」(视频/音频结果 URL):抓 Blob 存 IDB → hydrate
//                    成 objectURL,刷新后直接读、后端按 TTL 清理后仍可回看。http URL 会被
//                    fetch(结果地址同源)缓存;未缓存前保留原 URL 兜底。
// 缺省覆盖图片/视频/音频三区当前字段。
const DEFAULT_SCHEMA = {
  convArrayFields: ['images', 'refImages'],
  convStringFields: ['audioData', 'sourceVideo', 'srcVideo', 'maskVideo', 'voiceData'],
  msgArrayFields: ['images'],
  msgMediaFields: [],
  markNotPersisted: false,
};
const withSchema = (opts = {}) => ({ ...DEFAULT_SCHEMA, ...opts });

// ---------- 同步:剥掉未 hydrate 的引用(useState 初值用) ----------

// 未 hydrate 前引用无法直接用作 src / 请求参数,先剥掉。image 语义下消息被剥空补
// imagesNotPersisted:true(占位文案)。返回新列表(不改入参)。
export const stripUnresolvedMediaRefs = (list, opts = {}) => {
  const s = withSchema(opts);
  const stripArr = (arr) =>
    Array.isArray(arr) ? arr.filter((x) => !isMediaRef(x)) : arr;
  const stripStr = (v) => (isMediaRef(v) ? '' : v);
  return (Array.isArray(list) ? list : []).map((conv) => {
    const next = { ...conv };
    s.convArrayFields.forEach((f) => {
      if (next[f] !== undefined) next[f] = stripArr(next[f]);
    });
    s.convStringFields.forEach((f) => {
      if (next[f] !== undefined) next[f] = stripStr(next[f]);
    });
    next.messages = (conv.messages || []).map((m) => {
      const mm = { ...m };
      let stripped = false;
      s.msgArrayFields.forEach((f) => {
        if (Array.isArray(mm[f])) {
          const before = mm[f].length;
          mm[f] = mm[f].filter((x) => !isMediaRef(x));
          if (mm[f].length < before) stripped = true;
        }
      });
      // 单值生成结果:未 hydrate 的引用无法作 src → 置空(hydrate 后恢复成 objectURL);
      // http/data 原样保留(可直接渲染)。
      s.msgMediaFields.forEach((f) => {
        if (isMediaRef(mm[f])) mm[f] = '';
      });
      if (s.markNotPersisted && stripped && (mm.images || []).length === 0) {
        mm.imagesNotPersisted = true;
      }
      return mm;
    });
    return next;
  });
};

// ---------- 落盘:两段式(同步 strip+setItem → 异步 externalize) ----------

// 同步段的 strip:data: 剥弃(=旧行为);blob: 命中 resolvedUrlToKey 换引用、否则剥弃;
// idb-media: / http / 根相对 URL 原样保留(相对 URL 是视频/音频结果地址,异步缓存未完成
// 前也要留住,否则关页即丢)。保证最坏情况不弱于旧行为。
const stripForSyncPersist = (list, s) => {
  const mapStr = (v) => {
    if (isMediaRef(v) || isDirectUrl(v)) return v;
    if (isBlobUrl(v) && resolvedUrlToKey.has(v)) return refFromKey(resolvedUrlToKey.get(v));
    return ''; // data: 或未命中 blob:
  };
  const mapArr = (arr) =>
    Array.isArray(arr)
      ? arr.map(mapStr).filter((x) => x !== '')
      : arr;
  return list.map((conv) => {
    const next = { ...conv };
    s.convArrayFields.forEach((f) => {
      if (next[f] !== undefined) next[f] = mapArr(next[f]);
    });
    s.convStringFields.forEach((f) => {
      if (next[f] !== undefined) next[f] = mapStr(next[f]);
    });
    next.messages = (conv.messages || []).map((m) => {
      const mm = { ...m };
      s.msgArrayFields.forEach((f) => {
        if (Array.isArray(mm[f])) {
          const kept = mm[f].map(mapStr).filter((x) => x !== '');
          if (s.markNotPersisted && kept.length === 0 && mm[f].length > 0) {
            mm.imagesNotPersisted = true;
          }
          mm[f] = kept;
        }
      });
      // 单值生成结果:idb-media/http 保留(http 兜底,异步段再抓 Blob 换引用),
      // blob: 命中换引用、否则剥;data: 剥(结果一般是 http,不会走这)。
      s.msgMediaFields.forEach((f) => {
        if (mm[f] !== undefined) mm[f] = mapStr(mm[f]);
      });
      return mm;
    });
    return next;
  });
};

// 把一个 data: URL 外化到 IDB,返回引用(去重命中直接返回旧引用)。失败返回 ''。
const externalizeDataUrl = async (dataUrl) => {
  if (dataUrlToKey.has(dataUrl)) return refFromKey(dataUrlToKey.get(dataUrl));
  if (idbUnavailable) return '';
  try {
    const blob = await (await fetch(dataUrl)).blob();
    const key = genKey();
    await store.setItem(key, blob);
    dataUrlToKey.set(dataUrl, key);
    resolvedUrlToKey.set(dataUrl, key);
    return refFromKey(key);
  } catch (e) {
    return '';
  }
};

// 外化一个字符串媒体值 → 引用 / 原样 / ''。http 或根相对 URL 原样保留(与同步段
// mapStr、hydrate 一致;否则异步二次落盘会把相对 URL 覆盖成空)。
const externalizeStr = async (v) => {
  if (!v || isMediaRef(v) || isDirectUrl(v)) return v || '';
  if (isDataUrl(v)) return await externalizeDataUrl(v);
  if (isBlobUrl(v)) return resolvedUrlToKey.has(v) ? refFromKey(resolvedUrlToKey.get(v)) : '';
  return '';
};
const externalizeArr = async (arr) => {
  if (!Array.isArray(arr)) return arr;
  const out = [];
  for (const v of arr) {
    const r = await externalizeStr(v);
    if (r) out.push(r);
  }
  return out;
};

// 生成结果媒体(视频/音频结果 URL):与 externalizeStr 不同——http URL 也 fetch 进 IDB
// (结果地址同源,跨刷新缓存 + 后端 TTL 清理后仍可回看)。url→key 去重,同一结果只抓一次;
// 抓取失败(后端已清理/网络异常)保留原 URL 兜底。
const externalizeMedia = async (v) => {
  if (!v || isMediaRef(v)) return v || '';
  if (resolvedUrlToKey.has(v)) return refFromKey(resolvedUrlToKey.get(v));
  if (idbUnavailable) return v;
  if (isBlobUrl(v)) return v; // 内存态 objectURL,无法可靠再 fetch,保留
  try {
    const blob = await (await fetch(v)).blob();
    const key = genKey();
    await store.setItem(key, blob);
    resolvedUrlToKey.set(v, key);
    return refFromKey(key);
  } catch (e) {
    return v;
  }
};

export const persistWithMedia = (storageKey, list, opts = {}) => {
  const s = withSchema(opts);
  const limit = opts.limit || 10;
  const capped = (Array.isArray(list) ? list : []).slice(0, limit);
  // 同步段:立刻落文本(不弱于旧行为)
  try {
    localStorage.setItem(storageKey, JSON.stringify(stripForSyncPersist(capped, s)));
  } catch (e) {
    // 忽略配额错误(理论上此时只剩引用+文本,不该超)
  }
  // 异步段:外化媒体 + latest-wins 二次落盘
  const mySeq = (persistSeq.get(storageKey) || 0) + 1;
  persistSeq.set(storageKey, mySeq);
  (async () => {
    try {
      const externalized = [];
      for (const conv of capped) {
        const next = { ...conv };
        for (const f of s.convArrayFields) {
          if (next[f] !== undefined) next[f] = await externalizeArr(next[f]);
        }
        for (const f of s.convStringFields) {
          if (next[f] !== undefined) next[f] = await externalizeStr(next[f]);
        }
        next.messages = [];
        for (const m of conv.messages || []) {
          const mm = { ...m };
          for (const f of s.msgArrayFields) {
            if (Array.isArray(mm[f])) mm[f] = await externalizeArr(mm[f]);
          }
          for (const f of s.msgMediaFields) {
            if (mm[f] !== undefined) mm[f] = await externalizeMedia(mm[f]);
          }
          next.messages.push(mm);
        }
        externalized.push(next);
      }
      if (persistSeq.get(storageKey) !== mySeq) return; // 被更新的 persist 取代,放弃
      localStorage.setItem(storageKey, JSON.stringify(externalized));
    } catch (e) {
      // 外化整体失败:同步段已落了保底文本,忽略
    } finally {
      scheduleMediaCleanup();
    }
  })();
};

// ---------- 载入:hydrate(引用 → data: / objectURL) ----------

const blobToDataUrl = (blob) =>
  new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(r.result);
    r.onerror = reject;
    r.readAsDataURL(blob);
  });

// conv 级:引用 → data: URL(续问要发后端),并反向登记去重表(否则刷新后首次 persist 会
// 把同一图再存一个新 key,写放大)。
const hydrateToDataUrl = async (ref) => {
  if (!isMediaRef(ref)) return isDataUrl(ref) || isDirectUrl(ref) ? ref : '';
  if (idbUnavailable) return '';
  try {
    const blob = await store.getItem(keyFromRef(ref));
    if (!blob) return '';
    const dataUrl = await blobToDataUrl(blob);
    dataUrlToKey.set(dataUrl, keyFromRef(ref));
    resolvedUrlToKey.set(dataUrl, keyFromRef(ref));
    return dataUrl;
  } catch (e) {
    return '';
  }
};

// 消息级:引用 → objectURL(仅展示,省内存),objectUrls 缓存优先。
const hydrateToObjectUrl = async (ref) => {
  if (!isMediaRef(ref)) return isDataUrl(ref) || isDirectUrl(ref) ? ref : '';
  if (idbUnavailable) return '';
  const key = keyFromRef(ref);
  if (objectUrls.has(key)) return objectUrls.get(key);
  try {
    const blob = await store.getItem(key);
    if (!blob) return '';
    const url = URL.createObjectURL(blob);
    objectUrls.set(key, url);
    resolvedUrlToKey.set(url, key);
    return url;
  } catch (e) {
    return '';
  }
};

export const hydrateConversationsFromStorage = async (list, opts = {}) => {
  const s = withSchema(opts);
  const out = [];
  for (const conv of Array.isArray(list) ? list : []) {
    const next = { ...conv };
    for (const f of s.convArrayFields) {
      if (Array.isArray(next[f])) {
        const arr = [];
        for (const v of next[f]) {
          const r = await hydrateToDataUrl(v);
          if (r) arr.push(r);
        }
        next[f] = arr;
      }
    }
    for (const f of s.convStringFields) {
      if (next[f] !== undefined) next[f] = await hydrateToDataUrl(next[f]);
    }
    next.messages = [];
    for (const m of conv.messages || []) {
      const mm = { ...m };
      for (const f of s.msgArrayFields) {
        if (Array.isArray(mm[f])) {
          const arr = [];
          for (const v of mm[f]) {
            const r = await hydrateToObjectUrl(v);
            if (r) arr.push(r);
          }
          if (s.markNotPersisted && arr.length === 0 && mm[f].length > 0) {
            mm.imagesNotPersisted = true;
          }
          mm[f] = arr;
        }
      }
      // 单值生成结果:引用 → objectURL(从 IDB 直读);http/data 原样保留(兜底)。
      for (const f of s.msgMediaFields) {
        if (mm[f] !== undefined) mm[f] = await hydrateToObjectUrl(mm[f]);
      }
      next.messages.push(mm);
    }
    out.push(next);
  }
  return out;
};

// ---------- 孤儿清理 ----------

let cleanupTimer = null;
let followupTimer = null;
const TEN_MIN = 10 * 60 * 1000;
export const scheduleMediaCleanup = () => {
  if (cleanupTimer) return;
  cleanupTimer = setTimeout(() => {
    cleanupTimer = null;
    runCleanup();
  }, 30000);
};

// 会话被挤出 top-10 后其 Blob 即成孤儿,但若年龄 < 10min 会被本轮清理的安全期跳过。若之后
// 用户不再触发 persist,清理不会再跑 → 孤儿拖到下次会话才回收。故当本轮跳过了"未引用但年轻"
// 的 Blob 时,自动重排一次延时清理(等它们过安全期),闭掉这个缝、保证同会话内回收。
const rescheduleCleanupLater = () => {
  if (followupTimer) return;
  followupTimer = setTimeout(() => {
    followupTimer = null;
    runCleanup();
  }, TEN_MIN + 30000);
};

const runCleanup = async () => {
  if (idbUnavailable) return;
  // 引用集:扫已知 key 的原始串(结构无关,对字段变化免疫)
  const referenced = new Set();
  KNOWN_STORAGE_KEYS.forEach((k) => {
    const raw = localStorage.getItem(k);
    if (!raw) return;
    const m = raw.match(/idb-media:[\w-]+/g);
    if (m) m.forEach((ref) => referenced.add(keyFromRef(ref)));
  });
  try {
    const stale = [];
    let skippedYoungOrphan = false;
    await store.iterate((_value, key) => {
      if (referenced.has(key)) return;
      // 跳过年龄 < 10min 的(封多 tab 竞态:B 已写 IDB 尚未落 localStorage 时 A 别删)
      const ts = parseInt((key.split('-')[1] || '0'), 10);
      if (ts && Date.now() - ts < TEN_MIN) {
        skippedYoungOrphan = true; // 未引用但年轻 → 稍后重排一次
        return;
      }
      stale.push(key);
    });
    for (const key of stale) {
      await store.removeItem(key);
      const url = objectUrls.get(key);
      if (url) URL.revokeObjectURL(url);
      objectUrls.delete(key);
      // 清反向表里指向该 key 的项
      for (const [u, k] of resolvedUrlToKey) if (k === key) resolvedUrlToKey.delete(u);
      for (const [d, k] of dataUrlToKey) if (k === key) dataUrlToKey.delete(d);
    }
    if (skippedYoungOrphan) rescheduleCleanupLater();
  } catch (e) {
    // 忽略
  }
};
