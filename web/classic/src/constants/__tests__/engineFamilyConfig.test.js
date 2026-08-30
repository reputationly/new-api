import { describe, it, expect } from 'vitest';
import {
  parseAudioModelConfig,
  getEngineForAudioModel,
  AUDIO_ENGINE_INDEXTTS25,
} from '../audioPlayground.constants';
import {
  recomputeModelLevel,
  PLAYGROUND_MODEL_LEVEL_FIELDS,
  MUSIC_ENGINE_MINIMAX_MUSIC3,
} from '../playgroundAdmin.constants';
import {
  parseMusicModelConfig,
  getEngineForMusicModel,
  musicExamplesForMode,
} from '../musicPlayground.constants';
import { defaultOptimizeSystemPrompt } from '../promptOptimize.constants';
import {
  TRANSLATE_SYSTEM_MUSIC3,
  TRANSLATE_SYSTEM_BASE,
} from '../../hooks/musicPlayground/useMusicGeneration';

// 语音模型配置里的 engine 声明。
//
// 这条链路上每一环失效都**不会报错**，只会让 IndexTTS-2.5 的独有能力(语速 /
// 语种 / 文本归一化)整体消失，所以必须由测试守住而不是靠 review:
//
//   parseAudioModelConfig 是**白名单式重建**，且管理页草稿正是用它水合
//   (usePlaygroundAdminDraft 的 AudioModelConfig.toDraft)。保存路径上的
//   recomputeModelLevel 只是 {...model} 整体展开，不会补回 parse 丢掉的键 ——
//   于是「parse 漏一个字段」= 「运营每次打开语音配置页保存就把它删一次」。
//
//   前端后果:getEngineForAudioModel 恒返回空 → isIndexTTS25 恒 false → 2.5 的
//   控件不渲染、metadata 分发分支不执行。
//   后端后果:AudioEngineFamilyForModel 读的是原始 JSON，本来不受前端影响，但
//   声明被管理页抹掉之后，lang / text_normalization 折进 extra_params 也一起失效。

describe('parseAudioModelConfig 的 engine 声明', () => {
  it('保住 engine，并按后端口径 lower+trim', () => {
    const parsed = parseAudioModelConfig(
      JSON.stringify({
        models: {
          'tts-2.5': { engine: '  IndexTTS2.5 ', capabilities: ['语音合成'] },
        },
      }),
    );
    expect(parsed.models['tts-2.5'].engine).toBe(AUDIO_ENGINE_INDEXTTS25);
  });

  it('未声明 engine 的模型落空串，不是 undefined', () => {
    const parsed = parseAudioModelConfig({
      models: { 'tts-2': { capabilities: ['语音合成'] } },
    });
    expect(parsed.models['tts-2'].engine).toBe('');
  });

  it('对象与字符串两种入参等价', () => {
    const raw = { models: { 'tts-2.5': { engine: AUDIO_ENGINE_INDEXTTS25 } } };
    expect(parseAudioModelConfig(raw).models['tts-2.5'].engine).toBe(
      parseAudioModelConfig(JSON.stringify(raw)).models['tts-2.5'].engine,
    );
  });
});

describe('getEngineForAudioModel', () => {
  const parsed = parseAudioModelConfig({
    models: {
      'tts-2.5': { engine: AUDIO_ENGINE_INDEXTTS25 },
      'tts-2': {},
    },
  });

  // 判据是配置声明而非模型名 substring:前端拿对外模型名、后端拿渠道重定向后的
  // 上游名，靠名字判两边必然分叉。所以名字里带 2.5 也不算数。
  it('只认声明，不认模型名', () => {
    expect(getEngineForAudioModel(parsed, 'tts-2.5')).toBe(
      AUDIO_ENGINE_INDEXTTS25,
    );
    expect(getEngineForAudioModel(parsed, 'tts-2')).toBe('');
  });

  it('模型不存在 / 配置为空都返回空串，不抛', () => {
    expect(getEngineForAudioModel(parsed, '不存在的模型')).toBe('');
    expect(getEngineForAudioModel(null, 'tts-2.5')).toBe('');
    expect(getEngineForAudioModel(undefined, undefined)).toBe('');
  });
});

