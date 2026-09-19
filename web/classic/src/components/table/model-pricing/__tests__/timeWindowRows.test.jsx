import { describe, it, expect } from 'vitest';
import { buildTimeWindowRows } from '../../../../helpers/discount';

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
});
