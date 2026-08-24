import { describe, it, expect, vi, afterEach } from 'vitest';
import {
  composeImageToRatio,
  parseRatio,
  FIT_BLUR,
  FIT_CROP,
} from '../imageCompose';

// jsdom 的 canvas 没有 2d 上下文（要装 node-canvas），而被测代码在拿不到 ctx 时会
// 原样返回入参 —— 不打桩的话每条用例都只是在测那条兜底分支。这里换上一个会记账的
// 假 ctx，测的就是真正的几何计算：画到哪、画多大。
const stubCanvas = (recorded) => {
  const ctx = {
    filter: 'none',
    drawImage: (...args) => recorded.push(args.slice(1)),
  };
  vi.spyOn(document, 'createElement').mockImplementation((tag) =>
    tag === 'canvas'
      ? {
          width: 0,
          height: 0,
          getContext: () => ctx,
          toDataURL: (type, q) => `data:${type};q=${q},composed`,
        }
      : {},
  );
  return ctx;
};

// 图片解码同理:jsdom 不会真的去解 data URL,onload 永远不触发。按给定尺寸立刻回调。
const stubImage = (w, h) => {
  vi.stubGlobal(
    'Image',
    class {
      constructor() {
        this.naturalWidth = w;
        this.naturalHeight = h;
        setTimeout(() => this.onload?.(), 0);
      }
    },
  );
};

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

const SRC = 'data:image/png;base64,AAAA';

describe('parseRatio', () => {
  it('认冒号与乘号两种写法', () => {
    expect(parseRatio('16:9')).toBeCloseTo(16 / 9, 6);
    expect(parseRatio('1:1')).toBe(1);
    expect(parseRatio('1280x720')).toBeCloseTo(16 / 9, 6);
  });

  it('解析不出返回 0（调用方据此跳过合成，画幅回落到跟随原图）', () => {
    expect(parseRatio('自动')).toBe(0);
    expect(parseRatio('')).toBe(0);
    expect(parseRatio('16:0')).toBe(0);
  });
});

describe('composeImageToRatio', () => {
  it('比例已经对上就原样返回：重编码一次只掉画质，换不来任何东西', async () => {
    stubImage(1280, 720);
    const recorded = [];
    stubCanvas(recorded);
    await expect(composeImageToRatio(SRC, '16:9', FIT_CROP)).resolves.toBe(SRC);
    expect(recorded).toHaveLength(0);
  });

  it('比例解析不出时原样返回（如「跟随上传素材」档的标记值）', async () => {
    stubImage(720, 1280);
    await expect(composeImageToRatio(SRC, 'auto', FIT_CROP)).resolves.toBe(SRC);
  });

  it('居中裁剪：cover 铺满画布，超出的部分对称裁掉', async () => {
    stubImage(720, 1280); // 9:16 竖图 → 16:9
    const recorded = [];
    stubCanvas(recorded);
    const out = await composeImageToRatio(SRC, '16:9', FIT_CROP);

    // 画布保持与原图相近的面积：921600 → 1280x720
    expect(recorded).toHaveLength(1);
    const [x, y, w, h] = recorded[0];
    expect(w).toBeCloseTo(1280, 0);
    expect(h).toBeCloseTo(2275.6, 0); // 高度溢出画布，上下各裁掉一半
    expect(x).toBeCloseTo(0, 6);
    expect(y).toBeCloseTo((720 - 2275.6) / 2, 0);
    expect(out).toMatch(/^data:image\/jpeg/);
  });

  it('虚化补边：前景 contain 居中，原图一个像素都不裁、比例不变', async () => {
    stubImage(720, 1280);
    const recorded = [];
    stubCanvas(recorded);
    await composeImageToRatio(SRC, '16:9', FIT_BLUR);

    // 两层模糊背景 + 一层前景
    expect(recorded).toHaveLength(3);
    const [x, y, w, h] = recorded[2];
    expect(w).toBeCloseTo(405, 0); // 720 * (720/1280)
    expect(h).toBeCloseTo(720, 0); // 高度顶满，说明没有被裁
    expect(w / h).toBeCloseTo(720 / 1280, 6); // 比例与原图一致 = 不形变
    expect(x).toBeCloseTo((1280 - 405) / 2, 0); // 居中，两侧留给虚化背景
    expect(y).toBeCloseTo(0, 6);
  });

  it('横图转竖屏同样成立（方向反过来不能写死成横的）', async () => {
    stubImage(1280, 720);
    const recorded = [];
    stubCanvas(recorded);
    await composeImageToRatio(SRC, '9:16', FIT_BLUR);

    const [x, y, w, h] = recorded[2];
    expect(w).toBeCloseTo(720, 0);
    expect(h).toBeCloseTo(405, 0);
    expect(x).toBeCloseTo(0, 6);
    expect(y).toBeCloseTo((1280 - 405) / 2, 0);
  });
});