// 这条是真正的回归守卫:模拟运营「打开语音配置页 → 什么都不改 → 保存」。
// engine 必须原样活下来。之前它活不下来，而且没有任何报错。
describe('管理页保存往返', () => {
  it('原样保存不会抹掉 engine', () => {
    const stored = {
      models: {
        'tts-2.5': {
          engine: AUDIO_ENGINE_INDEXTTS25,
          capabilities: ['语音合成'],
          maxChars: 500,
          tabs: { tts: {} },
        },
      },
    };
    const draft = parseAudioModelConfig(JSON.stringify(stored));
    const saved = recomputeModelLevel(
      'AudioModelConfig',
      draft.models['tts-2.5'],
    );
    expect(saved.engine).toBe(AUDIO_ENGINE_INDEXTTS25);

    // 再水合一次:多存几轮也不该衰减。
    const reparsed = parseAudioModelConfig({
      models: { 'tts-2.5': { ...saved, tabs: draft.models['tts-2.5'].tabs } },
    });
    expect(getEngineForAudioModel(reparsed, 'tts-2.5')).toBe(
      AUDIO_ENGINE_INDEXTTS25,
    );
  });
});

// 音乐侧同一条链路。parse 本来就保住了 engine，但管理页一直没有录入字段，
// 等于这个声明只能靠手改 option —— MiniMax-Music3 不声明就走 ACE-Step 分支，
// 拿不到 instructions，引擎直接 400。
describe('音乐引擎族', () => {
  it('parse 保住 engine 且管理页保存不抹掉', () => {
    const draft = parseMusicModelConfig(
      JSON.stringify({
        models: {
          'music-3': { engine: ' MiniMax-Music3 ', tabs: { text2music: {} } },
        },
      }),
    );
    expect(draft.models['music-3'].engine).toBe(MUSIC_ENGINE_MINIMAX_MUSIC3);

    const saved = recomputeModelLevel(
      'MusicModelConfig',
      draft.models['music-3'],
    );
    expect(saved.engine).toBe(MUSIC_ENGINE_MINIMAX_MUSIC3);
    expect(
      getEngineForMusicModel(
        parseMusicModelConfig({ models: { 'music-3': saved } }),
        'music-3',
      ),
    ).toBe(MUSIC_ENGINE_MINIMAX_MUSIC3);
  });
});

// 声明存得住还不够 —— 运营得能在管理页选到它。这两条挂了就等于功能只能靠手改 option。
describe('管理页引擎族录入入口', () => {
  it('语音与音乐都有 engine 字段', () => {
    for (const key of ['AudioModelConfig', 'MusicModelConfig']) {
      const fields = PLAYGROUND_MODEL_LEVEL_FIELDS[key] || [];
      const engine = fields.find((f) => f.key === 'engine');
      expect(engine, `${key} 缺引擎族录入字段`).toBeTruthy();
      expect(engine.type).toBe('select');
      // 必须有「默认」空值项，否则运营选了就取消不掉
      expect(engine.options.some((o) => o.value === '')).toBe(true);
    }
  });
});

