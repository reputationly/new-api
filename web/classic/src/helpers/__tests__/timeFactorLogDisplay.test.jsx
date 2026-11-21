import React from 'react';
import { describe, it, expect, beforeEach } from 'vitest';
import { render } from '@testing-library/react';
import {
  renderModelPrice,
  renderLogContent,
  renderModelPriceSimple,
  timeFactorDisplay,
} from '../render';

// 分时价日志的展示归一：后端 other.time_factors 是命中时段的分量系数表
// （分量 → 相对常规目录价的倍率），group_ratio 是含 input 系数的最终倍率。
// 展示口径 = 「该时段目录价 × 不含时段的常规倍率」，乘积与实扣逐位一致。
//
// 真实案例（2026-11-21 日志）：常规 输入 ¥2 / 输出 ¥8 / 缓存 ¥0.04，
// 空闲时段全分量 ×0.5，用户组倍率 0.5 → group_ratio 落库 0.25。
// 修复前展示「输入 ¥2 … 分组倍率 0.25x」把两层折扣拆散了；
// 修复后应为「输入 ¥1 / 输出 ¥4 / 缓存 ¥0.02 / 分组倍率 0.5x」。

const QPY = 1000000000;

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem('quota_per_yuan', String(QPY));
  localStorage.setItem('quota_display_type', 'CNY');
});

const renderText = (node) => render(<div>{node}</div>).container.textContent;

const idleLog = {
  model_ratio: 2,
  completion_ratio: 4,
  cache_ratio: 0.02,
  model_price: -1,
  group_ratio: 0.25,
  user_group_ratio: -1,
  prompt_tokens: 31,
  completion_tokens: 14,
  cache_tokens: 0,
  time_factors: { input: 0.5, output: 0.5, cache: 0.5 },
};

describe('分时价日志展示', () => {
  it('计费过程：目录价换成时段价，倍率摘掉时段系数', () => {
    const text = renderText(renderModelPrice(idleLog));
    expect(text).toContain('输入价格：¥1');
    expect(text).toContain('输出价格：¥4');
    // 合计 = (31×¥1 + 14×¥4)/1M × 0.5 = ¥0.0000435，与实扣一致
    expect(text).toContain('分组倍率 0.5');
    expect(text).not.toContain('分组倍率 0.25');
    expect(text).toContain('¥0.0000435');
  });

  it('日志详情摘要：同样按时段目录价 + 常规倍率', () => {
    const text = renderText(
      renderLogContent({ ...idleLog, displayMode: 'price' }),
    );
    expect(text).toContain('输入价格 ¥1');
    expect(text).toContain('输出价格 ¥4');
    expect(text).toContain('分组倍率 0.5x');
    expect(text).not.toContain('0.25x');
  });

  it('segments 摘要列：价格段同样归一', () => {
    const segs = renderModelPriceSimple({
      ...idleLog,
      outputMode: 'segments',
    });
    const text = segs.map((s) => s.text).join(' ');
    expect(text).toContain('输入 ¥1');
    expect(text).toContain('0.5x');
    expect(text).not.toContain('0.25x');
  });

  it('分量系数不一致时各自按自己的系数（output 0.25 / input 0.5）', () => {
    const text = renderText(
      renderModelPrice({
        ...idleLog,
        group_ratio: 0.25,
        time_factors: { input: 0.5, output: 0.25, cache: 0.5 },
      }),
    );
    // 输入 ¥2×0.5=¥1；输出 ¥8×0.25=¥2；倍率 0.25/0.5=0.5
    expect(text).toContain('输入价格：¥1');
    expect(text).toContain('输出价格：¥2');
    expect(text).toContain('分组倍率 0.5');
  });

  it('无 time_factors 的旧日志：展示与改造前完全一致', () => {
    const { time_factors, ...old } = idleLog;
    const text = renderText(renderModelPrice(old));
    expect(text).toContain('输入价格：¥2');
    expect(text).toContain('输出价格：¥8');
    expect(text).toContain('分组倍率 0.25');
  });

  it('专属倍率命中时时段系数不剥它：Layer-1 基准本就不含时段', () => {
    // SpecialRatio 是 ResolveGroupRatio 的 Layer-1（覆盖 Base），Layer-4 时段
    // 系数是之后才乘上去的——user_group_ratio 里没有 f_in 可剥。
    // 若误除 fIn，0.7 的身份折扣会显示成 1.4x 溢价。
    const text = renderText(
      renderModelPrice({
        ...idleLog,
        group_ratio: 0.35, // 0.7 专属 × 0.5 时段终值
        user_group_ratio: 0.7,
      }),
    );
    expect(text).toContain('专属倍率 0.7');
    expect(text).not.toContain('1.4');
  });

  it('专属倍率 + 模型规则 + 时段同时命中：算式用 final/f_in 复合终值', () => {
    // ResolveGroupRatio 分层：专属 0.7（L1）× 模型规则 ×0.8（L2）× 时段 ×0.5（L4）
    // → final = 0.28 落库。展示价 = 目录价 × f_c，算式倍率必须是 final/f_in = 0.56，
    // 否则 price×f_c×0.7 会漏掉模型规则的 0.8，合计与实扣不配平。
    const text = renderText(
      renderModelPrice({
        ...idleLog,
        group_ratio: 0.28, // 0.7 × 0.8 × 0.5
        user_group_ratio: 0.7,
      }),
    );
    // 分量价照旧按时段系数：输入 ¥1 / 输出 ¥4 / 缓存 ¥0.02
    expect(text).toContain('输入价格：¥1');
    expect(text).toContain('输出价格：¥4');
    // 复合终值 0.56 = 0.7 × 0.8，标签如实标「专属倍率×规则」而非冒充专属 0.7
    expect(text).toContain('专属倍率×规则 0.56');
    expect(text).not.toContain('专属倍率 0.7');
    // 合计 = (31×¥1 + 14×¥4)/1M × 0.56 = ¥0.00004872 = price × 0.7 × 0.8 × 0.5
    expect(text).toContain('¥0.00004872');
  });

  it('无时段的专属+模型规则同样配平：复合终值而非裸专属倍率', () => {
    // 同一分层在无时段日志上同样成立：专属 0.7 × 模型规则 0.8 → final 0.56。
    // 旧展示给「专属倍率 0.7」会让算式漏掉 0.8；现在恒用 final 终值。
    const { time_factors, ...noTime } = idleLog;
    const text = renderText(
      renderModelPrice({
        ...noTime,
        group_ratio: 0.56,
        user_group_ratio: 0.7,
      }),
    );
    expect(text).toContain('专属倍率×规则 0.56');
    expect(text).not.toContain('专属倍率 0.7');
  });

  it('timeFactorDisplay：缺省分量不随时段变、非分量项随 input', () => {
    const tfd = timeFactorDisplay({
      time_factors: { input: 0.5, output: 0.25 },
    });
    expect(tfd.fIn).toBe(0.5);
    expect(tfd.scale('input')).toBe(0.5);
    expect(tfd.scale('output')).toBe(0.25);
    // 名单内未配置 → 1（保持常规目录价）
    expect(tfd.scale('cache')).toBe(1);
    // 名单外加价项 → f_in（随整单倍率走）
    expect(tfd.scale('web_search')).toBe(0.5);
    expect(timeFactorDisplay({})).toBeNull();
    expect(timeFactorDisplay({ time_factors: {} })).toBeNull();
  });
});
