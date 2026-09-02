import { describe, it, expect } from 'vitest';
import {
  computeImageSize,
  getImageShapeConfig,
  parseImageSizeConfig,
  FALLBACK_IMAGE_SIZES,
  DEFAULT_IMAGE_SIZE_ALIGN,
  resolveSubmitImageSize,
} from '../imagePlayground.constants';

// 图像画幅配置。这套的存在理由是实测出来的：同一个模型，收到比例词 "16:9" 出
// 1344x768（1.03MP），收到 "2720x1536" 原样出 4.18MP —— 差 4 倍。比例词把画幅决定权
// 交给了引擎的离散表。
//
// 只有两种模式：配齐「宽高比 + 分辨率档」→ area（算像素）；其余一切 → table（值原样
// 下发）。下面的矩阵是**穷举**配置维度，不是"想到哪条测哪条" —— 早先零散写用例，
// 连着几轮评审都在补我没想到的组合。

describe('computeImageSize 必须精确复现 SenseNova-U1.5 的官方五档', () => {
  // base=2048（面积 4.19M）、align=32。32 是实测值：给引擎发 2368x1776，它出的是
  // 2368x1792（按 32 上取整）。用 32 算，界面显示的就是最终出图尺寸。
  const cases = [
    ['1:1', '2048x2048'],
    ['3:2', '2496x1664'],
    ['2:3', '1664x2496'],
    ['16:9', '2720x1536'],
    ['9:16', '1536x2720'],
  ];
  for (const [ratio, expected] of cases) {
    it(`${ratio} → ${expected}`, () => {
      expect(computeImageSize(ratio, 2048, 32)).toBe(expected);
    });
  }

  // ⚠️ 别拿 16:9 来测「向下取整」：它原始算出 2730.67，/32=85.33，floor 与 round
  // 结果相同（都是 85→2720），换成 round 这条照样绿——是假测试。
  // 4:3 才能区分：宽 2364.9，/32=73.9，floor→73→2336，round→74→2368。
  it('向下取整而不是四舍五入（4:3 才区分得开）', () => {
    expect(computeImageSize('4:3', 2048, 32)).toBe('2336x1760');
    expect(computeImageSize('4:3', 2048, 32)).not.toBe('2368x1760');
  });

  it('对齐粒度跟着模型走，不是写死 32', () => {
    // Qwen-Image 官方表是 16 的倍数，且没有一个是 64 的倍数（1328/64=20.75）。
    expect(computeImageSize('1:1', 1328, 16)).toBe('1328x1328');
    expect(computeImageSize('1:1', 1328, 32)).toBe('1312x1312');
  });

  it('留空对齐粒度时用默认 32', () => {
    expect(computeImageSize('16:9', 2048, null)).toBe(
      computeImageSize('16:9', 2048, DEFAULT_IMAGE_SIZE_ALIGN),
    );
  });

  // 运营可能填出解析不出的写法（中文全角冒号、斜杠）。返回空串是**契约**：
  // 调用方据此退回"一个 size 字段都不发"，而不是把空串塞进请求。
  it('解析不出的比例返回空串，调用方据此不下发 size', () => {
    expect(computeImageSize('', 2048, 32)).toBe('');
    expect(computeImageSize('16：9', 2048, 32)).toBe(''); // 中文全角冒号
    expect(computeImageSize('16/9', 2048, 32)).toBe('');
    expect(computeImageSize('16:9', 0, 32)).toBe('');
  });
});

// ---------------------------------------------------------------------------
// 配置矩阵：穷举配置维度 → 期望的 mode / sizes / tabScoped
//
// **必须走真实的 parseImageSizeConfig**。手搓 config 对象会绕开它的实际形态：
// parse 对每个模型都会落 sizes:[]（空数组是 truthy），没配分类默认值时还会把内置兜底
// 写进 config.default —— 这两点各自坑过一次，手搓对象一次都测不出来。
// ---------------------------------------------------------------------------
const build = ({ tab = {}, model = {}, dflt } = {}) =>
  parseImageSizeConfig(
    JSON.stringify({
      ...(dflt ? { default: dflt } : {}),
      models: { m: { ...model, tabs: { text2image: tab } } },
    }),
  );

const RATIOS = ['1:1', '16:9'];
const TIERS = [2048];

