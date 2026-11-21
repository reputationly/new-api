import { describe, it, expect } from 'vitest';
import {
  buildTimeWindowRows,
  formatTimePrices,
} from '../../../../helpers/discount';

// 「分时定价」表要回答的是「哪个时段几折、此刻走哪一档」。这两件事靠肉眼看渲染
// 结果验不出来——漏一行、标错「进行中」、或者把配置倍率当成最终倍率显示，
// 全都不会报错，只会让用户按一个错的数去对账。

// 运营的真实配法：「工作日高峰」在业务上是一个档位，但午休把它切成两段，
// 一个模板只能表示一个连续区间，所以拆成两个模板各绑一条、都填 0.5。
const twoWindowEntry = (activeIdx) => ({
  active: activeIdx !== null,
  best_ratio: 0.5,
  best_label: '上午工作时间',
  normal_ratio: 0.35,
  label: activeIdx === 0 ? '上午工作时间' : '下午工作时间',
  until: '2026-09-14T12:00:00+08:00',
  windows: [
    {
      label: '上午工作时间',
      start: '09:00',
      end: '12:00',
      days: [1, 2, 3, 4, 5],
      ratio: 0.5,
      active: activeIdx === 0,
    },
    {
      label: '下午工作时间',
      start: '14:00',
      end: '18:00',
      days: [1, 2, 3, 4, 5],
      ratio: 0.5,
      active: activeIdx === 1,
    },
  ],
});

const build = (entry) =>
  buildTimeWindowRows({
    usableGroup: { default: '默认' },
    modelEnableGroups: ['default'],
    groupTimeRatio: { default: { m: entry } },
    modelName: 'm',
    normalLabel: '其余时段',
  });

