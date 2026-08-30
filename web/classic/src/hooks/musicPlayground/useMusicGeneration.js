import {
  useState,
  useEffect,
  useCallback,
  useContext,
  useMemo,
  useRef,
} from 'react';
import { useTranslation } from 'react-i18next';
import { StatusContext } from '../../context/Status';
import { UserContext } from '../../context/User';
import {
  persistWithMedia,
  hydrateConversationsFromStorage,
  stripUnresolvedMediaRefs,
} from '../../helpers/playgroundMediaStorage';
import { isPlaygroundConfigIssue } from '../../helpers/playground';
import {
  parsePlaygroundTabConfig,
  getPromptOptimizeGlobal,
} from '../../constants/playgroundAdmin.constants';
import { urlToDataUrl } from '../../utils/playgroundMedia';
import {
  API,
  showError,
  showInfo,
  processGroupsData,
  processModelsData,
  getUserModelsCached,
  cachedGet,
  containsCJK,
} from '../../helpers';
import {
  MUSIC_API_ENDPOINTS,
  MUSIC_STATUS,
  MUSIC_MODES,
  MUSIC_HISTORY_LIMIT,
  MUSIC_CONV_TURN_LIMIT,
  MUSIC_POLL_INTERVAL_MS,
  MUSIC_POLL_MAX_TIMES,
  MUSIC_DURATIONS,
  MUSIC_DEFAULT_DURATION,
  MUSIC_DEFAULT_REPAINT_MODE,
  musicHistoryStorageKey,
  normalizeMusicStatus,
  parseProgress,
  buildMusicContentUrl,
  parseMusicModelConfig,
  getEngineForMusicModel,
  getMusicModelSet,
  getMaxCharsForModel,
  getRefAudioMaxMBForModel,
  getVideoMaxMBForModel,
  getTranslationForModel,
  MUSIC_ENGINE_MINIMAX_MUSIC3,
} from '../../constants/musicPlayground.constants';

// 中译英走体验区聊天门面(单次非流式);后端按会话身份注入上游 key。
const MUSIC_TRANSLATE_ENDPOINT = '/pg/chat/completions';

// 内置翻译模板(设计 §8):把用户输入转成一句 AudioCaps 风格英文音频描述。

// MiniMax-Music3 的中译英。**不能复用上面那份** —— 那份是给 AudioX 写的:
// AudioCaps 风格的音景描述、≤40 词、明令去掉 BPM 与 [verse]/[chorus] 标记。
// 而 Music3 的 instructions 要的正好是它禁掉的东西(Genre / BPM / Key / Vocals /
// Arrangement 的 Structured Caption,官方示例本身就 50 词以上)。拿 AudioX 那份去译,
// 引擎会收到一段被削平的 caption —— 不报错,只是编曲质量默默变差。
//
// 与「AI 优化提示词」的 MUSIC3_PROMPT 分工不同:那个是用户主动点的,可以扩写补全;
// 这个是提交时自动跑的,必须**忠实**——只做语言转换与格式归位,绝不替用户补
// 他没说的 BPM / 调式 / 乐器。
//
// 歌词不经过这里(它走 params.lyrics → prompt 位),所以无需在此声明保护;
// 但仍加一条兜底:用户若把歌词误贴进描述框,译文里也不该出现整句词。
export const TRANSLATE_SYSTEM_MUSIC3 = `You translate a user's music description into English for MiniMax-Music3's "instructions" field.
Rules:
- Output English only. Output ONLY the description itself — no preface, no explanation, no labels, no markdown. Never begin with "Sure", "Here is", "好的".
- Keep every musical detail the user gave (genre, tempo/BPM, key, mood, instruments, vocal type, section changes) and keep their emphasis. Translate faithfully.
- Do NOT invent details the user did not give. If they did not state a tempo or a key, leave it out rather than making one up.
- Where the user's wording maps onto the model's caption structure, use its labelled style: "Genre: … BPM: … Key: … Vocals: … Arrangement: …". Otherwise keep their prose order.
- Length follows the input: a one-line request stays one line. Do not pad it out.
- If the user names a vocal language (Mandarin, Cantonese, English …), state it as a vocal attribute; never switch the description itself into that language.
- This field describes the music, not the words. If the input contains lines that are clearly lyrics, describe how they should be sung and drop the words themselves — never translate lyrics into the description.`;

// 拟方案(文生音乐两步流程的第一步)。
//
// 官方 harness 的 Simple Mode 是两步:一句话 →【Create Sample】让 5Hz LM 产出
// caption/歌词/BPM/调式/时长 → 用户审阅编辑 →【Generate Music】。我们原来把两步压成一步,
// 在"未填歌词"时静默下发 sample_mode,而引擎在 sample_mode 下会用 LM 自己推的
// duration/bpm/keyscale 无条件覆盖用户下发值(llm_generation_inputs.py),于是"选了 60 秒
// 出 2:30"。补上审阅这一步后,提交时 caption+lyrics 都有值 → 不再命中 sample_mode 分支 →
// 时长/BPM 自然生效。
//
// 这里用的是网关自己的通用语言模型,不是 ACE-Step 的 5Hz LM(那要给门面加 create_sample
// 直通 + 两仓部署)。官方文档称模型对 caption 格式不敏感,且"用 LLM 改写模板"本就是官方
// 给的写 caption 方法之一,所以这条路是被认可的;若实测音乐性不足,再考虑接 5Hz LM。
// 每个字段的取值域都照 ACE-Step 1.5 引擎侧的硬约束写(acestep/constants.py):
//   VALID_LANGUAGES / VALID_KEYSCALES(注意 major|minor 小写、"A minor" 而非 "Am")/
//   BPM_MIN..MAX=30..300。
// 写错格式引擎会静默忽略该字段,退回自动推断 —— 用户以为设了,其实没生效。
//
// 时长是唯一一个不按引擎上限(10..600)写的字段:体验区的下拉只给 30/60/90/120,拟稿要是
// 回个 180,下拉里根本没有这一项,面板显示的和实际下发的就对不上了。所以提示词里直接把
// 取值域收成这四档,回填前再 snapMusicDuration 兜一次底(模型不听话是常态)。歌词篇幅也据此
// 定量 —— 词写多了引擎不会报错,只会把后面的段落唱没或整首赶着唱完。
const DRAFT_SYSTEM = `You are a music production assistant for ACE-Step 1.5, a text-to-music diffusion model. Given a user's one-line song idea, produce a complete production plan that the model can consume directly.

Return ONLY a JSON object. No markdown fence, no explanation, no preface:
{"caption": string, "lyrics": string, "bpm": number, "keyScale": string, "duration": number, "vocalLanguage": string}

Field rules — these are hard constraints from the engine; a malformed value is silently dropped and the setting is lost:

- caption: ENGLISH only. This is the single most important input. Cover these dimensions: genre/style, instrumentation, mood and atmosphere, timbre and production texture, vocal gender and delivery, arrangement or progression. Be concrete ("dreamy shoegaze with reverb-heavy guitars and whispered female vocals"), not generic ("nice music"). Comma-separated tags and natural prose both work.
  NEVER put tempo, BPM, key, or time signature in the caption — the model gets confused when the caption contradicts the dedicated fields. Put them in bpm / keyScale only.

- lyrics: actual singable lyrics in the language the user asked for (default: the language of the user's own request). Structure with section tags on their own line: [Verse 1], [Chorus], [Verse 2], [Bridge], [Outro]. Also available: [Interlude], [Instrumental] for non-vocal sections. Make the chorus memorable and repetitive.
  For a purely instrumental piece output exactly "[Instrumental]" and nothing else.
  THE AMOUNT OF LYRICS MUST FIT \`duration\` — decide duration first, then write to that budget. A sung line runs roughly 4 seconds, so the whole song gets about duration/4 lines (section tags are not lines). Concretely:
    30s  -> ~7 lines total, one short pair such as [Verse 1] + [Chorus]
    60s  -> ~15 lines, e.g. [Verse 1] [Chorus] [Verse 2] [Chorus]
    90s  -> ~22 lines, add a [Bridge] or an [Interlude]
    120s -> ~30 lines, a full [Verse] [Chorus] [Verse] [Chorus] [Bridge] [Chorus] [Outro]
  Writing too many lines is the usual failure: the generation runs out of time, so the last sections are cut off or the whole song is rushed and unintelligible. When torn between two lengths, write the shorter one.

- bpm: integer, 30-300. Typical: slow ballad 60-80, mid-tempo 90-120, fast 130-180. Must not contradict the mood described in the caption.

- keyScale: EXACTLY the format "<note><accidental> <mode>" where note is A-G, accidental is empty / # / b, and mode is lowercase "major" or "minor". Valid: "C major", "A minor", "F# minor", "Bb major". INVALID: "C Major", "Am", "F#m", "C". Common keys (C, G, D, A minor, E minor) are the most stable.

- duration: integer seconds, and MUST be exactly one of 30, 60, 90, 120 — the playground's duration control offers no other value, and anything else gets snapped to the nearest of these before it reaches the engine.
  If the user states or hints at a target length ("两分钟", "90 秒左右", "a minute or so", "短一点"), honour it: pick the nearest of the four. Anything longer than 120 seconds is capped at 120 — this playground generates at most two minutes, so treat "五分钟的完整歌曲" as a 120-second piece and write the lyrics for 120 seconds, not for five minutes.
  If the user says nothing about length, choose from the material: a loop or jingle 30, a single verse-chorus 60, a compact song 90, a full song 120.

- vocalLanguage: one BCP-47-ish code from the engine's list. Common: zh (Mandarin), yue (Cantonese), en, ja, ko, es, fr, de, ru, pt, it, ar, hi, th, vi. Use "unknown" for instrumental tracks. Must match the language the lyrics are actually written in.

Keep the user's intent faithfully. Do not substitute a different genre, mood, or language than the one asked for.`;