const MATRIX = [
  {
    name: '配齐「比例 + 档位」→ area，尺寸列表让位',
    cfg: { tab: { aspectRatios: RATIOS, sizeTiers: TIERS } },
    mode: 'area',
    sizes: [],
    tabScoped: true,
  },
  {
    name: '配齐比例档位、同时还配了尺寸 → 仍是 area（尺寸不生效，管理页有告警）',
    cfg: {
      tab: { sizes: ['1024x1024'], aspectRatios: RATIOS, sizeTiers: TIERS },
    },
    mode: 'area',
    sizes: [],
    tabScoped: true,
  },
  {
    name: '只配比例、没配档位 → 不成对即不生效，回落尺寸列表',
    cfg: { tab: { aspectRatios: RATIOS } },
    mode: 'table',
    sizes: FALLBACK_IMAGE_SIZES,
    tabScoped: true,
  },
  {
    name: '只配档位、没配比例 → 同上',
    cfg: { tab: { sizeTiers: TIERS } },
    mode: 'table',
    sizes: FALLBACK_IMAGE_SIZES,
    tabScoped: true,
  },
  {
    name: 'tab 级精确像素 → table',
    cfg: { tab: { sizes: ['2048x2048', '1024x1024'] } },
    mode: 'table',
    sizes: ['2048x2048', '1024x1024'],
    tabScoped: true,
  },
  {
    // 现网上线时就是这个形态（用户报的"只能选宽高比"）。比例词原样下发，
    // 后端 setImageShape 走 aspect_ratio 分支 —— 与改造前一字不差。
    name: 'tab 级比例词 → table，原样下发',
    cfg: { tab: { sizes: ['16:9', '9:16'] } },
    mode: 'table',
    sizes: ['16:9', '9:16'],
    tabScoped: true,
  },
  {
    name: 'tab 级混填两种写法 → table，一个都不丢',
    cfg: { tab: { sizes: ['16:9', '1:1', '1024x1024'] } },
    mode: 'table',
    sizes: ['16:9', '1:1', '1024x1024'],
    tabScoped: true,
  },
  {
    name: '模型级 sizes（本 tab 没配）→ table，但不算 tab 级声明',
    cfg: { model: { sizes: ['1664x928'] } },
    mode: 'table',
    sizes: ['1664x928'],
    tabScoped: false,
  },
  {
    name: '分类默认值（像素）→ table',
    cfg: { dflt: ['1536x1536'] },
    mode: 'table',
    sizes: ['1536x1536'],
    tabScoped: false,
  },
  {
    name: '分类默认值（比例词）→ table，原样下发',
    cfg: { dflt: ['16:9', '4:3'] },
    mode: 'table',
    sizes: ['16:9', '4:3'],
    tabScoped: false,
  },
  {
    name: '什么都没配 → 内置兜底',
    cfg: {},
    mode: 'table',
    sizes: FALLBACK_IMAGE_SIZES,
    tabScoped: false,
  },
  {
    // 宽高比与分辨率档是 tab-only：recomputeModelLevel 不写、parse 不留、这里不读，
    // 三处口径一致。手写进 option 的模型级值同样不生效。
    name: '手写的模型级比例/档位不读 → table',
    cfg: { model: { aspectRatios: RATIOS, sizeTiers: TIERS } },
    mode: 'table',
    sizes: FALLBACK_IMAGE_SIZES,
    tabScoped: false,
  },
];

describe('画幅配置矩阵', () => {
  for (const c of MATRIX) {
    it(c.name, () => {
      const r = getImageShapeConfig(build(c.cfg), 'm', 'text2image');
      expect(r.mode, 'mode').toBe(c.mode);
      expect(r.sizes, 'sizes').toEqual(c.sizes);
      expect(r.tabScoped, 'tabScoped').toBe(c.tabScoped);
    });
  }

  it('本模型配了就不再读分类默认值', () => {
    const r = getImageShapeConfig(
      build({ tab: { sizes: ['2048x2048'] }, dflt: ['1024x1024'] }),
      'm',
      'text2image',
    );
    expect(r.sizes).toEqual(['2048x2048']);
  });

  it('比例与档位只对配置它的那个 tab 生效，不串到别的 tab', () => {
    const cfg = parseImageSizeConfig(
      JSON.stringify({
        models: {
          m: {
            tabs: {
              text2image: { aspectRatios: RATIOS, sizeTiers: TIERS },
              image2image: {},
            },
          },
        },
      }),
    );
    expect(getImageShapeConfig(cfg, 'm', 'text2image').mode).toBe('area');
    expect(getImageShapeConfig(cfg, 'm', 'image2image').mode).toBe('table');
    expect(getImageShapeConfig(cfg, 'm', 'image2image').tabScoped).toBe(false);
  });
});

