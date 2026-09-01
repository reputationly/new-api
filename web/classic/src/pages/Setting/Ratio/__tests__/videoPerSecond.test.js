import { describe, it, expect } from 'vitest';
import {
  VIDEO_MODE_TOKEN,
  VIDEO_MODE_PER_CALL,
  VIDEO_MODE_PER_SECOND,
  VIDEO_COL_PER_SECOND,
  videoCellKey,
  parseVideoMatrixEntry,
  serializeVideoMatrix,
  videoResolutionOptionsForModel,
  VIDEO_DEFAULT_RESOLUTIONS,
} from '../hooks/useModelPricingEditorState';

/**
 * per_second 模式的编辑器序列化（docs/video-billing-matrix-design.md）。
 *
 * 后端存的是一维表 { 分辨率: $/秒 }，编辑器 state 是二维 cells[分辨率||列]。
 * 用一个虚拟列名把一维塞进二维，好处是列渲染、数字过滤、留空判定全部复用；
 * 代价是序列化两端必须成对拍平/还原——漏一端就是「填了保存不上」或「存了显示不出」。
 *
 * 货币边界与另外两种模式一致：后端美元、界面人民币，只乘汇率不乘 2。
 */

const RATE = 7.3;
const cell = (r) => videoCellKey(r, VIDEO_COL_PER_SECOND);

describe('per_second 序列化', () => {
  it('拍平成一维的 { 分辨率: $/秒 }', () => {
    const out = serializeVideoMatrix(
      {
        mode: VIDEO_MODE_PER_SECOND,
        resolutions: ['544p', '1080p', '2k'],
        cells: {
          [cell('544p')]: '0.10',
          [cell('1080p')]: '0.50',
          [cell('2k')]: '1.00',
        },
      },
      RATE,
    );
    expect(out.mode).toBe(VIDEO_MODE_PER_SECOND);
    // 一维：值直接是数字，不是 { col: 数字 }
    expect(out.per_second['1080p']).toBeCloseTo(0.5 / RATE, 9);
    expect(out.per_second['2k']).toBeCloseTo(1 / RATE, 9);
    expect(out.token).toBeUndefined();
    expect(out.per_call).toBeUndefined();
  });

  // 留空 = 该档未配置，不写入。写 0 会被后端当成「未配置」的另一种形态
  it('留空的档位不写进配置', () => {
    const out = serializeVideoMatrix(
      {
        mode: VIDEO_MODE_PER_SECOND,
        resolutions: ['544p', '1080p'],
        cells: { [cell('1080p')]: '0.50' },
      },
      RATE,
    );
    expect(Object.keys(out.per_second)).toEqual(['1080p']);
  });

  it('一格都没填时返回 null，不写进配置', () => {
    expect(
      serializeVideoMatrix(
        { mode: VIDEO_MODE_PER_SECOND, resolutions: ['544p'], cells: {} },
        RATE,
      ),
    ).toBeNull();
  });

  it('解析回来能还原成同一份 state', () => {
    // 美元值取的是 ¥0.10 / ¥0.50 的精确换算（÷7.3），不是四舍五入到 4 位的
    // 0.0137——后者乘回来是 0.10001，界面上会显示成一个没人填过的数
    const entry = {
      mode: VIDEO_MODE_PER_SECOND,
      per_second: { '544p': 0.013698630137, '1080p': 0.068493150685 },
    };
    const parsed = parseVideoMatrixEntry(entry, RATE);
    expect(parsed.mode).toBe(VIDEO_MODE_PER_SECOND);
    expect(parsed.resolutions).toEqual(['544p', '1080p']);
    expect(parsed.cells[cell('544p')]).toBe('0.10');
    expect(parsed.cells[cell('1080p')]).toBe('0.50');
  });

  // 往返必须闭合：解析→序列化拿回原值，否则打开编辑器不改任何东西再保存就会漂
  it('parse → serialize 往返不丢精度', () => {
    const entry = {
      mode: VIDEO_MODE_PER_SECOND,
      per_second: { '1080p': 0.0685, '2k': 0.137 },
    };
    const round = serializeVideoMatrix(
      parseVideoMatrixEntry(entry, RATE),
      RATE,
    );
    expect(round.per_second['1080p']).toBeCloseTo(0.0685, 9);
    expect(round.per_second['2k']).toBeCloseTo(0.137, 9);
  });
});