// 拟稿回来的时长收敛到「目标时长」下拉真有的那几档(MUSIC_DURATIONS,去掉 '' 自动档)。
// 提示词里已经写死了这四档,这里是兜底 —— 模型回个 180 的话,下拉里没有这一项,面板显示
// 的和实际下发的就对不上。取最近的一档,于是超过 120 的一律落到 120(体验区上限);
// 正好卡在两档中间时(45)取小的那档,与提示词里「拿不准就写短的」同向。
// 非法值返回 '' → 调用方不动原值。
const MUSIC_DURATION_CHOICES = MUSIC_DURATIONS.map(Number).filter(
  (n) => Number.isFinite(n) && n > 0,
);

const snapMusicDuration = (value) => {
  const n = Number(value);
  if (!Number.isFinite(n) || n <= 0 || !MUSIC_DURATION_CHOICES.length)
    return '';
  return String(
    MUSIC_DURATION_CHOICES.reduce((best, cur) =>
      Math.abs(cur - n) < Math.abs(best - n) ? cur : best,
    ),
  );
};

// 音乐模型体验区 hook。一个 hook 覆盖全部 7 个玩法(mode),同一异步任务门面
// (/pg/videos)、同一轮询/历史/锁定模式;按 mode 的 engine 分支输入形态与 metadata:
//   - acestep(t2m/cover/repaint):描述 caption(prompt)+ 可选歌词/时长 +
//     (cover/repaint)驱动音频(单音频)。
//   - audiox(t2a/v2a/v2m):t2a 纯文本;v2a/v2m 视频上传(metadata.video)+ 可选文本
//     (有文本→tv2a/tv2m,否则 v2a/v2m)。
//   - soulx(svs):双音频上传(metadata.prompt_audio + metadata.target_audio),无需文本。
// 上传的音频/视频是 base64 data-url,以 Blob 存 IndexedDB,localStorage 只留短引用;
// 刷新后可恢复、可续问。纯文本玩法(t2m/t2a)无上传,不受影响。

const MUSIC_MEDIA_SCHEMA = {
  convArrayFields: [],
  // 覆盖全部上传字段:acestep 驱动音频 + audiox 视频 + soulx 双音频。
  convStringFields: [
    'audioData',
    'videoData',
    'promptAudioData',
    'targetAudioData',
  ],
  msgArrayFields: [],
  // 生成的音频结果:抓 Blob 缓存进 IDB,同视频/语音。格式无关(.mp3 / .wav)。
  msgMediaFields: ['musicUrl'],
  markNotPersisted: false,
};

const loadConversations = (storageKey) => {
  try {
    const raw = localStorage.getItem(storageKey);
    const parsed = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed) ? parsed : [];
  } catch (e) {
    return [];
  }
};

const persistConversations = (storageKey, list) => {
  persistWithMedia(storageKey, list, {
    ...MUSIC_MEDIA_SCHEMA,
    limit: MUSIC_HISTORY_LIMIT,
  });
};

// 加载漏斗:重建 completed 消息的空 musicUrl(初始态与 hydrate 两条路径都经此)。
// 结果音频以 Blob 缓存进 IDB,localStorage 只留 idb-media: 引用 —— 引用未 hydrate(或
// blob 已被孤儿清理删掉)时会被剥成 '',而 AsyncTaskBubble/MusicChatArea 判「完成」都要
// status + url 双条件,于是一条早已生成好的曲子会退化成永远 100% 的「生成中」。任务产物
// 在后端按 taskId 可寻址,故这里直接用 taskId 重建 URL。identity 保持:无改动的
// conv/message 原样返回,不破坏 hydrate 的引用比对。同 useVideoGeneration 的 ensureVideoUrls。
const ensureMusicUrls = (list) => {
  if (!Array.isArray(list)) return list;
  return list.map((conv) => {
    let changed = false;
    const messages = (conv.messages || []).map((m) => {
      if (
        m.role === 'assistant' &&
        m.status === MUSIC_STATUS.COMPLETED &&
        m.taskId &&
        !m.musicUrl
      ) {
        changed = true;
        return { ...m, musicUrl: buildMusicContentUrl(m.taskId) };
      }
      return m;
    });
    return changed ? { ...conv, messages } : conv;
  });
};

let idSeq = 0;
const genId = () => `mus-${Date.now()}-${idSeq++}`;

const extractApiErrMsg = (error, fallback) => {
  const d = error?.response?.data || {};
  return d.error?.message || d.message || error?.message || fallback;
};

// 会话内需持久化/回填的全部参数字段(随 mode 使用其子集)。
const PARAM_FIELDS = [
  'group',
  'model',
  // acestep
  'lyrics',
  'duration',
  'audioData',
  'audioName',
  'bpm',
  'vocalLanguage',
  'keyScale',
  // acestep 改编/重绘
  'coverStrength',
  'repaintStart',
  'repaintEnd',
  'repaintMode',
  'repaintStrength',
  // 引用上一首生成结果作为源音频(task:<task_id>);有值时不需要上传
  'srcTaskId',
  'srcTaskLabel',
  // 通用
  'seed',
  'guidanceScale',
  'inferenceSteps',
];

const pickParams = (src) => {
  const out = {};
  PARAM_FIELDS.forEach((f) => {
    out[f] = src[f];
  });
  return out;
};