// 文生音乐这个 tab 同时挂 ACE-Step 与 MiniMax-Music3,而两者的描述位语义相反:
// ACE-Step 是 caption(与歌词并列),Music3 是官方 Structured Caption 编曲说明
// (歌词另走引擎 input)。按 tab 给一份文案/模板,对其中一个必然是错的,而且不报错 ——
// 只是产出一段用不上的东西。这一组锁的就是"按引擎族分流"这件事本身。
describe('文生音乐按引擎族分流', () => {
  it('优化提示词模板:Music3 走 Structured Caption,ACE-Step 不受影响', () => {
    const music3 = defaultOptimizeSystemPrompt(
      't2m',
      MUSIC_ENGINE_MINIMAX_MUSIC3,
    );
    const acestep = defaultOptimizeSystemPrompt('t2m', 'acestep');
    expect(music3).not.toBe(acestep);
    // 官方 README 点名的三段与关键字段,漏了就不是那套规范了
    for (const key of [
      'Global Metadata',
      'Vocal Details',
      'Arrangement',
      'BPM',
    ]) {
      expect(music3, `Music3 模板缺 ${key}`).toContain(key);
    }
    // 歌词是独立输入,模板必须明确不许写词
    expect(music3.toLowerCase()).toContain('never write');
    // 没声明引擎族时维持原行为(t2m 无专用模板 → 通用兜底)
    expect(defaultOptimizeSystemPrompt('t2m', '')).toBe(acestep);
  });

  it('一键示例:Music3 单独一套,且每条都带歌词', () => {
    const music3 = musicExamplesForMode('t2m', MUSIC_ENGINE_MINIMAX_MUSIC3);
    const acestep = musicExamplesForMode('t2m', 'acestep');
    expect(music3).not.toEqual(acestep);
    expect(music3.length).toBeGreaterThan(0);
    for (const ex of music3) {
      // 歌词是引擎的 input,门面对空 prompt 直接 400 —— 示例点了必须能提交
      expect((ex.params?.lyrics || '').trim(), `${ex.label} 缺歌词`).not.toBe(
        '',
      );
      // 段落标签按官方要求独占一行
      expect(ex.params.lyrics, `${ex.label} 的段落标签未独占一行`).toMatch(
        /^\[[A-Za-z-]+\]$/m,
      );
      // Music3 没有 vocalLanguage 这个参数,带上只是在 inputs 里留死值
      expect(
        ex.params.vocalLanguage,
        `${ex.label} 不该带 vocalLanguage`,
      ).toBeUndefined();
      // 描述与歌词不能自相矛盾:歌词必填、写了就一定会被唱出来,所以描述里不能声称
      // 没有人声。曾经有一条示例描述写 "instrumental, no lead vocal" 却给了整段歌词,
      // 等于一边告诉引擎没人声、一边给它词唱。
      expect(
        ex.prompt.toLowerCase(),
        `${ex.label} 的描述说没有人声,却给了歌词`,
      ).not.toMatch(/instrumental,\s*no lead vocal|\bno vocals?\b/);
      // 反过来也要有交代:歌词一定会被唱,描述里必须说清谁在唱
      expect(ex.prompt, `${ex.label} 的描述缺 Vocals 一节`).toContain(
        'Vocals:',
      );
    }
    // 其他 mode 不受引擎参数影响
    expect(musicExamplesForMode('t2a', MUSIC_ENGINE_MINIMAX_MUSIC3)).toEqual(
      musicExamplesForMode('t2a', 'audiox'),
    );
  });
});

// 中译英模板也必须按引擎族分:AudioX 那份是给音景写的(AudioCaps 风格、≤40 词、
// 明令去掉 BPM 与 [verse]/[chorus]),而 Music3 的 instructions 要的正好是它禁掉的
// 东西。拿错模板不报错,只是引擎收到一段被削平的 caption、编曲质量默默变差 ——
// 这条路在"用户写中文描述、不点优化直接提交"时会走到,是默认路径不是边角。
describe('中译英模板按引擎族分', () => {
  it('Music3 用自己的模板,不复用 AudioX 那份', () => {
    expect(TRANSLATE_SYSTEM_MUSIC3).not.toBe(TRANSLATE_SYSTEM_BASE);
  });

  it('AudioX 模板的禁令没有被带进 Music3 模板', () => {
    // 这三条是 AudioX 专属约束,落到 Music3 上正好把它要的东西删掉。
    // 比对禁令原句而非孤立词:Music3 模板里有「用户没说速度就别编」,
    // 那句话本身也会含 BPM 这个词,按词判会误伤。
    const BAN_NOTATION = 'no music notation, no BPM';
    const BAN_LENGTH = '<= 40 words';
    expect(TRANSLATE_SYSTEM_BASE).toContain(BAN_NOTATION);
    expect(TRANSLATE_SYSTEM_BASE).toContain(BAN_LENGTH);
    expect(TRANSLATE_SYSTEM_MUSIC3).not.toContain(BAN_NOTATION);
    expect(TRANSLATE_SYSTEM_MUSIC3).not.toContain(BAN_LENGTH);
    expect(TRANSLATE_SYSTEM_MUSIC3).not.toContain('AudioCaps');
  });

  it('Music3 模板保留结构化字段,且要求忠实、不许替用户编', () => {
    for (const key of ['BPM', 'Key', 'Vocals', 'Arrangement']) {
      expect(TRANSLATE_SYSTEM_MUSIC3, `缺 ${key}`).toContain(key);
    }
    // 自动跑的一步必须忠实:不能像"优化提示词"那样扩写
    expect(TRANSLATE_SYSTEM_MUSIC3).toContain('Do NOT invent');
    // 歌词兜底:误贴进描述框时也不能被译进去
    expect(TRANSLATE_SYSTEM_MUSIC3).toContain('never translate lyrics');
  });
});