describe('三种模式互不串味', () => {
  // mode 认错会让整表落到另一个字段名下，后端 validate 通过、查表永远未命中
  it('未知 mode 回落到 token 而不是 per_second', () => {
    expect(parseVideoMatrixEntry({ mode: 'whatever' }, RATE).mode).toBe(
      VIDEO_MODE_TOKEN,
    );
  });

  it('per_call 仍然序列化成二维表', () => {
    const out = serializeVideoMatrix(
      {
        mode: VIDEO_MODE_PER_CALL,
        resolutions: ['720p'],
        seconds: ['5', '10'],
        cells: {
          [videoCellKey('720p', '5')]: '1.00',
          [videoCellKey('720p', '10')]: '2.00',
        },
      },
      RATE,
    );
    expect(out.mode).toBe(VIDEO_MODE_PER_CALL);
    expect(out.per_call['720p']['10']).toBeCloseTo(2 / RATE, 9);
    expect(out.per_second).toBeUndefined();
  });
});

// A：矩阵行名的候选分辨率从模型自己的 sizes 来，而不是硬编码通用档位。
// 硬编码的话，LTX-2.5（544P/704P/1080P/2K）和 H3（480P/768p）配出来的行名
// 永远命中不了，而未命中是静默回退固定价的。
describe('分辨率候选来自模型配置', () => {
  const cfg = JSON.stringify({
    models: {
      'ltx2.5': { sizes: ['544P', '704P', '1080P', '2K'] },
      'minimax-h3-fl2va': { tabs: { text2video: { sizes: ['480P', '768p'] } } },
      'both-levels': {
        sizes: ['480P'],
        tabs: { t: { sizes: ['720P', '480p'] } },
      },
      'ratio-only': { sizes: ['16:9', '9:16'] },
      'no-sizes': {},
    },
  });

  it('取该模型配的档位，统一小写', () => {
    expect(videoResolutionOptionsForModel(cfg, 'ltx2.5')).toEqual([
      '544p',
      '704p',
      '1080p',
      '2k',
    ]);
  });

  // 2026-08 的 tabs 改造把参数按玩法分格存了，只读模型级会漏掉
  it('tab 层的 sizes 也要收', () => {
    expect(videoResolutionOptionsForModel(cfg, 'minimax-h3-fl2va')).toEqual([
      '480p',
      '768p',
    ]);
  });

  it('模型级与 tab 层合并且去重', () => {
    expect(videoResolutionOptionsForModel(cfg, 'both-levels')).toEqual([
      '480p',
      '720p',
    ]);
  });

  // 比例词不含分辨率信息，VideoResolutionTier 对它返回空串，配成行名是死配置
  it('比例词不进候选，退回通用档位', () => {
    expect(videoResolutionOptionsForModel(cfg, 'ratio-only')).toEqual(
      VIDEO_DEFAULT_RESOLUTIONS,
    );
  });

  it('取不到配置时优雅回落，不抛异常', () => {
    for (const [raw, name] of [
      [cfg, 'no-sizes'],
      [cfg, '不存在的模型'],
      ['不是 JSON', 'ltx2.5'],
      ['', 'ltx2.5'],
      [null, 'ltx2.5'],
      [cfg, ''],
    ]) {
      expect(videoResolutionOptionsForModel(raw, name)).toEqual(
        VIDEO_DEFAULT_RESOLUTIONS,
      );
    }
  });
});