// mode 参数化:t2m/cover/repaint(ACE-Step)+ t2a/v2a/v2m/svs(AudioX/SoulX)。
export const useMusicGeneration = (mode = 't2m') => {
  const { t } = useTranslation();
  const [statusState] = useContext(StatusContext);
  const [userState] = useContext(UserContext);

  const modeDef = MUSIC_MODES[mode] || MUSIC_MODES.t2m;
  const {
    capability,
    matchCapabilities,
    engine,
    needsAudio,
    audioMetaKey,
    needsText,
    resolveTaskType,
  } = modeDef;
  const storageKey = musicHistoryStorageKey(mode);

  const [inputs, setInputs] = useState({
    group: '',
    model: '',
    // acestep:歌词/时长/驱动音频
    lyrics: '', // 可选歌词(metadata.lyrics);留空则由引擎按 caption 自动生成
    duration: MUSIC_DEFAULT_DURATION, // 秒;'' = 引擎默认
    audioData: '', // 驱动音频(base64 data-url);仅 cover/repaint 使用
    audioName: '',
    bpm: '', // 速度;空 = 自动
    vocalLanguage: '', // 演唱语言;空 = 自动
    keyScale: '', // 调式(如 C Major / Am);空 = 自动
    // acestep 改编(cover):保留原曲结构的程度;空 = 引擎默认 1.0
    coverStrength: '',
    // acestep 重绘(repaint):重绘区间与力度。区间空 = 全曲重绘(引擎默认,与改编无异)
    repaintStart: '',
    repaintEnd: '',
    repaintMode: MUSIC_DEFAULT_REPAINT_MODE,
    repaintStrength: '',
    // 引用上一首生成结果当源音频:有值时走 task:<id>,免去"下载再上传"
    srcTaskId: '',
    srcTaskLabel: '',
    // 通用高级参数:留空即不下发,走引擎默认。
    seed: '', // 指定后可复现;空 = 随机
    guidanceScale: '', // 贴合描述程度;空 = 引擎默认
    inferenceSteps: '', // 采样步数;空 = 引擎默认
  });
  const [groups, setGroups] = useState([]);
  const [models, setModels] = useState([]);
  const [modelGroupsMap, setModelGroupsMap] = useState(new Map());

  const initialConvsRef = useRef(null);
  const [conversations, setConversations] = useState(() => {
    const raw = loadConversations(storageKey);
    // strip 后立刻用 taskId 重建 completed 消息的空 musicUrl,再存进 initialConvsRef——
    // 保证 initialSet 与 state 引用一致(hydrate 的引用比对不被破坏)。
    const stripped = ensureMusicUrls(
      stripUnresolvedMediaRefs(raw, MUSIC_MEDIA_SCHEMA),
    );
    initialConvsRef.current = { raw, stripped };
    return stripped;
  });
  const [currentConvId, setCurrentConvId] = useState(null);
  const [generating, setGenerating] = useState(false);
  // ACE-Step 的「AI 优化提示词」(draftPlan)调用中(单次非流式,1~3 秒);按钮据此转圈并禁用。
  const [drafting, setDrafting] = useState(false);

  const messages = useMemo(() => {
    const conv = conversations.find((c) => c.id === currentConvId);
    return conv ? conv.messages : [];
  }, [conversations, currentConvId]);

  // 对话内已生成过 → 参数锁定(同语音):模型/上传/参数均不可改,直到新对话。
  const locked = currentConvId !== null;

  const conversationsRef = useRef(conversations);
  conversationsRef.current = conversations;
  const lockedRef = useRef(locked);
  lockedRef.current = locked;
  const groupRef = useRef(inputs.group);
  groupRef.current = inputs.group;
  const activePollRef = useRef(null);

  // mount 后从 IDB 还原上传的音频/视频,按初始对象引用逐条合并(不整体覆盖)。
  useEffect(() => {
    let canceled = false;
    const init = initialConvsRef.current;
    if (!init || !(init.raw || []).length) return;
    (async () => {
      const hydrated = await hydrateConversationsFromStorage(
        init.raw,
        MUSIC_MEDIA_SCHEMA,
      );
      if (canceled) return;
      const hydratedById = new Map(hydrated.map((c) => [c.id, c]));
      const initialSet = new Set(init.stripped);
      const mediaFields = [
        ...MUSIC_MEDIA_SCHEMA.convArrayFields,
        ...MUSIC_MEDIA_SCHEMA.convStringFields,
      ];
      setConversations((prev) =>
        // hydrated 版本若 IDB blob 缺失,musicUrl 会是 '';外面再兜一层 taskId 重建,
        // 避免 completed 消息被还原成空 URL 而渲染成「生成中」。
        ensureMusicUrls(
          prev.map((c) => {
            const h = hydratedById.get(c.id);
            if (!h) return c;
            if (initialSet.has(c)) return h;
            const merged = { ...c };
            mediaFields.forEach((f) => {
              merged[f] = h[f];
            });
            return merged;
          }),
        ),
      );
    })();
    return () => {
      canceled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleInputChange = useCallback((key, value) => {
    if (lockedRef.current) return;
    setInputs((prev) => ({ ...prev, [key]: value }));
  }, []);

  // 一键示例:标量参数(params)+ 文件(files:字段→素材 URL)一次性写入 inputs。
  // 文件 URL fetch→base64 data-url(与手动上传同形态);数组字段逐个转。锁定时忽略。
  const applyExample = useCallback(
    async (ex) => {
      if (lockedRef.current || !ex || typeof ex !== 'object') return;
      try {
        const patch = { ...(ex.params || {}) };
        const entries = await Promise.all(
          Object.entries(ex.files || {}).map(async ([field, url]) => [
            field,
            Array.isArray(url)
              ? await Promise.all(url.map(urlToDataUrl))
              : await urlToDataUrl(url),
          ]),
        );
        entries.forEach(([field, value]) => {
          patch[field] = value;
        });
        if (lockedRef.current) return;
        setInputs((prev) => ({ ...prev, ...patch }));
      } catch (e) {
        showError(t('加载示例素材失败,请重试'));
      }
    },
    [t],
  );

  // 音乐模型集合 = 「音乐模型配置」里声明、且能力含当前 tab 能力的模型。
  const modelConfig = useMemo(
    () => parseMusicModelConfig(statusState?.status?.MusicModelConfig),
    [statusState?.status?.MusicModelConfig],
  );

  const musicModelSet = useMemo(
    () => getMusicModelSet(modelConfig, capability, matchCapabilities),
    [modelConfig, capability, matchCapabilities],
  );

  // 当前模型要不要中译英。
  // 实际引擎:模型声明优先,未声明才回退 tab 默认(modeDef.engine)。
  //
  // tab 默认是硬编码的(「文生音乐」恒为 acestep),而同一个 tab 可以挂多个引擎的模型。
  // MiniMax-Music3 也是文生音乐,只按 tab 判会走 ACE-Step 分支拿到 lyrics/thinking
  // 这些它不认的键,而它必需的 instructions 一个都不下发,引擎侧直接 400。
  const resolvedEngine = useMemo(
    () => getEngineForMusicModel(modelConfig, inputs.model) || engine,
    [modelConfig, inputs.model, engine],
  );

  const translationCfg = useMemo(
    () => getTranslationForModel(modelConfig, inputs.model, mode),
    [modelConfig, inputs.model, mode],
  );
  // 中译英是**针对 AudioX 这一个引擎的特殊处理**,不是文生音效这个玩法的固有属性:
  // AudioX 的文本编码器只认英文,中文进去会塌成 <unk>。所以判据是「玩法可能需要 +
  // 当前模型声明了需要」两层,换成认中文的模型时运营把那个开关关掉即可,代码不用动
  // (同视频页按引擎族给 MiniMax H3 换提示词模板的处理)。
  //
  // 早先这里还控制左侧一个「语言模型」下拉,让用户自己挑翻译模型;现已撤掉 ——
  // 翻译与「AI 优化提示词」的两条实现都是同一类辅助调用,统一用运营在「体验区管理 →
  // 通用设置」里配的那个模型(见下面的 promptOptimizeGlobal)。
  //
  // MiniMax-Music3 也归进「玩法可能需要」这一层。它与 AudioX 的理由不同:不是中文会塌成
  // <unk>,而是官方 README 里 Structured Caption 的示例清一色英文、对中文 caption 只字
  // 未提 —— 属于"英文一定行、中文没说"。所以做成**运营可开可关的**(第二层
  // translationCfg.enabled 仍是模型级开关),而不是写死。
  //
  // ⚠️ **翻译只碰描述,永远不碰歌词**。这里译的是输入框里的文本(→ instructions);
  // 歌词是左侧独立字段(→ 引擎 input),不经过 translatePrompt。这条不是巧合而是必须:
  // 歌词是要被唱出来的内容,译了就等于换一种语言演唱 —— 同 H3 把台词
  // (`<d>` 内)排除在英文化之外的理由(见 h3Prompt.constants.js 的 SHARED_OUTPUT_RULES:
  // "a translated line makes the character speak the wrong language")。
  const needsEnglishOnly =
    resolvedEngine === MUSIC_ENGINE_MINIMAX_MUSIC3 && translationCfg.enabled;

  // 音乐体验区所有辅助语言模型调用(中译英、draftPlan)共用的运营配置:与各体验区
  // 「AI 优化提示词」同一份(总开关 + 模型 + 分组)。原先让用户在左侧自己挑一个,但这
  // 三者都是「单次非流式打 /pg/chat/completions」的同一类调用,却要两套配置面、两个
  // 模型、两种可用性判断 —— 运营那边配好了优化模型,用户这边还得再选一遍,选错就报
  // 「当前分组下暂无可用语言模型」。
  //
  // 刻意只读 __global,不读 tab 级 promptOptimize:这里判的是「有没有可用的辅助语言
  // 模型」,而 tab 级那个开关管的是「要不要出优化按钮」,两件事。按 tab 判会让运营关掉
  // 优化按钮时连中译英和 draftPlan 一起判没了。
  //(t2m 现已声明 promptOptimize —— 「AI 优化提示词」在 ACE-Step 上走 draftPlan、
  // 在 Music3 上走通用优化,两条实现互斥,界面上只会出现一个按钮,
  // 见 playgroundAdmin.constants.js 该 tab 处的说明。)
  // 系统提示词同理不走运营改写的那份 —— 写词的输出是 JSON、翻译的输出
  // 是一行英文 caption,拿优化提示词那套模板去改都会把解析打挂,故各自固定用内置模板。
  const promptOptimizeGlobal = useMemo(
    () =>
      getPromptOptimizeGlobal(
        parsePlaygroundTabConfig(statusState?.status?.PlaygroundTabConfig),
      ),
    [statusState?.status?.PlaygroundTabConfig],
  );
  // 辅助模型到底能不能用。三处调用都要看它:没配就是没得调。
  const assistModelReady =
    promptOptimizeGlobal.enabled && !!promptOptimizeGlobal.model;

  // 未开总开关 / 没配模型时按钮整体不渲染,与 PromptOptimizeButton 同一条规矩:
  // 与其给一个点了报「未配置」的按钮,不如让它不存在。
  //
  // **Music3 不走这条实现**:draftPlan 产出的是 ACE-Step 那一套(caption +
  // 歌词 + BPM + 调式 + 时长)并回填各控件,而 Music3 只有描述与歌词两个位、没有
  // BPM/调式/时长。给它这个按钮等于回填一堆无处可去的字段。Music3 换成通用的
  // 「AI 优化提示词」(模板已按引擎族换成编曲说明,见 promptOptimize.constants.js),
  // 两个按钮仍是互斥的,不会并排出现两个。
  const draftAvailable =
    resolveTaskType(true) === 't2m' &&
    resolvedEngine !== MUSIC_ENGINE_MINIMAX_MUSIC3 &&
    assistModelReady;

  // 「会自动帮你翻成英文」这句提示只有真翻得动才能说。运营关掉总开关或没配模型时,
  // 模型只认英文这个事实不变、但自动翻译没了,此时要换一句「请直接写英文」——
  // 照旧说「已开启自动翻译」是在承诺一件做不到的事,用户照写中文,发出去才报错。
  const showTranslation = needsEnglishOnly && assistModelReady;
  const englishOnlyNoTranslate = needsEnglishOnly && !assistModelReady;

  // 当前模型的字数上限(0=不限制)。
  const maxChars = useMemo(
    () => getMaxCharsForModel(modelConfig, inputs.model, mode),
    [modelConfig, inputs.model, mode],
  );
  // 当前模型的驱动/参考音大小上限(MB)。
  const refAudioMaxMB = useMemo(
    () => getRefAudioMaxMBForModel(modelConfig, inputs.model, mode),
    [modelConfig, inputs.model, mode],
  );
  // 当前模型的视频大小上限(MB)。
  const videoMaxMB = useMemo(
    () => getVideoMaxMBForModel(modelConfig, inputs.model),
    [modelConfig, inputs.model],
  );

  const musicGroups = useMemo(() => {
    const set = new Set();
    musicModelSet.forEach((model) => {
      (modelGroupsMap.get(model) || []).forEach((g) => set.add(g));
    });
    return set;
  }, [musicModelSet, modelGroupsMap]);

  const loadPricing = useCallback(async () => {
    try {
      const payload = await cachedGet(MUSIC_API_ENDPOINTS.PRICING, {
        config: { skipErrorHandler: true },
      });
      const { success, data } = payload || {};
      if (!success || !Array.isArray(data)) return;
      const groupsMap = new Map();
      data.forEach((item) => {
        if (!item || !item.model_name) return;
        groupsMap.set(item.model_name, item.enable_groups || []);
      });
      setModelGroupsMap(groupsMap);
    } catch (e) {
      // 留空:分组不按 enable_groups 收窄
    }
  }, []);

  const loadGroups = useCallback(async () => {
    try {
      const { success, data } = await cachedGet(
        MUSIC_API_ENDPOINTS.USER_GROUPS,
      );
      if (!success) return;
      const userGroup =
        userState?.user?.group ||
        JSON.parse(localStorage.getItem('user') || '{}')?.group;
      let groupOptions = processGroupsData(data, userGroup);
      const allowAllGroups = musicGroups.has('all');
      if (musicGroups.size > 0 && !allowAllGroups) {
        groupOptions = groupOptions.filter(
          (g) => musicGroups.has(g.value) || g.value === 'auto',
        );
      }
      setGroups(groupOptions);
      setInputs((prev) => {
        if (lockedRef.current) return prev;
        const has = groupOptions.some((g) => g.value === prev.group);
        return has ? prev : { ...prev, group: groupOptions[0]?.value || '' };
      });
    } catch (e) {
      showError(t('加载分组失败'));
    }
  }, [userState, musicGroups, t]);

  const loadModels = useCallback(async () => {
    const requestedGroup = inputs.group;
    try {
      const { success, data } = await getUserModelsCached(requestedGroup);
      if (!success) return;
      if (requestedGroup !== groupRef.current) return;
      let list = Array.isArray(data) ? data : [];
      list = list.filter((m) => musicModelSet.has(m));
      const { modelOptions, selectedModel } = processModelsData(
        list,
        inputs.model,
      );
      setModels(modelOptions);
      setInputs((prev) => {
        if (lockedRef.current) return prev;
        return prev.model === selectedModel
          ? prev
          : { ...prev, model: selectedModel || '' };
      });
    } catch (e) {
      showError(t('加载模型失败'));
    }
  }, [inputs.group, inputs.model, musicModelSet, t]);

  useEffect(() => {
    if (userState?.user) loadPricing();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user]);
  useEffect(() => {
    if (userState?.user) loadGroups();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user, musicGroups]);
  useEffect(() => {
    if (userState?.user) loadModels();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user, inputs.group, musicModelSet]);

  const patchConvMessage = useCallback(
    (convId, msgId, patch) => {
      setConversations((prev) => {
        const next = prev.map((c) =>
          c.id === convId
            ? {
                ...c,
                messages: c.messages.map((m) =>
                  m.id === msgId ? { ...m, ...patch } : m,
                ),
              }
            : c,
        );
        persistConversations(storageKey, next);
        return next;
      });
    },
    [storageKey],
  );

  const turnsUsed = useMemo(
    () => messages.filter((m) => m.role === 'user').length,
    [messages],
  );
  const turnLimitReached = turnsUsed >= MUSIC_CONV_TURN_LIMIT;

  const finishPoll = useCallback(() => {
    if (activePollRef.current?.timer) clearTimeout(activePollRef.current.timer);
    activePollRef.current = null;
    setGenerating(false);
  }, []);

  const pollOnce = useCallback(
    async (convId, msgId, taskId, count) => {
      const active = activePollRef.current;
      if (!active || active.canceled || active.taskId !== taskId) return;
      try {
        const res = await API.get(
          `${MUSIC_API_ENDPOINTS.VIDEO_FETCH}/${encodeURIComponent(taskId)}`,
          { skipErrorHandler: true },
        );
        const data = res.data || {};
        const inner = data.data || {};
        const status = normalizeMusicStatus(data.status || inner.status);
        const progress = parseProgress(
          data.progress != null ? data.progress : inner.progress,
        );

        if (status === MUSIC_STATUS.COMPLETED) {
          patchConvMessage(convId, msgId, {
            status: MUSIC_STATUS.COMPLETED,
            progress: 100,
            musicUrl: buildMusicContentUrl(taskId),
          });
          finishPoll();
          return;
        }
        if (status === MUSIC_STATUS.FAILED) {
          const msg =
            data.error?.message ||
            inner.error?.message ||
            inner.fail_reason ||
            data.fail_reason ||
            t('生成失败');
          patchConvMessage(convId, msgId, {
            status: MUSIC_STATUS.FAILED,
            error: msg,
          });
          showError(msg);
          finishPoll();
          return;
        }
        patchConvMessage(convId, msgId, {
          status: status || MUSIC_STATUS.IN_PROGRESS,
          ...(progress !== undefined ? { progress } : {}),
        });
        if (count >= MUSIC_POLL_MAX_TIMES) {
          patchConvMessage(convId, msgId, { pollTimedOut: true });
          finishPoll();
          return;
        }
      } catch (e) {
        if (count >= MUSIC_POLL_MAX_TIMES) {
          patchConvMessage(convId, msgId, { pollTimedOut: true });
          finishPoll();
          return;
        }
      }
      const cur = activePollRef.current;
      if (!cur || cur.canceled || cur.taskId !== taskId) return;
      cur.timer = setTimeout(
        () => pollOnce(convId, msgId, taskId, count + 1),
        MUSIC_POLL_INTERVAL_MS,
      );
    },
    [patchConvMessage, finishPoll, t],
  );

  const resumePoll = useCallback(
    (convId, msgId, taskId) => {
      if (!taskId) return;
      const active = activePollRef.current;
      if (active && active.taskId === taskId && !active.canceled) return;
      if (active?.timer) clearTimeout(active.timer);
      patchConvMessage(convId, msgId, { pollTimedOut: false });
      activePollRef.current = {
        convId,
        msgId,
        taskId,
        timer: null,
        canceled: false,
      };
      setGenerating(true);
      activePollRef.current.timer = setTimeout(
        () => pollOnce(convId, msgId, taskId, 1),
        MUSIC_POLL_INTERVAL_MS,
      );
    },
    [pollOnce, patchConvMessage],
  );

  // 挂载后为最近一个仍在进行中的任务恢复轮询。
  useEffect(() => {
    if (!userState?.user || activePollRef.current) return;
    let best = null;
    conversationsRef.current.forEach((conv) => {
      (conv.messages || []).forEach((m) => {
        if (m.role !== 'assistant') return;
        const active =
          m.status === MUSIC_STATUS.QUEUED ||
          m.status === MUSIC_STATUS.IN_PROGRESS;
        if (!active) return;
        if (m.taskId) {
          const ts = Number(String(m.id).split('-')[1]) || 0;
          if (!best || ts > best.ts) {
            best = { convId: conv.id, msgId: m.id, taskId: m.taskId, ts };
          }
        } else {
          // 孤儿助手消息:建消息后未拿到 taskId 即中断(如翻译那几秒内刷新页面),
          // 无从恢复。清「翻译中」并置 FAILED(带重试),避免气泡永久转圈。
          patchConvMessage(conv.id, m.id, {
            translating: false,
            status: MUSIC_STATUS.FAILED,
            error: t('翻译失败,请改用英文描述'),
          });
        }
      });
    });
    if (best) resumePoll(best.convId, best.msgId, best.taskId);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userState?.user]);

  const refetch = useCallback(
    (msgId, taskId) => {
      if (currentConvId == null || !taskId) return;
      resumePoll(currentConvId, msgId, taskId);
    },
    [currentConvId, resumePoll],
  );

  // 单次非流式调用语言模型,把中文描述转成英文。失败抛错,交由 generate 走降级。
  //
  // AudioX/SoulX 下线后,音乐页只剩 MiniMax-Music3 需要中译英(ACE-Step 的文本编码器
  // 认中文),所以这里不再按玩法挑模板 —— 原来的 forVideo/forMusic3 两个开关与
  // AudioX 那两份音景模板一并移除。**兜底分支消失本身就是收益**:那份 AudioX 模板会
  // 把 BPM 与 [verse]/[chorus] 删掉、压到 40 词、改写成 AudioCaps 音景,正是 Music3
  // 要的反面,以前只靠一个参数传对才躲开。
  //
  // 用哪个模型不再让用户在左侧挑,而是与「AI 优化提示词」的两条实现共用运营在
  // 「体验区管理 → 通用设置」里配的那一个 —— 三者都是「单次非流式打 /pg/chat/completions
  // 的辅助调用」,没道理一个体验区里摆两套模型配置。
  const translatePrompt = useCallback(
    async (rawText) => {
      const model = promptOptimizeGlobal.model;
      if (!model) throw new Error('no-translation-model');
      // 复用 axios API 实例:自动带 baseURL(分离部署时打到 API 而非前端 origin)与
      // New-API-User 认证头,与 /pg/videos 提交同构。skipErrorHandler 交由本地 catch 降级。
      const res = await API.post(
        MUSIC_TRANSLATE_ENDPOINT,
        {
          model,
          // 分组留空则不下发,后端按用户自己的分组走(同 usePromptOptimize)。
          ...(promptOptimizeGlobal.group
            ? { group: promptOptimizeGlobal.group }
            : {}),
          stream: false,
          messages: [
            {
              role: 'system',
              content: TRANSLATE_SYSTEM_MUSIC3,
            },
            { role: 'user', content: rawText },
          ],
        },
        { skipErrorHandler: true },
      );
      const out = (res?.data?.choices?.[0]?.message?.content || '').trim();
      if (!out) throw new Error('translate-empty');
      return out;
    },
    [promptOptimizeGlobal.model, promptOptimizeGlobal.group],
  );

  // draftPlan = ACE-Step 上「AI 优化提示词」的实现,对应官方 Simple Mode 里
  // 【Create Sample】那一步:据一句话描述拟出
  // caption/歌词/BPM/调式/时长,直接回填到配置面板的各个控件,由用户过目再改。
  //
  // 这一步的意义不只是省事:填了歌词之后提交就不再命中 sample_mode 分支,引擎那边
  // "用 LM 自己推的时长覆盖用户值"的逻辑也就不会触发,时长/BPM 才真正生效。
  //
  // 空输入 / 报错的处理与「AI 优化提示词」(hooks/common/usePromptOptimize.js)对齐:
  // 空输入不是错误而是「还没轮到我」,给 info 指个方向;报错要把上游原文带出来,
  // 只在识别出是分组/渠道配错时加一句「找管理员」的前缀 —— 原来一律吞成
  // 「生成方案失败,请重试或换一个语言模型」,分组配错的人换几个模型也换不出来。
  const draftPlan = useCallback(
    async (rawText) => {
      const text = (rawText || '').trim();
      if (!text) {
        showInfo(
          t(
            '先写一句大概方向，比如「一首深情的中文抒情歌曲」，AI 再帮你补全描述、歌词与曲式',
          ),
        );
        return false;
      }
      if (!draftAvailable) return false;
      setDrafting(true);
      try {
        let out;
        try {
          const res = await API.post(
            MUSIC_TRANSLATE_ENDPOINT,
            {
              model: promptOptimizeGlobal.model,
              // 分组留空则不下发,后端按用户自己的分组走(同 usePromptOptimize)。
              ...(promptOptimizeGlobal.group
                ? { group: promptOptimizeGlobal.group }
                : {}),
              stream: false,
              messages: [
                { role: 'system', content: DRAFT_SYSTEM },
                { role: 'user', content: text },
              ],
            },
            { skipErrorHandler: true },
          );
          out = (res?.data?.choices?.[0]?.message?.content || '').trim();
        } catch (e) {
          const msg = e?.response?.data?.error?.message || e?.message || '';
          showError(
            isPlaygroundConfigIssue(msg)
              ? t('AI 优化提示词暂不可用，请联系管理员') + ' — ' + msg
              : t('生成方案失败:') + msg,
          );
          return false;
        }
        // 模型常自作主张包一层 ```json 围栏,剥掉再解析。
        out = out
          .replace(/^```(?:json)?\s*/i, '')
          .replace(/\s*```$/, '')
          .trim();
        let plan;
        try {
          plan = JSON.parse(out);
        } catch (e) {
          // 与请求失败分开报:这条能行动的建议是「换个模型」,小模型经常吐不出合法
          // JSON;请求失败时给这句反而会把人引到错误方向。
          showError(t('模型没有返回可用的方案,请重试或换一个语言模型'));
          return false;
        }
        setInputs((prev) => {
          const next = { ...prev };
          if (plan.lyrics) next.lyrics = String(plan.lyrics).trim();
          if (Number.isFinite(Number(plan.bpm)) && Number(plan.bpm) > 0)
            next.bpm = String(Math.round(Number(plan.bpm)));
          if (plan.keyScale) next.keyScale = String(plan.keyScale).trim();
          // 收敛到下拉真有的那几档;>120 秒一律落到 120(体验区上限)。
          const duration = snapMusicDuration(plan.duration);
          if (duration) next.duration = duration;
          if (plan.vocalLanguage)
            next.vocalLanguage = String(plan.vocalLanguage).trim();
          return next;
        });
        // caption 单独返回:它要替换输入框里的描述,由调用方决定怎么用。
        return typeof plan.caption === 'string' ? plan.caption.trim() : true;
      } finally {
        setDrafting(false);
      }
    },
    [draftAvailable, promptOptimizeGlobal.model, promptOptimizeGlobal.group, t],
  );

  const generate = useCallback(
    async (prompt) => {
      const text = (prompt || '').trim();
      // t2m/cover/repaint/t2a 需文本;v2*/tv2* 文本可选;svs 无需文本。
      if (needsText && !text) return;
      if (generating) return;

      // 字数上限(0=不限制):按当前模型配置就地拦截(仅对有文本时)。
      if (text) {
        const charLimit = getMaxCharsForModel(modelConfig, inputs.model, mode);
        if (charLimit > 0 && text.length > charLimit) {
          showError(
            t('描述文本超过字数上限 {{max}} 字(当前 {{cur}} 字)', {
              max: charLimit,
              cur: text.length,
            }),
          );
          return;
        }
      }

      let convId = currentConvId;
      let params;
      if (convId == null) {
        if (!inputs.model) {
          showError(t('请先选择一个音乐模型'));
          return;
        }
        if (
          needsAudio &&
          !inputs.srcTaskId &&
          !(inputs.audioData || '').startsWith('data:')
        ) {
          showError(t('请先上传驱动音频'));
          return;
        }
        convId = genId();
        params = pickParams(inputs);
      } else {
        const conv = conversationsRef.current.find((c) => c.id === convId);
        const used = conv
          ? conv.messages.filter((m) => m.role === 'user').length
          : 0;
        if (used >= MUSIC_CONV_TURN_LIMIT) {
          showError(
            t('本轮对话生成次数已达上限（{{count}} 次），请开启新对话', {
              count: MUSIC_CONV_TURN_LIMIT,
            }),
          );
          return;
        }
        params = conv ? pickParams(conv) : pickParams(inputs);
      }

      // Music3 的歌词是必填 —— 它占的是 prompt 位(→ 引擎 input),而门面对
      // task_type=tts 硬校验 prompt 非空(adaptor.go:371「需要合成文本(prompt)」)。
      // 不在这里拦的话用户会拿到一句在音乐页毫无意义的「需要合成文本」。
      // 放在 setGenerating(true) 之前,免得还要回滚状态。
      if (
        resolvedEngine === MUSIC_ENGINE_MINIMAX_MUSIC3 &&
        !(params.lyrics || '').trim()
      ) {
        showError(t('MiniMax-Music3 需要歌词，请在左侧「歌词」框填写'));
        return;
      }

      // 上传的驱动/参考媒体:刷新后 localStorage 已剥离 → 提示重开对话重传。
      let audioDataURL = '';
      if (needsAudio) {
        // 引用上一首生成结果:发 task:<task_id>,由后端 nfsinput/taskref.go 在共享盘上
        // 直读产物(零网络、已做归属与终态校验),不必把音频拉成 base64 再传一遍。
        // 它也不受 localStorage 剥离上传数据的影响,所以放在失效校验之前。
        audioDataURL = params.srcTaskId
          ? `task:${params.srcTaskId}`
          : params.audioData || '';
        if (!params.srcTaskId && !audioDataURL.startsWith('data:')) {
          showError(t('驱动音频已失效,请开启新对话并重新上传'));
          return;
        }
      }

      // ── 中译英(前端编排)──────────────────────────────────────────
      // 命中「玩法需翻译 + 有文本 + 含中文 + 该模型启用译文」时,先调语言模型转英文,
      // 再用英文提交(AudioX 文本编码器仅认英文,中文会塌成 <unk>)。已是英文则不触发。
      // 失败降级(设计 §11):视频生音 → 丢文字改纯视频 v2a;文生音效 → 报错不提交。
      // 时序:消息先建(点发送即可见),翻译放在建消息之后 —— 译文回填 userMsg 展示对照,
      // 助手气泡在拿到 taskId 前先显示「翻译中…」,避免翻译那几秒聊天区空白。
      //
      // 翻译模型没配时在这里就拦掉,不建消息、不提交、不计费。**不能改成「翻不了就把
      // 中文原样发上去」**:AudioX 的文本编码器会把中文塌成 <unk>,那是静默出一段跟
      // 描述无关的音频还照扣额度,比明着报错更糟。
      if (
        needsEnglishOnly &&
        !!text &&
        containsCJK(text) &&
        !assistModelReady
      ) {
        showError(t('当前模型仅支持英文,且未配置翻译模型,请直接用英文描述'));
        return;
      }
      const willTranslate = needsEnglishOnly && !!text && containsCJK(text);

      const reqId = genId();
      const now = new Date().toISOString();
      const userMsg = {
        id: `${reqId}-u`,
        role: 'user',
        // 空文本时用当前玩法的能力标签(视频配音效/视频配乐/歌声合成…),而非硬编码
        // "歌声合成" —— 否则 v2a/v2m 的纯视频任务在历史里被误标成唱歌。
        content: text || `（${capability}）`,
      };
      const asstId = `${reqId}-a`;
      const asstMsg = {
        id: asstId,
        role: 'assistant',
        status: MUSIC_STATUS.QUEUED,
        model: params.model,
        prompt: text,
        progress: 0,
        taskId: null,
        musicUrl: null,
        // 翻译中标志:渲染层据此优先显示「翻译中…」;译文回填/降级/失败后一律置 false。
        translating: willTranslate,
      };

      setConversations((prev) => {
        const idx = prev.findIndex((c) => c.id === convId);
        let next;
        if (idx === -1) {
          next = [
            {
              id: convId,
              ...params,
              title: text || capability,
              createdAt: now,
              updatedAt: now,
              messages: [userMsg, asstMsg],
            },
            ...prev,
          ];
        } else {
          const conv = {
            ...prev[idx],
            updatedAt: now,
            messages: [...prev[idx].messages, userMsg, asstMsg],
          };
          next = [conv, ...prev.filter((_, i) => i !== idx)];
        }
        next = next.slice(0, MUSIC_HISTORY_LIMIT);
        persistConversations(storageKey, next);
        return next;
      });
      if (currentConvId == null) setCurrentConvId(convId);
      setGenerating(true);

      // 建消息之后再翻译:成功回填译文小字(userMsg)并清「翻译中」;失败按玩法降级。
      let effectiveText = text;
      if (willTranslate) {
        try {
          effectiveText = await translatePrompt(text);
          patchConvMessage(convId, userMsg.id, {
            translatedText: effectiveText,
          });
          patchConvMessage(convId, asstId, { translating: false });
        } catch (e) {
          {
            // 翻译失败即置 FAILED(带重试),不提交:描述是 Music3 的必填项,
            // 拿未翻译的中文硬发出去只会得到一段不知所云的编曲。
            // (原先这里还有一条"视频生音降级"分支,随 AudioX 下线一并移除。)
            patchConvMessage(convId, asstId, {
              status: MUSIC_STATUS.FAILED,
              translating: false,
              error: t('翻译失败,请改用英文描述'),
            });
            showError(t('翻译失败,请改用英文描述'));
            setGenerating(false);
            return;
          }
        }
      }

      // 解析 task_type:视频生音按是否带(译后)文本分支到 tv2a/v2a;其余与文本无关。
      //
      // **MiniMax-Music3 例外:发 tts 而不是 t2m**。task_type 在门面那边不只是标签,
      // 它唯一决定打引擎的哪条路由(gpustack routes/videos.py 的 _engine_kind):
      // t2m → kind "music" → POST /v1/tasks/music/,那是 ACE-Step-1.5 的路由,
      // vLLM-Omni 根本没有(它只有 audio/audiogen/image/video 四条),发过去是 405
      // ——405 而非 404 是因为带尾斜杠的请求被重定向后落到 DELETE /v1/tasks/{task_id}
      // 上(task_id="music"),路径匹配、方法不匹配。
      //
      // tts → kind "audio" → POST /v1/tasks/audio/,用的是
      // AudioTaskRequest(OpenAICreateSpeechRequest) + Omnispeech,正是 Music3 冒烟
      // 走的 /v1/audio/speech 的异步孪生,instructions 原生就在那个 schema 里。
      // 顺带把输出扩展名也修对了:kind "audio" 出 .wav(Music3 出的就是 wav),
      // kind "music" 出 .mp3,本来就是错的。
      //
      // 名字叫 tts 而实为音乐,确实别扭 —— 但门面的 kind 只认 task_type,不认引擎族,
      // 要让 t2m 也能指向 vLLM-Omni 得改 gpustack 并等 50 台节点全量升级。等那边铺开
      // 后这里就该摘掉,改回 t2m。
      // 已核过副作用:TTS 专属的输入物化(voice/ref_audio 等)只对 body 里存在的键生效,
      // Music3 不发音频输入,是空转;adaptor 里 taskType=="tts" 的 extra_params 折叠
      // 只搬 emo_* 那几个键,Music3 也没有。
      const resolvedTaskType =
        resolvedEngine === MUSIC_ENGINE_MINIMAX_MUSIC3
          ? 'tts'
          : resolveTaskType(effectiveText.length > 0);
      // 占位符仅用于 svs(歌声合成引擎需非空 input,文本仅占位);v2a 是纯视频输入,
      // 后端明确允许空 prompt —— 绝不能塞占位,否则会拿"歌声合成"去条件化 AudioX。
      //
      // **Music3 的 prompt 位放歌词,不是描述**。门面把 prompt 原样写成
      // body["prompt"],而引擎 AudioTaskRequest.input 的 alias 就含 prompt ——
      // 也就是 prompt → input。Music3 的 input 是歌词、描述走 instructions
      // (冒烟即如此:input=歌词 / instructions=曲风编配)。与 ACE-Step 相反,后者的
      // 描述才是 prompt(caption)、歌词是并列的 lyrics 字段。发反了不报错,只会把
      // 曲风描述当歌词唱出来。
      const promptField =
        resolvedEngine === MUSIC_ENGINE_MINIMAX_MUSIC3
          ? (params.lyrics || '').trim()
          : effectiveText || (resolvedTaskType === 'svs' ? t('歌声合成') : '');

      try {
        // gpustackplus 门面契约:task_type + 输入(音频/视频)+ 标量参数经 metadata 透传
        // (adaptor 把上传物化 NFS → input_refs → 引擎)。
        const metadata = { task_type: resolvedTaskType };

        if (resolvedEngine === MUSIC_ENGINE_MINIMAX_MUSIC3) {
          // ── MiniMax-Music3:曲风描述(必填)+ 歌词(必填,已放在 prompt 位)──
          //
          // 引擎硬校验 instructions:不传直接 400
          // ("MiniMax Music 3 requires 'instructions' describing the music
          //   (genre, instrumentation, tempo, mood); it is what decides the
          //   arrangement")。这里把用户填的描述文本放 instructions。
          //
          // 歌词**不再走 metadata.lyrics** —— 它已经是上面的 promptField(→ 引擎
          // input)。lyrics 不是 AudioTaskRequest 的字段,再发一份只是噪声,还会让
          // 后来者以为引擎读的是它。
          //
          // 用 effectiveText 而不是 text:运营给这个模型开了中译英时,要发的是译后的
          // 英文 caption。歌词不受影响 —— 它走的是上面的 promptField,取自
          // params.lyrics,从不经过翻译。
          //
          // 描述为空时不下发空串:让引擎报它自己那句可自助的错,好过我们塞一个
          // 空 instructions 让它生成一段无从解释的编曲。
          if (effectiveText.trim())
            metadata.instructions = effectiveText.trim();
        } else if (resolvedEngine === 'acestep') {
          // ── ACE-Step:歌词/时长/驱动音频/BPM/演唱语言 ──
          const lyrics = (params.lyrics || '').trim();
          const dur = parseFloat(params.duration);
          if (Number.isFinite(dur) && dur > 0) metadata.audio_duration = dur;
          if (needsAudio && audioMetaKey) metadata[audioMetaKey] = audioDataURL;

          // thinking=true 让 5Hz LM 先出音频语义码再喂 DiT(llm_dit 两阶段);默认的 false
          // 只跑单阶段 dit。官方把它列为质量第一条建议(GRADIO_GUIDE.md「Use thinking mode」)。
          metadata.thinking = true;
          // 引擎 batch_size 默认 2,但 ACE-Step 侧的门面适配层只把 raw_audio_paths[0]
          // 搬到 save_result_path(tasks_facade_service.materialize_output),第二首直接丢。
          // 显式压到 1:同样拿一首,少烧一半算力。要真拿多首得改 ACE-Step/gpustack/new-api
          // 三层的单产物契约,不在本次范围——这里先并发提交多个任务代替。
          metadata.batch_size = 1;

          // t2m 且未填歌词 → 额外开启 sample 模式:引擎按描述用 LM 自动生成 caption+歌词。
          // prompt 仍保持=描述文本 —— 既满足门面「prompt 必填」校验,也让不认 sample_mode
          // 的路径能靠 prompt + LM 补词兜底。其余情况(已填歌词 或 cover/repaint):描述作
          // 为 caption(prompt),歌词直接透传。
          if (resolvedTaskType === 't2m' && !lyrics) {
            metadata.sample_mode = true;
            metadata.sample_query = text;
          } else if (lyrics) {
            metadata.lyrics = lyrics;
          }

          const bpm = parseInt(params.bpm, 10);
          if (Number.isFinite(bpm) && bpm > 0) metadata.bpm = bpm;
          const lang = (params.vocalLanguage || '').trim();
          if (lang) metadata.vocal_language = lang;
          const keyScale = (params.keyScale || '').trim();
          if (keyScale) metadata.key_scale = keyScale;

          // 改编:保留原曲结构的程度。官方标为 cover 的 Key parameter,原先没暴露,
          // 用户只能吃引擎默认的 1.0(最大保留),等于"改编"改不动。
          if (resolvedTaskType === 'cover') {
            const cs = parseFloat(params.coverStrength);
            if (Number.isFinite(cs) && cs >= 0 && cs <= 1)
              metadata.audio_cover_strength = cs;
          }

          // 重绘:区间 + 力度。区间不填时引擎默认 start=0/end=-1 → 全曲重绘,那就跟改编
          // 没区别了 —— repaint 的价值就在只改一段,所以这里如实透传用户填的区间。
          if (resolvedTaskType === 'repaint') {
            const rs = parseFloat(params.repaintStart);
            const re = parseFloat(params.repaintEnd);
            if (Number.isFinite(rs) && rs >= 0) metadata.repainting_start = rs;
            if (Number.isFinite(re) && re > 0) metadata.repainting_end = re;
            const rm = (params.repaintMode || '').trim();
            if (rm) metadata.repaint_mode = rm;
            // repaint_strength 仅 balanced 模式生效(引擎侧语义),其余模式不下发。
            if (rm === 'balanced') {
              const rst = parseFloat(params.repaintStrength);
              if (Number.isFinite(rst) && rst >= 0 && rst <= 1)
                metadata.repaint_strength = rst;
            }
          }

          const gs = parseFloat(params.guidanceScale);
          if (Number.isFinite(gs) && gs > 0) metadata.guidance_scale = gs;
          const steps = parseInt(params.inferenceSteps, 10);
          if (Number.isFinite(steps) && steps > 0)
            metadata.inference_steps = steps;
          const seedStr = String(params.seed ?? '').trim();
          if (seedStr !== '') {
            metadata.seed = seedStr;
            metadata.use_random_seed = false;
          }
        }

        const body = {
          model: params.model,
          group: params.group,
          prompt: promptField,
          metadata,
        };
        const res = await API.post(
          MUSIC_API_ENDPOINTS.VIDEO_GENERATIONS,
          body,
          {
            skipErrorHandler: true,
          },
        );
        const data = res.data || {};
        const inner = data.data || {};
        const taskId = data.id || data.task_id || inner.task_id || inner.id;
        if (!taskId) throw new Error(t('提交任务失败'));
        const status = normalizeMusicStatus(data.status || inner.status);
        if (status === MUSIC_STATUS.FAILED) {
          const msg =
            data.error?.message ||
            inner.error?.message ||
            inner.fail_reason ||
            data.fail_reason ||
            t('生成失败');
          patchConvMessage(convId, asstId, {
            status: MUSIC_STATUS.FAILED,
            error: msg,
          });
          showError(msg);
          setGenerating(false);
          return;
        }
        patchConvMessage(convId, asstId, { taskId, status, progress: 0 });
        activePollRef.current = {
          convId,
          msgId: asstId,
          taskId,
          timer: null,
          canceled: false,
        };
        activePollRef.current.timer = setTimeout(
          () => pollOnce(convId, asstId, taskId, 1),
          MUSIC_POLL_INTERVAL_MS,
        );
      } catch (error) {
        const msg = extractApiErrMsg(error, t('生成失败'));
        patchConvMessage(convId, asstId, {
          status: MUSIC_STATUS.FAILED,
          error: msg,
        });
        showError(msg);
        setGenerating(false);
      }
    },
    [
      currentConvId,
      inputs,
      generating,
      engine,
      needsAudio,
      audioMetaKey,
      needsText,
      needsEnglishOnly,
      assistModelReady,
      translatePrompt,
      resolveTaskType,
      storageKey,
      patchConvMessage,
      pollOnce,
      modelConfig,
      t,
    ],
  );

  const regenerate = useCallback((prompt) => generate(prompt), [generate]);

  const newConversation = useCallback(() => {
    setCurrentConvId(null);
  }, []);

  const clearHistory = useCallback(() => {
    if (activePollRef.current) activePollRef.current.canceled = true;
    finishPoll();
    setConversations([]);
    persistConversations(storageKey, []);
    setCurrentConvId(null);
  }, [finishPoll, storageKey]);

  const deleteHistoryItem = useCallback(
    (id) => {
      const active = activePollRef.current;
      if (active && active.convId === id) {
        active.canceled = true;
        finishPoll();
      }
      setConversations((prev) => {
        const next = prev.filter((c) => c.id !== id);
        persistConversations(storageKey, next);
        return next;
      });
      setCurrentConvId((cur) => (cur === id ? null : cur));
    },
    [finishPoll, storageKey],
  );

  const openHistoryItem = useCallback(
    (conv) => {
      setCurrentConvId(conv.id);
      setInputs((prev) => {
        const next = { ...prev };
        PARAM_FIELDS.forEach((f) => {
          if (conv[f] != null) next[f] = conv[f];
        });
        return next;
      });
      const assts = (conv.messages || []).filter((m) => m.role === 'assistant');
      const last = assts[assts.length - 1];
      if (
        last?.taskId &&
        (last.status === MUSIC_STATUS.QUEUED ||
          last.status === MUSIC_STATUS.IN_PROGRESS)
      ) {
        resumePoll(conv.id, last.id, last.taskId);
      }
    },
    [resumePoll],
  );

  useEffect(() => {
    return () => {
      if (activePollRef.current?.timer)
        clearTimeout(activePollRef.current.timer);
      activePollRef.current = null;
    };
  }, []);

  // 缺必填上传 → 发送置灰。引用上一首生成结果(srcTaskId)时不需要上传,故同样算已备齐。
  const missingRequiredAudio =
    !locked &&
    needsAudio &&
    !inputs.srcTaskId &&
    !(inputs.audioData || '').startsWith('data:');

  return {
    inputs,
    handleInputChange,
    applyExample,
    groups,
    models,
    messages,
    conversations,
    currentConvId,
    generating,
    locked,
    turnLimitReached,
    missingRequiredAudio,
    // 给 UI 的是 **resolvedEngine**(模型声明优先、未声明才回退 tab 默认),不是
    // modeDef.engine。tab 默认是硬编码的(「文生音乐」恒 acestep),而同一个 tab 挂着
    // 多个引擎的模型 —— 用 tab 默认的话,选了 MiniMax-Music3 时面板照样按 ACE-Step
    // 渲染:时长/演唱语言/BPM 这些它根本不认的控件全在,而且拖了不报错、只是无效。
    engine: resolvedEngine,
    needsAudio,
    needsText,
    showTranslation,
    englishOnlyNoTranslate,
    draftAvailable,
    drafting,
    draftPlan,
    maxChars,
    refAudioMaxMB,
    videoMaxMB,
    generate,
    regenerate,
    refetch,
    newConversation,
    clearHistory,
    deleteHistoryItem,
    openHistoryItem,
  };
};