describe('buildTimeWindowRows', () => {
  it('两个模板拆出的同一档位，两行都要列出来', () => {
    const rows = build(twoWindowEntry(0));
    // 带档位名：角标说的是「上午工作时间等2个时段」，表里只有时间区间的话
    // 用户对不上号
    expect(rows.map((r) => r.range)).toEqual([
      '上午工作时间 工作日 09:00-12:00',
      '下午工作时间 工作日 14:00-18:00',
      '其余时段',
    ]);
    // 三行都是最终倍率对应的折扣，不是配置倍率
    expect(rows.map((r) => r.discount.text)).toEqual(['5折', '5折', '3.5折']);
  });

  it('「其余时段」必须有一行，否则看不出未命中时段时按几折付', () => {
    const rows = build(twoWindowEntry(null));
    const normal = rows.find((r) => r.range === '其余时段');
    expect(normal).toBeDefined();
    expect(normal.ratio).toBe(0.35);
  });

  it('只有当前生效的那一行标「进行中」', () => {
    expect(build(twoWindowEntry(0)).map((r) => r.active)).toEqual([
      true,
      false,
      false,
    ]);
    expect(build(twoWindowEntry(1)).map((r) => r.active)).toEqual([
      false,
      true,
      false,
    ]);
    // 未命中任何时段时，生效的是「其余时段」那一行
    expect(build(twoWindowEntry(null)).map((r) => r.active)).toEqual([
      false,
      false,
      true,
    ]);
  });

  // 窗口可以配 per-window 时区，而「09:00-12:00」不说是哪个时区的，用户没法判断
  // 它对自己意味着几点。默认时区不标，免得每行都挂一个对所有人都一样的尾巴。
  it('非默认时区要标出来，默认时区不标', () => {
    const mk = (tz) => ({
      active: false,
      best_ratio: 0.5,
      normal_ratio: 0.35,
      windows: [
        {
          label: '纽约档',
          start: '09:00',
          end: '12:00',
          days: [],
          tz,
          ratio: 0.5,
          active: false,
        },
      ],
    });
    expect(build(mk('America/New_York'))[0].range).toBe(
      '纽约档 每天 09:00-12:00(America/New_York)',
    );
    expect(build(mk('Asia/Shanghai'))[0].range).toBe('纽约档 每天 09:00-12:00');
    expect(build(mk(''))[0].range).toBe('纽约档 每天 09:00-12:00');
  });

  it('没配时段规则的模型不出表', () => {
    expect(build(undefined)).toEqual([]);
  });

  // 窗口全是 ×1 原价档时，getTimeDiscountInfo 会返回 null（它答的是「值不值得出
  // 角标」）。拿它做入口判空，整张表连「其余时段 3.5折」那行都会消失——正是那行
  // 要解决的问题。
  it('窗口全是原价档时仍要列出「其余时段」', () => {
    const rows = build({
      active: false,
      best_ratio: 1,
      normal_ratio: 0.35,
      windows: [
        {
          label: '高峰时段',
          start: '09:00',
          end: '18:00',
          days: [1, 2, 3, 4, 5],
          ratio: 1,
          active: false,
        },
      ],
    });
    expect(rows.map((r) => r.range)).toEqual([
      '高峰时段 工作日 09:00-18:00',
      '其余时段',
    ]);
    expect(rows[1].discount.text).toBe('3.5折');
  });

  // entry.active 答的是「有没有优惠」，命中 ×1 原价档时为假。若「其余时段」行按
  // !entry.active 判定，它会被标成当前档，而用户实际付的是那个原价档的价。
  it('命中原价档时当前行是那个档，不是「其余时段」', () => {
    const rows = build({
      active: false, // 没有优惠
      best_ratio: 1,
      normal_ratio: 0.35,
      windows: [
        {
          label: '高峰时段',
          start: '09:00',
          end: '18:00',
          days: [1, 2, 3, 4, 5],
          ratio: 1,
          active: true, // 但它确实是计费档
        },
      ],
    });
    expect(rows.map((r) => r.active)).toEqual([true, false]);
  });

  it('后端没下发 normal_ratio 时不硬造「其余时段」行', () => {
    const entry = twoWindowEntry(null);
    delete entry.normal_ratio;
    expect(build(entry).map((r) => r.range)).toEqual([
      '上午工作时间 工作日 09:00-12:00',
      '下午工作时间 工作日 14:00-18:00',
    ]);
  });

  // 后端编译后的窗口是 segments 形状（多段/补集展开），start/end/days 只在
  // 单段时回填。多段必须靠 segments 渲染，否则广场上每个时段都显示「每天 -」。
  it('多段窗口按 segments 渲染，天数相同的段合并区间', () => {
    const rows = build({
      active: false,
      best_ratio: 0.5,
      normal_ratio: 0.35,
      windows: [
        {
          label: '工作日高峰',
          segments: [
            { start: '09:00', end: '12:00', days: [1, 2, 3, 4, 5] },
            { start: '14:00', end: '18:00', days: [1, 2, 3, 4, 5] },
          ],
          ratio: 0.5,
          active: false,
        },
      ],
    });
    expect(rows[0].range).toBe('工作日高峰 工作日 09:00-12:00、14:00-18:00');
  });

  it('段间天数不同时逐段标注，不能把周六的时段读成工作日的', () => {
    const rows = build({
      active: false,
      best_ratio: 0.5,
      normal_ratio: 0.35,
      windows: [
        {
          label: '分散档',
          segments: [
            { start: '09:00', end: '10:00', days: [1] },
            { start: '15:00', end: '16:00', days: [6] },
          ],
          ratio: 0.5,
          active: false,
        },
      ],
    });
    expect(rows[0].range).toBe('分散档 周一 09:00-10:00、周六 15:00-16:00');
  });

  it('只列该模型可用的分组', () => {
    const rows = buildTimeWindowRows({
      usableGroup: { default: '默认', premium: '高级' },
      modelEnableGroups: ['default'], // premium 下没有这个模型
      groupTimeRatio: {
        default: { m: twoWindowEntry(null) },
        premium: { m: twoWindowEntry(null) },
      },
      modelName: 'm',
      normalLabel: '其余时段',
    });
    expect(rows.every((r) => r.group === 'default')).toBe(true);
  });

  // 补集窗口（「其余时间」）展开后的多段只是实现细节：用户配的是
  // 「空闲 = 除高峰以外」，展示就该是一行参照描述，而不是把补集
  // 铺成「周末全天 + 工作日三个碎段」。
  it('补集窗口渲染为「除「X」以外」，不铺开展开段', () => {
    const rows = build({
      active: false,
      best_ratio: 0.25,
      normal_ratio: 0.5,
      normal_applies: false,
      windows: [
        {
          label: '高峰时段',
          segments: [
            { start: '09:00', end: '12:00', days: [1, 2, 3, 4, 5] },
            { start: '14:00', end: '18:00', days: [1, 2, 3, 4, 5] },
          ],
          ratio: 0.5,
          active: false,
        },
        {
          label: '空闲时段',
          complement_of: '高峰时段',
          segments: [
            { start: '00:00', end: '09:00', days: [1, 2, 3, 4, 5] },
            { start: '12:00', end: '14:00', days: [1, 2, 3, 4, 5] },
            { start: '18:00', end: '24:00', days: [1, 2, 3, 4, 5] },
            { start: '00:00', end: '24:00', days: [0, 6] },
          ],
          ratio: 0.25,
          active: true,
        },
      ],
    });
    expect(rows.map((r) => r.range)).toEqual([
      '高峰时段 工作日 09:00-12:00、14:00-18:00',
      '空闲时段 除「高峰时段」以外',
    ]);
  });

  // 高峰+空闲并集覆盖整周时，「其余时段」永远不会命中——死行不展示。
  // 反之只配补集规则时被排除的时段落在常规价上，那行必须留着。
  it('normal_applies=false 时省略「其余时段」行，缺省字段仍保留', () => {
    const fullCoverage = {
      active: false,
      best_ratio: 0.5,
      normal_ratio: 0.35,
      normal_applies: false,
      windows: [
        {
          label: '全天档',
          start: '00:00',
          end: '24:00',
          ratio: 0.5,
          active: true,
        },
      ],
    };
    expect(build(fullCoverage)).toHaveLength(1);

    // 旧后端不下发 normal_applies：按「可能未覆盖」处理，行照旧出现
    const legacy = { ...fullCoverage };
    delete legacy.normal_applies;
    expect(build(legacy)).toHaveLength(2);
    expect(build(legacy)[1].label).toBe('其余时段');
  });
});

