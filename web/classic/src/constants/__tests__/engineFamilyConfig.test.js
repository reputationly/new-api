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
  VIDEO_ENGINE_LTX25,
} from '../playgroundAdmin.constants';
import { parseVideoModelConfig } from '../videoPlayground.constants';
import { defaultPromptGuide } from '../promptGuide.constants';
import {
  parseMusicModelConfig,
  getEngineForMusicModel,
  musicExamplesForMode,
} from '../musicPlayground.constants';
import { defaultOptimizeSystemPrompt } from '../promptOptimize.constants';
import { TRANSLATE_SYSTEM_MUSIC3 } from '../../hooks/musicPlayground/useMusicGeneration';

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
    expect(musicExamplesForMode('cover', MUSIC_ENGINE_MINIMAX_MUSIC3)).toEqual(
      musicExamplesForMode('cover', 'acestep'),
    );
  });
});

// 中译英模板。AudioX/SoulX 下线后音乐页只剩 ACE-Step(认中文,不翻译)与 Music3,
// translatePrompt 因此只有一份模板、没有兜底分支 —— 原先那份 AudioX 音景模板
// (AudioCaps 风格、≤40 词、明令去掉 BPM 与 [verse]/[chorus])已随玩法删除。
// 这一组守的是内容契约:Music3 的模板不能被写成音效那一路,否则引擎收到的是一段
// 被削平的 caption,不报错、只是编曲质量默默变差。
describe('Music3 中译英模板', () => {
  it('保留结构化字段,且要求忠实、不许替用户编', () => {
    for (const key of ['BPM', 'Key', 'Vocals', 'Arrangement']) {
      expect(TRANSLATE_SYSTEM_MUSIC3, `缺 ${key}`).toContain(key);
    }
    // 自动跑的一步必须忠实:不能像「AI 优化提示词」那样扩写
    expect(TRANSLATE_SYSTEM_MUSIC3).toContain('Do NOT invent');
    // 歌词兜底:误贴进描述框时也不能被译进去
    expect(TRANSLATE_SYSTEM_MUSIC3).toContain('never translate lyrics');
  });

  it('没有被写成音效模板那一路', () => {
    // 这三条是已下线的 AudioX 模板的特征,落到 Music3 上正好把它要的东西删掉
    expect(TRANSLATE_SYSTEM_MUSIC3).not.toContain('no music notation, no BPM');
    expect(TRANSLATE_SYSTEM_MUSIC3).not.toContain('<= 40 words');
    expect(TRANSLATE_SYSTEM_MUSIC3).not.toContain('AudioCaps');
  });
});

// LTX-2.5 是视频页的第三个引擎族(wan 系 / MiniMax H3 / LTX-2.5)。它的接入链路与前两者
// 同构,但有一处**只在前端断掉、后端完全看不出来**的坑,这一组就是守它的:
//
//   后端 ltx25.go 那套整形(秒→帧换算、seconds 剥离、尺寸与显存包络准入)只在
//   engine === 'ltx-2.5' 时才跑,而 engine 的唯一来源是运营在「引擎族」下拉里选的值。
//   下拉里没有这一项 = 那套代码一次都不会被触发,而症状是每个带时长的请求 500
//   (整数秒 × 24 恒 ≡ 0 mod 8,取不到 8k+1 需要的余数 1)。
describe('LTX-2.5 引擎族的接入面', () => {
  it('运营能在「引擎族」下拉里选到 LTX-2.5', () => {
    const engine = PLAYGROUND_MODEL_LEVEL_FIELDS.VideoModelConfig.find(
      (f) => f.key === 'engine',
    );
    expect(
      engine.options.some((o) => o.value === VIDEO_ENGINE_LTX25),
      '下拉里没有 LTX-2.5，后端整形永远不会被触发',
    ).toBe(true);
  });

  it('取值与后端 common.VideoEngineLTX25 一致', () => {
    // 后端比较前 lower+trim，这里必须是已经规范化的字面量
    expect(VIDEO_ENGINE_LTX25).toBe('ltx-2.5');
    expect(VIDEO_ENGINE_LTX25).toBe(VIDEO_ENGINE_LTX25.trim().toLowerCase());
  });

  it('engine 声明能在 parse 里活下来（白名单式重建的常见漏法）', () => {
    const parsed = parseVideoModelConfig(
      JSON.stringify({ models: { 'ltx2.5': { engine: '  LTX-2.5 ' } } }),
    );
    expect(parsed.models['ltx2.5'].engine).toBe(VIDEO_ENGINE_LTX25);
  });

  // 像素串档位必须原样活下来:LTX 的 sizes 配的是 "960x544" 这类精确画布(引擎认
  // OpenAI 风格的 WIDTHxHEIGHT),被规范化成档位词或被丢掉都会让它拿不到画布。
  it('像素串尺寸档位在 parse 里保持原样', () => {
    const parsed = parseVideoModelConfig({
      models: {
        'ltx2.5': {
          engine: VIDEO_ENGINE_LTX25,
          tabs: { text2video: { sizes: ['960x544', '1248X704', '704x704'] } },
        },
      },
    });
    expect(parsed.models['ltx2.5'].tabs.text2video.sizes).toEqual([
      '960x544',
      '1248x704',
      '704x704',
    ]);
  });

  // 提示词模板按引擎族换整份。模型卡明确「在长段单段落视听描述上训练,短提示词会明显
  // 劣化」,而通用模板要求的是一句话镜头描述 —— 方向相反,套用等于越优化越差。
  // 且它是音视频联合生成,通用模板一个字没提声音。
  it('AI 优化模板:LTX 单独一份，要求长段落且必须写声音', () => {
    const ltx = defaultOptimizeSystemPrompt('text2video', VIDEO_ENGINE_LTX25);
    const generic = defaultOptimizeSystemPrompt('text2video', '');
    expect(ltx).not.toBe(generic);
    // 段落形状是硬约束，不是风格偏好
    expect(ltx).toContain('ONE long');
    expect(ltx.toLowerCase()).toContain('never output bullet');
    // 音轨是这个模型的一半能力，通用模板里一个字都没有
    expect(ltx).toContain('Audio is not optional');
    expect(generic).not.toContain('Audio is not optional');
    // 首帧玩法同样有专版，且不能与文生视频那份混用
    expect(defaultOptimizeSystemPrompt('flf2v', VIDEO_ENGINE_LTX25)).not.toBe(
      ltx,
    );
    // 没声明引擎族时维持原行为
    expect(defaultOptimizeSystemPrompt('text2video', '')).toBe(generic);
  });

  // 提示词建议(用户看的那个问号)同样按引擎族换,且必须点明两件用户自助不了的事:
  // 要写整段、只吃首帧。
  it('提示词建议:LTX 单独一份，点明整段写法与「只吃首帧」', () => {
    const t2v = defaultPromptGuide('text2video', VIDEO_ENGINE_LTX25);
    expect(t2v).not.toBe(defaultPromptGuide('text2video', ''));
    expect(t2v).toContain('一整段');
    const kf = defaultPromptGuide('flf2v', VIDEO_ENGINE_LTX25);
    // 关键帧 tab 承载多种玩法，LTX 只支持首帧 —— 不写清楚用户会去找尾帧上传框
    expect(kf).toContain('只吃首帧');
    // LTX 没有专版建议的玩法要回落通用版，而不是变成空白
    expect(defaultPromptGuide('vace', VIDEO_ENGINE_LTX25)).toBe(
      defaultPromptGuide('vace', ''),
    );
  });
});
