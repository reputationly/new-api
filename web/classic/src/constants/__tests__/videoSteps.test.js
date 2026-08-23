import { describe, it, expect } from 'vitest';
import {
  getDefaultStepsForVideoModel,
  VIDEO_STEPS_MODES,
  parseVideoModelConfig,
  aspectRatioToShape,
} from '../videoPlayground.constants';

describe('getDefaultStepsForVideoModel', () => {
  const cfg = {
    models: {
      'video-h3-turbo8': { defaultSteps: 8 },
      'video-h3': { defaultSteps: null },
      'video-h3-zero': { defaultSteps: 0 },
    },
  };

  it('返回运营配的步数', () => {
    expect(getDefaultStepsForVideoModel(cfg, 'video-h3-turbo8')).toBe(8);
  });

  // null/0/未配都是「没配」，一律回落到 null —— 体验区据此把框子留空、不下发，
  // 由后端回落引擎族基座档。0 若当成有效值发出去，引擎会跑 0 步出黑帧。
  it('没配 / null / 0 一律当没配', () => {
    expect(getDefaultStepsForVideoModel(cfg, 'video-h3')).toBeNull();
    expect(getDefaultStepsForVideoModel(cfg, 'video-h3-zero')).toBeNull();
    expect(getDefaultStepsForVideoModel(cfg, '不存在的模型')).toBeNull();
    expect(getDefaultStepsForVideoModel(null, 'video-h3')).toBeNull();
  });

  // parseVideoModelConfig 是白名单式重建：漏了 defaultSteps，每次管理页保存都会把步数
  // 抹掉，而症状只是「蒸馏模型悄悄变慢」，不报错。
  it('经 parseVideoModelConfig 往返后步数还在', () => {
    const raw = JSON.stringify({
      models: { 'video-h3-turbo8': { defaultSteps: 8, engine: 'minimax-h3' } },
    });
    const norm = parseVideoModelConfig(raw);
    expect(getDefaultStepsForVideoModel(norm, 'video-h3-turbo8')).toBe(8);
  });
});

describe('VIDEO_STEPS_MODES', () => {
  // 用户要的三个玩法：文生视频 / 关键帧 / 参考生视频。
  it('只开放这三个玩法', () => {
    expect(VIDEO_STEPS_MODES).toEqual(['text2video', 'flf2v', 'r2va']);
  });

  // 超分/配音/数字人/视频编辑的画面由源素材决定，给步数旋钮只会误导。
  it('不含由源素材决定形态的玩法', () => {
    for (const m of ['sr', 'dub', 's2v', 'vace', 'image2video']) {
      expect(VIDEO_STEPS_MODES).not.toContain(m);
    }
  });
});

// 网关侧 h3AspectRatioFromTargetShape 靠 3% 相对容差把 target_shape 反推回具名比例。
// 这里守住前端这一半的契约：aspectRatioToShape 产出的 shape 必须落在那个容差内，
// 否则 H3 用户选的比例会被静默丢弃、退回缺省 16:9。
describe('aspectRatioToShape 与后端反推容差的契约', () => {
  const named = {
    '21:9': 21 / 9,
    '16:9': 16 / 9,
    '4:3': 4 / 3,
    '1:1': 1,
    '3:4': 3 / 4,
    '9:16': 9 / 16,
  };

  it('每个具名比例的 shape 都在 3% 容差内还原得回去', () => {
    for (const [ratio, want] of Object.entries(named)) {
      const shape = aspectRatioToShape(ratio);
      expect(shape, `${ratio} 应能算出 shape`).toBeTruthy();
      const [h, w] = shape;
      const got = w / h;
      expect(
        Math.abs(got - want) / want,
        `${ratio} 反推偏差超出后端 3% 容差`,
      ).toBeLessThanOrEqual(0.03);
    }
  });
});