describe('动态目录价（catalog）模式', () => {
  // 用户的真实配置：default 固定 5 折 + 空闲时段绝对价目 输入¥1/输出¥4。
  // w.ratio = normal_ratio(0.5) × 价目比(0.5) = 0.25 —— 合成折 2.5 折不是
  // 配置出来的折扣，catalog 行的折数必须取与时刻无关的 discount_ratio(0.5)。
  const catalogEntry = {
    active: true,
    best_ratio: 0.25,
    normal_ratio: 0.5,
    normal_applies: false,
    windows: [
      {
        label: '高峰时段',
        start: '09:00',
        end: '18:00',
        days: [1, 2, 3, 4, 5],
        price_mode: 'catalog',
        prices: { input: 2, output: 8 },
        ratio: 0.5,
        discount_ratio: 0.5,
        active: false,
      },
      {
        label: '空闲时段',
        complement_of: '高峰时段',
        price_mode: 'catalog',
        prices: { input: 1, output: 4, cache: 0.02 },
        ratio: 0.25,
        discount_ratio: 0.5,
        active: true,
      },
    ],
  };

  it('catalog 行：折数取固定折扣而非合成终值，价目透传', () => {
    const rows = build(catalogEntry);
    expect(rows).toHaveLength(2);
    const idle = rows[1];
    expect(idle.priceMode).toBe('catalog');
    expect(idle.prices).toEqual({ input: 1, output: 4, cache: 0.02 });
    // discount_ratio 0.5 → 5折；若误用 w.ratio 0.25 会显示 2.5折
    expect(idle.discount.text).toBe('5折');
    expect(idle.discount.text).not.toBe('2.5折');
  });

  it('factor 行（无 price_mode）：维持「该时段最终几折」', () => {
    const rows = build(twoWindowEntry(0));
    expect(rows[0].priceMode).toBe('factor');
    expect(rows[0].prices).toBeNull();
    expect(rows[0].discount.text).toBe('5折'); // ratio 0.5 → 5折，旧行为不变
  });

  it('混配：同行可有 catalog 与 factor 两种 priceMode', () => {
    const mixed = {
      ...catalogEntry,
      windows: [
        catalogEntry.windows[0],
        {
          ...catalogEntry.windows[1],
          price_mode: undefined,
          prices: undefined,
        },
      ],
    };
    const rows = build(mixed);
    expect(rows[0].priceMode).toBe('catalog');
    expect(rows[1].priceMode).toBe('factor');
    // factor 行仍按 w.ratio 折算（0.25 → 2.5折），catalog 行按固定折扣
    expect(rows[1].discount.text).toBe('2.5折');
  });
});

describe('formatTimePrices', () => {
  it('按固定分量序输出「标签¥价」，最多 3 个 + …', () => {
    expect(
      formatTimePrices({ output: 4, input: 1, cache: 0.02 }, (s) => s),
    ).toBe('输入¥1 输出¥4 缓存命中¥0.02');
    expect(
      formatTimePrices(
        { input: 1, output: 4, cache: 0.02, create_cache: 0.01 },
        (s) => s,
      ),
    ).toBe('输入¥1 输出¥4 缓存命中¥0.02 …');
    expect(formatTimePrices(null, (s) => s)).toBe('');
    expect(formatTimePrices({}, (s) => s)).toBe('');
    // 非法/非正值分量跳过
    expect(formatTimePrices({ input: 0, output: -1 }, (s) => s)).toBe('');
  });
});
