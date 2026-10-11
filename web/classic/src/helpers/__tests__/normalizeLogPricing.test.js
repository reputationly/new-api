import React from 'react';
import { describe, it, expect, beforeEach } from 'vitest';
import { render } from '@testing-library/react';
import { normalizeLogPricing, renderModelPrice, renderQuota } from '../render';

/**
 * F2：历史日志的定价字段在渲染前统一换算成人民币。
 *
 * 判定标记是 `other.price_unit === 'cny'`：
 *   - 有标记 → 新日志，字段本身就是人民币，原样返回；
 *   - 没有 → 历史数据，**含 V0 与 V1 两代**：V1 那次改造只细化了 quota 单位，
 *     没有改比率语义，所以 V1 期日志的 model_ratio 仍是「× 2 后为 $/百万 tokens」，
 *     各 *_price / video_unit_price 仍是美元。
 * 换算只能用当前汇率（日志里没冻结当时的汇率）。
 *
 * 这个文件同时钉住 S4 的不变量：计费过程合计（9 位）等于该日志的花费列。
 */

const RATE = 7.3; // 站点美元汇率
const QPY = 1000000000; // 1 元 = 10⁹ quota

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem('quota_per_yuan', String(QPY));
  localStorage.setItem('quota_display_type', 'CNY');
  localStorage.setItem('status', JSON.stringify({ usd_exchange_rate: RATE }));
});

// ¥2/百万 tokens 在历史日志里的存法：$2/百万 ÷ 2 = ratio 0.13698630137
const OLD_RATIO_FOR_2_CNY = 2 / (2 * RATE);

describe('normalizeLogPricing：新旧两代日志', () => {
  it('带 price_unit: cny 的新日志原样返回（同一个对象，不做任何换算）', () => {
    const other = {
      price_unit: 'cny',
      model_ratio: 2,
      model_price: 0.2,
      video_unit_price: 0.5,
      completion_ratio: 4,
    };
    const out = normalizeLogPricing(other);
    expect(out).toBe(other);
    expect(out.model_ratio).toBe(2);
    expect(out.model_price).toBe(0.2);
    expect(out.video_unit_price).toBe(0.5);
  });

  // V0（1 quota = ¥0.000001 之前）与 V1（1 quota = ¥0.000001）的 model_ratio 都是
  // 美元口径，两条都走同一套换算
  it.each([
    ['V0 期日志', { model_ratio: OLD_RATIO_FOR_2_CNY, completion_ratio: 4 }],
    [
      'V1 期日志',
      {
        model_ratio: OLD_RATIO_FOR_2_CNY,
        completion_ratio: 4,
        cache_ratio: 0.25,
        is_stream: true,
      },
    ],
  ])('%s：倍率换算成 ¥/百万 tokens', (_label, other) => {
    const out = normalizeLogPricing(other);
    expect(out.model_ratio).toBeCloseTo(2, 9);
    expect(out.completion_ratio).toBe(4); // 无量纲倍数不动
    expect(out.price_unit).toBeUndefined();
  });

  it('绝对价格字段乘汇率，其余字段原样保留', () => {
    const out = normalizeLogPricing({
      model_price: -1,
      web_search_price: 10,
      file_search_price: 2.5,
      audio_input_price: 1,
      image_generation_call_price: 0.04,
      video_unit_price: 0.5,
      base_amount: 0.05,
      cache_tokens: 123,
      ws: true,
    });
    // -1 是「未配置按次价」的哨兵，不能乘汇率（走倍率计费的判据）
    expect(out.model_price).toBe(-1);
    expect(out.web_search_price).toBeCloseTo(73, 9);
    expect(out.file_search_price).toBeCloseTo(18.25, 9);
    expect(out.audio_input_price).toBeCloseTo(7.3, 9);
    expect(out.image_generation_call_price).toBeCloseTo(0.292, 9);
    expect(out.video_unit_price).toBeCloseTo(3.65, 9);
    expect(out.base_amount).toBeCloseTo(0.365, 9);
    expect(out.cache_tokens).toBe(123);
    expect(out.ws).toBe(true);
  });

  it('0 与负数保持原样（免费 / 哨兵），非数字保持原样', () => {
    const out = normalizeLogPricing({
      model_price: 0,
      web_search_price: 0,
      base_amount: null,
    });
    expect(out.model_price).toBe(0);
    expect(out.web_search_price).toBe(0);
    expect(out.base_amount).toBeNull();
  });

  it('不就地修改入参（渲染层多次读取同一份 other 不会重复放大）', () => {
    const other = { model_ratio: OLD_RATIO_FOR_2_CNY, web_search_price: 10 };
    const out = normalizeLogPricing(other);
    expect(other.model_ratio).toBe(OLD_RATIO_FOR_2_CNY);
    expect(other.web_search_price).toBe(10);
    expect(out).not.toBe(other);
    // 幂等性靠「每次从原始 other 重算」保证，重复调用得到同一个结果
    expect(normalizeLogPricing(other)).toEqual(out);
  });

  it('分段表达式：v1 是美元口径（系数要乘汇率），v2 已是人民币', () => {
    const enc = (s) => Buffer.from(s, 'utf8').toString('base64');
    const v1 = normalizeLogPricing({ expr_b64: enc('tier("base", p * 2)') });
    expect(v1.expr_price_scale).toBeCloseTo(RATE, 9);
    const v2 = normalizeLogPricing({
      expr_b64: enc('v2:tier("base", p * 14.6)'),
    });
    expect(v2.expr_price_scale).toBe(1);
  });

  it('非对象输入原样返回，不抛异常', () => {
    expect(normalizeLogPricing(null)).toBeNull();
    expect(normalizeLogPricing(undefined)).toBeUndefined();
    expect(normalizeLogPricing([])).toEqual([]);
  });
});

describe('历史日志：计费过程合计 == 花费列（9 位）', () => {
  // E1 同款请求在 V1 期落库：other 是美元口径，logs.quota 已经是迁移后的 quota
  const quota = 16583000; // = ¥0.016583

  const renderText = (node) =>
    render(React.createElement('div', null, node)).container.textContent;

  it.each([['V0 期'], ['V1 期']])('%s历史日志：合计与花费列逐位一致', () => {
    const other = normalizeLogPricing({
      model_ratio: OLD_RATIO_FOR_2_CNY,
      completion_ratio: 4,
      group_ratio: 0.5,
      model_price: -1,
    });
    const text = renderText(
      renderModelPrice({
        ...other,
        prompt_tokens: 199,
        completion_tokens: 4096,
        displayMode: 'price',
      }),
    );
    expect(text).toContain('= ¥0.016583000');
    expect(renderQuota(quota, 9)).toBe('¥0.016583000');
  });
});
