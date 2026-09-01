import { describe, it, expect } from 'vitest';
import {
  flattenVideoMatrix,
  videoResolutionRank,
  VIDEO_PER_SECOND_COLUMN,
} from '../videoMatrix';

/**
 * 视频计费矩阵的摊平结果。两端共用这一份（手机端直接 import），
 * 所以它错了就是 PC 和手机一起错。
 *
 * per_second 的后端表是一维的 { 分辨率: $/秒 }，由本模块升成二维走同一套逻辑。
 * 虚拟列名必须是个哨兵值而不是「秒」这类可读文本——渲染端拿它拼标签会得到
 * 「秒 秒」，而哨兵值逼着渲染端显式分支。
 */

const perSecond = {
  mode: 'per_second',
  per_second: { '1080p': 0.05, '544p': 0.01, '2k': 0.1 },
};

describe('per_second 摊平', () => {
  it('升成二维，列名是哨兵值', () => {
    const rows = flattenVideoMatrix(perSecond);
    expect(rows).toHaveLength(3);
    rows.forEach((r) => expect(r.column).toBe(VIDEO_PER_SECOND_COLUMN));
  });

  it('按分辨率升序，2k 排在 1080p 之后', () => {
    expect(flattenVideoMatrix(perSecond).map((r) => r.resolution)).toEqual([
      '544p',
      '1080p',
      '2k',
    ]);
  });

  it('乘上分组倍率', () => {
    const rows = flattenVideoMatrix(perSecond, 0.5);
    expect(rows.find((r) => r.resolution === '1080p').priceUSD).toBeCloseTo(
      0.025,
      9,
    );
  });
});

describe('折前价', () => {
  // 划线原价的口径与按量/按次两条路一致：少乘一个分组倍率，且仅在有折扣时给
  it('打折时给出折前价', () => {
    const row = flattenVideoMatrix(perSecond, 0.5).find(
      (r) => r.resolution === '1080p',
    );
    expect(row.priceUSD).toBeCloseTo(0.025, 9);
    expect(row.originalPriceUSD).toBeCloseTo(0.05, 9);
  });

  it('无折扣时不给折前价', () => {
    flattenVideoMatrix(perSecond, 1).forEach((r) =>
      expect(r.originalPriceUSD).toBeNull(),
    );
    // 详情页展示基础单价时不传倍率，同样不该出现划线
    flattenVideoMatrix(perSecond).forEach((r) =>
      expect(r.originalPriceUSD).toBeNull(),
    );
  });

  // 倍率 > 1 是涨价，划线会显示成「原价更便宜」，比不显示更糟
  it('倍率大于 1 时不给折前价', () => {
    flattenVideoMatrix(perSecond, 1.5).forEach((r) =>
      expect(r.originalPriceUSD).toBeNull(),
    );
  });

  // 免费分组倍率是 0：折后价为 0，折前价仍要给，否则用户看不出免了多少
  it('免费分组给出折前价', () => {
    const row = flattenVideoMatrix(perSecond, 0).find(
      (r) => r.resolution === '1080p',
    );
    expect(row.priceUSD).toBe(0);
    expect(row.originalPriceUSD).toBeCloseTo(0.05, 9);
  });

  it('token 与 per_call 同样给折前价', () => {
    const token = flattenVideoMatrix(
      { mode: 'token', token: { '720p': { without_video: 8 } } },
      0.5,
    )[0];
    expect(token.originalPriceUSD).toBeCloseTo(8, 9);

    const perCall = flattenVideoMatrix(
      { mode: 'per_call', per_call: { '720p': { 5: 0.2 } } },
      0.5,
    )[0];
    expect(perCall.originalPriceUSD).toBeCloseTo(0.2, 9);
  });
});

describe('分辨率排序权重', () => {
  it('k 档按数字单调，不再塌成 0', () => {
    expect(videoResolutionRank('2k')).toBeGreaterThan(
      videoResolutionRank('1080p'),
    );
    expect(videoResolutionRank('4k')).toBeGreaterThan(videoResolutionRank('2k'));
  });

  it('兜底行排最后', () => {
    expect(videoResolutionRank('*')).toBeGreaterThan(videoResolutionRank('8k'));
  });

  it('LTX 的四档顺序正确', () => {
    const order = ['2k', '544p', '1080p', '704p'].sort(
      (a, b) => videoResolutionRank(a) - videoResolutionRank(b),
    );
    expect(order).toEqual(['544p', '704p', '1080p', '2k']);
  });
});