// 白名单式重建：漏一个键 = 运营每次在管理页保存就把它删一次。
describe('新字段能往返', () => {
  it('aspectRatios / sizeTiers / sizeAlign 都保得住', () => {
    const parsed = parseImageSizeConfig(
      JSON.stringify({
        models: {
          'u1.5': {
            sizeAlign: 32,
            tabs: {
              text2image: {
                aspectRatios: ['16:9', '1:1'],
                sizeTiers: ['2048', 1024],
              },
            },
          },
        },
      }),
    );
    const t = parsed.models['u1.5'].tabs.text2image;
    expect(t.aspectRatios).toEqual(['16:9', '1:1']);
    // 字符串数字要归一成整数，并从小到大排
    expect(t.sizeTiers).toEqual([1024, 2048]);
    expect(parsed.models['u1.5'].sizeAlign).toBe(32);
    expect(getImageShapeConfig(parsed, 'u1.5', 'text2image').align).toBe(32);
  });

  it('未配的字段不落键，好让取值链正确降级', () => {
    const parsed = parseImageSizeConfig({
      models: { m: { tabs: { text2image: {} } } },
    });
    const t = parsed.models['m'].tabs.text2image;
    expect('aspectRatios' in t).toBe(false);
    expect('sizeTiers' in t).toBe(false);
    expect(parsed.models['m'].sizeAlign).toBeNull();
    expect(getImageShapeConfig(parsed, 'm', 'text2image').align).toBe(
      DEFAULT_IMAGE_SIZE_ALIGN,
    );
  });
});

// 宽高比与分辨率档是 **tab-only**：写（管理页保存）、读（getImageShapeConfig）、
// parse（白名单重建）三处必须一致。早先只有读侧收成 tab-only，保存那侧照写不误，
// 于是每次保存都往 option 里塞一份没人读、下次加载又被丢掉的噪声键。
describe('tab-only 字段不得被反推到模型级', () => {
  it('recomputeModelLevel 不写图像的 aspectRatios / sizeTiers', async () => {
    const { recomputeModelLevel } = await import(
      '../playgroundAdmin.constants'
    );
    const saved = recomputeModelLevel('ImageModelSizeConfig', {
      tabs: {
        text2image: { aspectRatios: ['16:9'], sizeTiers: [2048] },
        image2image: { aspectRatios: ['1:1'], sizeTiers: [1024] },
      },
    });
    expect(saved).not.toHaveProperty('aspectRatios');
    expect(saved).not.toHaveProperty('sizeTiers');
  });

  it('视频侧不受影响——它的 aspectRatios 有模型级兜底的实际用途', async () => {
    const { recomputeModelLevel } = await import(
      '../playgroundAdmin.constants'
    );
    const saved = recomputeModelLevel('VideoModelConfig', {
      tabs: { text2video: { aspectRatios: ['16:9'] } },
    });
    expect(saved.aspectRatios).toEqual(['16:9']);
  });
});

// 下发 size 的判据。**穷举**三个来源 × 三种值 —— 这条判断散在 generate 里的时候，
// 接连两轮评审各挑出它一个漏洞（空串照发、auto 漏排除），两次都是"改了一支忘了另一支"。
describe('resolveSubmitImageSize 穷举', () => {
  const ctx = (o) => ({
    isI2I: false,
    usesComputedShape: false,
    canPickI2ISize: false,
    ...o,
  });
  const T2I = ctx({});
  const I2I_NONE = ctx({ isI2I: true });
  const I2I_AREA = ctx({ isI2I: true, usesComputedShape: true });
  const I2I_TABLE = ctx({ isI2I: true, canPickI2ISize: true });

  const CASES = [
    ['文生图 · 正常值 → 发', '2720x1536', T2I, '2720x1536'],
    ['文生图 · 空值（算不出比例）→ 不发', '', T2I, ''],
    ['文生图 · auto → 不发', 'auto', T2I, ''],
    ['图生图 · 未开启画幅 · 有值 → 不发', '1024x1024', I2I_NONE, ''],
    ['图生图 · 算出来的像素 → 发', '2720x1536', I2I_AREA, '2720x1536'],
    ['图生图 · 算不出 → 不发', '', I2I_AREA, ''],
    ['图生图 · 老会话存的 auto → 不发', 'auto', I2I_AREA, ''],
    ['图生图 · 白名单档位 → 发', '1024x1024', I2I_TABLE, '1024x1024'],
    ['图生图 · 白名单 + auto → 不发', 'auto', I2I_TABLE, ''],
  ];
  for (const [name, size, context, expected] of CASES) {
    it(name, () => {
      expect(resolveSubmitImageSize(size, context)).toBe(expected);
    });
  }

  it('顺带归一化：全角乘号与大写 X 都认', () => {
    expect(resolveSubmitImageSize('2720X1536', T2I)).toBe('2720x1536');
    expect(resolveSubmitImageSize('2720×1536', T2I)).toBe('2720x1536');
  });
});
