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
} from '../musicPlayground.constants';

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
