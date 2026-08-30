import { describe, it, expect } from 'vitest';
import {
  MUSIC3_DURATIONS,
  MUSIC3_FRAMES_PER_SECOND,
  MUSIC3_MAX_FRAMES,
  music3FramesForSeconds,
} from '../musicPlayground.constants';

// Music3 的时长有三个来源不同的数字,曾经互相打架:档位表最大 240 秒、tooltip 写
// 「上限 6 分钟」、常量注释提模型卡的五分钟。这一组把它们的关系钉住。
describe('Music3 时长换算', () => {
  it('秒 → max_new_tokens 按 25 fps', () => {
    expect(music3FramesForSeconds(30)).toBe(750); // 官方 curl 里就是这个数
    expect(music3FramesForSeconds('60')).toBe(1500);
    expect(MUSIC3_FRAMES_PER_SECOND).toBe(25);
  });

  it('留空 / 非法 → null(不下发,由引擎默认决定)', () => {
    for (const v of ['', null, undefined, 0, -5, 'abc']) {
      expect(music3FramesForSeconds(v)).toBeNull();
    }
  });

  it('超出引擎硬上限时钳位,不把超额值发出去', () => {
    expect(music3FramesForSeconds(9999)).toBe(MUSIC3_MAX_FRAMES);
  });

  // 关键:档位表里**不该有**会被钳位的档 —— 那种档位是在骗用户,他选了 6 分钟,
  // 实际发出去的和选 5 分 60 秒一样。档位最大值应落在硬上限之内。
  it('每个档位换算后都在硬上限内,没有"选了也没用"的档', () => {
    for (const d of MUSIC3_DURATIONS.filter(Boolean)) {
      const frames = music3FramesForSeconds(d);
      expect(frames, `${d}s 档位`).toBeLessThanOrEqual(MUSIC3_MAX_FRAMES);
      expect(frames).toBe(parseInt(d, 10) * MUSIC3_FRAMES_PER_SECOND);
    }
  });

  it('第一档是"自动"(空串),即不下发', () => {
    expect(MUSIC3_DURATIONS[0]).toBe('');
  });
});
