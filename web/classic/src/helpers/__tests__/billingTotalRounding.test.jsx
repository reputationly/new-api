import React from 'react';
import { describe, it, expect, beforeEach } from 'vitest';
import { render } from '@testing-library/react';
import {
  roundToDisplay,
  renderModelPrice,
  renderAudioModelPrice,
  renderClaudeModelPrice,
  renderVideoMatrixPriceSimple,
  buildVideoMatrixDetailRows,
  renderQuotaTrimmed,
} from '../render';

/**
 * F2 / F5：计费过程的「合计」按人民币 quota 网格四舍五入（远离零），与花费列逐位一致。
 *
 * 1 quota = ¥0.000000001，后端把 quota 取整到整数，所以合计必须落在同一张网格上：
 * 若只做 `(金额).toFixed(9)`，二进制近似会把落在边界上的金额推向错误的一侧。
 *
 * 计费过程里的定价字段（model_ratio / model_price / 视频单价）现在都是人民币，
 * 不再有 × 2 与 × 汇率。
 *
 * 断言口径统一为：合计 === renderQuotaTrimmed(round(人民币金额 × 10⁹))。
 */

const QPY = 1000000000; // 1 元 = 10⁹ quota（后端 QuotaPerYuan）

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem('quota_per_yuan', String(QPY));
  localStorage.setItem('quota_display_type', 'CNY');
});

const renderText = (node) => render(<div>{node}</div>).container.textContent;

// 取最后一处「= ¥金额」：单价行没有等号，合计行才有
const totalText = (text) => {
  const matches = [...String(text).matchAll(/=\s*¥(-?[\d.]+)/g)];
  expect(matches.length).toBeGreaterThan(0);
  return matches[matches.length - 1][1];
};

// 与花费列（renderQuotaTrimmed(quota)）逐位一致。
// quota = 后端把人民币金额取整到 10⁻⁹（远离零）；后端用精确十进制，所以这里也用
// 同一条规则算 quota —— 直接用 Math.round(浮点 × 10⁹) 会在 .5 边界上给出少 1 的结果，
// 那正是本次要修的现象。
const chargeColumnText = (amountCny) => {
  const quota = Math.round(roundToDisplay(amountCny, 9) * 1e9);
  return renderQuotaTrimmed(quota).slice(1);
};

const expectTotalMatchesChargeColumn = (text, amountCny) => {
  expect(totalText(text)).toBe(chargeColumnText(amountCny));
};

describe('roundToDisplay', () => {
  it('默认 9 位：四舍五入、远离零、带浮点容差', () => {
    expect(roundToDisplay(0.0165835)).toBe(0.0165835);
    expect(roundToDisplay(0.0000000005)).toBe(0.000000001);
    expect(roundToDisplay(0.00000000049)).toBe(0);
    expect(roundToDisplay(-0.0000000005)).toBe(-0.000000001);
    expect(roundToDisplay(1.0000000005)).toBe(1.000000001);
  });

  it('返回的是数值，不是定宽字符串（定宽由调用方 toFixed）', () => {
    expect(typeof roundToDisplay(0.0000000005)).toBe('number');
    expect(roundToDisplay(0.0000000005).toFixed(9)).toBe('0.000000001');
    expect(roundToDisplay(0.5e-9).toFixed(9)).toBe('0.000000001');
  });

  it('位数可调，且同样远离零', () => {
    expect(roundToDisplay(0.0165835, 6)).toBe(0.016584);
    expect(roundToDisplay(0.0165825, 6)).toBe(0.016583);
    expect(roundToDisplay(1.005, 2)).toBe(1.01);
    expect(roundToDisplay(2.675, 2)).toBe(2.68);
    expect(roundToDisplay(-1.005, 2)).toBe(-1.01);
  });

  it('零与非有限值原样返回', () => {
    expect(Object.is(roundToDisplay(0), 0)).toBe(true);
    expect(typeof roundToDisplay(0)).toBe('number');
    expect(Number.isNaN(roundToDisplay(NaN))).toBe(true);
    expect(roundToDisplay(Infinity)).toBe(Infinity);
  });
});

describe('F2.1-1 renderModelPrice 的合计', () => {
  // E1：deepseek 输入 ¥2/百万 tokens、输出 ×4、分组 0.5，prompt 199 / completion 4096
  const modelRatio = 2;
  const completionRatio = 4;

  it('按 token 分支（E1）：199 / 4096 tokens → 合计 ¥0.016583', () => {
    const text = renderText(
      renderModelPrice({
        prompt_tokens: 199,
        completion_tokens: 4096,
        model_ratio: modelRatio,
        completion_ratio: completionRatio,
        group_ratio: 0.5,
        model_price: -1,
        displayMode: 'price',
      }),
    );
    // (199 + 4096 × 4) × 2 × 1000 × 0.5 = 16,583,000 quota = ¥0.016583
    const amountCny =
      (199 / 1e6) * modelRatio * 0.5 +
      (4096 / 1e6) * modelRatio * completionRatio * 0.5;
    expect(amountCny * QPY).toBeCloseTo(16583000, 6);
    expectTotalMatchesChargeColumn(text, amountCny);
    expect(totalText(text)).toBe('0.016583');
    expect(renderQuotaTrimmed(16583000)).toBe('¥0.016583');
  });

  it('按 token 分支：缓存命中与分组倍率一起进合计', () => {
    const opts = {
      prompt_tokens: 12345,
      completion_tokens: 6789,
      model_ratio: 1.369863013699,
      completion_ratio: 3,
      cache_tokens: 4321,
      cache_ratio: 0.25,
      group_ratio: 1.7,
      model_price: -1,
      displayMode: 'price',
    };
    const text = renderText(renderModelPrice(opts));
    const amountCny =
      ((opts.prompt_tokens -
        opts.cache_tokens +
        opts.cache_tokens * opts.cache_ratio) /
        1e6) *
        opts.model_ratio *
        opts.group_ratio +
      (opts.completion_tokens / 1e6) *
        opts.model_ratio *
        opts.completion_ratio *
        opts.group_ratio;
    expectTotalMatchesChargeColumn(text, amountCny);
  });

  it('按次分支（E5）：合计 = 按次价 × 分组倍率，按人民币 9 位舍入', () => {
    const opts = {
      prompt_tokens: 100,
      completion_tokens: 100,
      model_price: 0.2,
      group_ratio: 0.333,
      displayMode: 'price',
    };
    const text = renderText(renderModelPrice(opts));
    expectTotalMatchesChargeColumn(text, opts.model_price * opts.group_ratio);
  });

  // 边界样例：人民币金额正好落在第 10 位小数的 .5 上。
  // 2500 tokens × ¥0.0000002/百万 × 1000 = 0.5 quota → 1 quota = ¥0.000000001。
  it('按 token 分支：.5 边界向上（合计 ¥0.000000001）', () => {
    const opts = {
      prompt_tokens: 2500,
      completion_tokens: 0,
      model_ratio: 0.0000002,
      completion_ratio: 4,
      group_ratio: 1,
      model_price: -1,
      displayMode: 'price',
    };
    const text = renderText(renderModelPrice(opts));
    const amountCny = (2500 / 1e6) * 0.0000002;
    expect(amountCny * QPY).toBeCloseTo(0.5, 6);
    expectTotalMatchesChargeColumn(text, amountCny);
    expect(totalText(text)).toBe('0.000000001');
  });

  it('按次分支：.5 边界向上（合计 ¥0.000000001）', () => {
    const opts = {
      model_price: 0.000000001,
      group_ratio: 0.5,
      displayMode: 'price',
    };
    const text = renderText(renderModelPrice(opts));
    const amountCny = 0.000000001 * 0.5;
    expect(amountCny * QPY).toBeCloseTo(0.5, 6);
    expectTotalMatchesChargeColumn(text, amountCny);
    expect(totalText(text)).toBe('0.000000001');
  });
});

describe('F2.1-2 renderAudioModelPrice 的合计', () => {
  it('按次分支', () => {
    const opts = {
      model_price: 0.2,
      group_ratio: 0.333,
      displayMode: 'price',
    };
    const text = renderText(renderAudioModelPrice(opts));
    expectTotalMatchesChargeColumn(text, opts.model_price * opts.group_ratio);
  });

  it('按 token 分支：文字部分 + 音频部分', () => {
    const opts = {
      prompt_tokens: 1200,
      completion_tokens: 800,
      model_ratio: 0.13698630137,
      completion_ratio: 4,
      cache_tokens: 200,
      cache_ratio: 0.5,
      audio_ratio: 2,
      audio_completion_ratio: 3,
      audio_input: 400,
      audio_output: 200,
      group_ratio: 0.5,
      model_price: -1,
      displayMode: 'price',
    };
    const text = renderText(renderAudioModelPrice(opts));
    const inputRatioPrice = opts.model_ratio;
    const completionRatioPrice = opts.model_ratio * opts.completion_ratio;
    const textPrice =
      ((opts.prompt_tokens -
        opts.cache_tokens +
        opts.cache_tokens * opts.cache_ratio) /
        1e6) *
        inputRatioPrice *
        opts.group_ratio +
      (opts.completion_tokens / 1e6) * completionRatioPrice * opts.group_ratio;
    const audioPrice =
      (opts.audio_input / 1e6) *
        inputRatioPrice *
        opts.audio_ratio *
        opts.group_ratio +
      (opts.audio_output / 1e6) *
        inputRatioPrice *
        opts.audio_ratio *
        opts.audio_completion_ratio *
        opts.group_ratio;
    expectTotalMatchesChargeColumn(text, textPrice + audioPrice);
  });

  it('按 token 分支：.5 边界向上（合计 ¥0.000000256，不是 ¥0.000000255）', () => {
    const opts = {
      prompt_tokens: 2555,
      completion_tokens: 0,
      model_ratio: 0.0001,
      completion_ratio: 4,
      group_ratio: 1,
      model_price: -1,
      displayMode: 'price',
    };
    const text = renderText(renderAudioModelPrice(opts));
    const amountCny = (2555 / 1e6) * 0.0001;
    expect(amountCny * QPY).toBeCloseTo(255.5, 6);
    expectTotalMatchesChargeColumn(text, amountCny);
    expect(totalText(text)).toBe('0.000000256');
  });

  it('按次分支：.5 边界向上（合计 ¥0.000000256）', () => {
    const text = renderText(
      renderAudioModelPrice({
        model_price: 0.000000511,
        group_ratio: 0.5,
        displayMode: 'price',
      }),
    );
    expectTotalMatchesChargeColumn(text, 0.000000511 * 0.5);
    expect(totalText(text)).toBe('0.000000256');
  });
});

describe('F2.1-3 renderClaudeModelPrice 的合计', () => {
  it('按次分支', () => {
    const opts = {
      model_price: 0.2,
      group_ratio: 0.333,
      displayMode: 'price',
    };
    const text = renderText(renderClaudeModelPrice(opts));
    expectTotalMatchesChargeColumn(text, opts.model_price * opts.group_ratio);
  });

  it('分项算式分支：缓存命中 + 5m / 1h 缓存创建一起进合计', () => {
    const opts = {
      prompt_tokens: 1000,
      completion_tokens: 1000,
      model_ratio: 0.13698630137,
      completion_ratio: 4,
      cache_tokens: 300,
      cache_ratio: 0.5,
      cache_creation_tokens_5m: 100,
      cache_creation_ratio_5m: 1.25,
      cache_creation_tokens_1h: 200,
      cache_creation_ratio_1h: 2,
      group_ratio: 0.5,
      model_price: -1,
      displayMode: 'price',
    };
    const text = renderText(renderClaudeModelPrice(opts));
    const inputRatioPrice = opts.model_ratio;
    const completionRatioPrice = opts.model_ratio * opts.completion_ratio;
    const effectiveInputTokens =
      opts.prompt_tokens +
      opts.cache_tokens * opts.cache_ratio +
      opts.cache_creation_tokens_5m * opts.cache_creation_ratio_5m +
      opts.cache_creation_tokens_1h * opts.cache_creation_ratio_1h;
    const amountCny =
      (effectiveInputTokens / 1e6) * inputRatioPrice * opts.group_ratio +
      (opts.completion_tokens / 1e6) * completionRatioPrice * opts.group_ratio;
    expectTotalMatchesChargeColumn(text, amountCny);
  });

  it('分项算式分支：.5 边界向上（合计 ¥0.000000026，不是 ¥0.000000025）', () => {
    const opts = {
      prompt_tokens: 2555,
      completion_tokens: 0,
      model_ratio: 0.00001,
      completion_ratio: 4,
      group_ratio: 1,
      model_price: -1,
      displayMode: 'price',
    };
    const text = renderText(renderClaudeModelPrice(opts));
    const amountCny = (2555 / 1e6) * 0.00001;
    expect(amountCny * QPY).toBeCloseTo(25.55, 6);
    expectTotalMatchesChargeColumn(text, amountCny);
    expect(totalText(text)).toBe('0.000000026');
  });

  it('按次分支：.5 边界向上（合计 ¥0.000000256）', () => {
    const text = renderText(
      renderClaudeModelPrice({
        model_price: 0.000000511,
        group_ratio: 0.5,
        displayMode: 'price',
      }),
    );
    expectTotalMatchesChargeColumn(text, 0.000000511 * 0.5);
    expect(totalText(text)).toBe('0.000000256');
  });
});

describe('F2.1-4 buildVideoMatrixDetailRows 的计费过程行', () => {
  const processRowText = (rows) => {
    const row = rows.find((r) => String(r.value).includes('='));
    expect(row).toBeTruthy();
    return row.value;
  };

  it('按秒：单价 × 时长 × 倍率', () => {
    const opts = {
      video_price_mode: 'per_second',
      video_unit_price: 0.5,
      video_seconds: 10,
      group_ratio: 0.5,
    };
    const text = processRowText(buildVideoMatrixDetailRows(opts));
    expectTotalMatchesChargeColumn(text, 0.5 * 10 * 0.5);
  });

  it('按 token：tokens / 1M × 单价 × 倍率', () => {
    const opts = {
      video_price_mode: 'token',
      video_unit_price: 46,
      completion_tokens: 12345,
      group_ratio: 0.5,
    };
    const text = processRowText(buildVideoMatrixDetailRows(opts));
    expectTotalMatchesChargeColumn(text, (12345 / 1e6) * 46 * 0.5);
  });

  it('按次：单价 × 倍率', () => {
    const opts = {
      video_price_mode: 'per_call',
      video_unit_price: 1,
      group_ratio: 0.5,
    };
    const text = processRowText(buildVideoMatrixDetailRows(opts));
    expectTotalMatchesChargeColumn(text, 1 * 0.5);
  });

  // 边界样例：5 tokens × ¥0.0001/1M = 0.5 quota → 向上 1 quota = ¥0.000000001
  it('按 token：.5 边界向上（合计 ¥0.000000001，不是 ¥0）', () => {
    const opts = {
      video_price_mode: 'token',
      video_unit_price: 0.0001,
      completion_tokens: 5,
      group_ratio: 1,
    };
    const text = processRowText(buildVideoMatrixDetailRows(opts));
    expect((5 / 1e6) * 0.0001 * QPY).toBeCloseTo(0.5, 9);
    expectTotalMatchesChargeColumn(text, (5 / 1e6) * 0.0001);
    expect(totalText(text)).toBe('0.000000001');
  });

  // 边界样例：¥0.000000001 / 次 × 0.5 = 0.5 quota → 向上 1 quota
  it('按次：.5 边界向上（合计 ¥0.000000001，不是 ¥0）', () => {
    const opts = {
      video_price_mode: 'per_call',
      video_unit_price: 0.000000001,
      group_ratio: 0.5,
    };
    const text = processRowText(buildVideoMatrixDetailRows(opts));
    expect(0.000000001 * 0.5 * QPY).toBeCloseTo(0.5, 9);
    expectTotalMatchesChargeColumn(text, 0.000000001 * 0.5);
    expect(totalText(text)).toBe('0.000000001');
  });

  // 边界样例：¥0.000000511 / 秒 × 1 秒 × 0.5 = 255.5 quota → 256
  it('按秒：.5 边界向上（合计 ¥0.000000256，不是 ¥0.000000255）', () => {
    const opts = {
      video_price_mode: 'per_second',
      video_unit_price: 0.000000511,
      video_seconds: 1,
      group_ratio: 0.5,
    };
    const text = processRowText(buildVideoMatrixDetailRows(opts));
    expect((0.000000511 * 0.5 * QPY).toFixed(2)).not.toBe('0.00');
    expect(0.000000511 * 0.5 * QPY).toBeCloseTo(255.5, 6);
    expectTotalMatchesChargeColumn(text, 0.000000511 * 1 * 0.5);
    expect(totalText(text)).toBe('0.000000256');
  });
});

describe('F2.1-5 renderVideoMatrixPriceSimple 的算式金额', () => {
  const formulaText = (segments) => {
    const segment = segments.find((s) => String(s.text).includes('='));
    expect(segment).toBeTruthy();
    return segment.text;
  };

  // 第 5 项保留「去掉末尾 0」的写法：金额过 Number() 再拼字符串
  const compactAmount = (amountCny) =>
    String(Number(chargeColumnText(amountCny)));

  it('按秒：金额去掉末尾 0', () => {
    const opts = {
      video_price_mode: 'per_second',
      video_unit_price: 0.5,
      video_seconds: 10,
      group_ratio: 0.5,
    };
    const text = formulaText(renderVideoMatrixPriceSimple(opts));
    expect(text.endsWith(`= ¥${compactAmount(0.5 * 10 * 0.5)}`)).toBe(true);
  });

  it('按 token：金额去掉末尾 0', () => {
    const opts = {
      video_price_mode: 'token',
      video_unit_price: 46,
      completion_tokens: 12345,
      group_ratio: 0.5,
    };
    const text = formulaText(renderVideoMatrixPriceSimple(opts));
    expect(text.endsWith(`= ¥${compactAmount((12345 / 1e6) * 46 * 0.5)}`)).toBe(
      true,
    );
  });

  // 第 5 项既要去掉末尾 0，也要按新的舍入规则取金额：
  // 5 tokens × ¥0.0002/1M × 0.5 = 0.5 quota → 向上 1 quota → ¥0.000000001
  it('按 token：.5 边界向上后仍是去掉末尾 0 的写法', () => {
    const opts = {
      video_price_mode: 'token',
      video_unit_price: 0.0002,
      completion_tokens: 5,
      group_ratio: 0.5,
    };
    const text = formulaText(renderVideoMatrixPriceSimple(opts));
    expect((5 / 1e6) * 0.0002 * 0.5 * QPY).toBeCloseTo(0.5, 9);
    expect(text.endsWith('= ¥0.000000001')).toBe(true);
  });

  it('按秒：.5 边界向上（¥0.000000256，不是 ¥0.000000255）', () => {
    const opts = {
      video_price_mode: 'per_second',
      video_unit_price: 0.000000511,
      video_seconds: 1,
      group_ratio: 0.5,
    };
    const text = formulaText(renderVideoMatrixPriceSimple(opts));
    expect(text.endsWith('= ¥0.000000256')).toBe(true);
  });
});

describe('TOKENS 展示模式：合计显示成 quota 数，与花费列同口径', () => {
  it('E1 的合计 = 16,583,000 quota（= 花费列的显示）', () => {
    localStorage.setItem('quota_display_type', 'TOKENS');
    const text = renderText(
      renderModelPrice({
        prompt_tokens: 199,
        completion_tokens: 4096,
        model_ratio: 2,
        completion_ratio: 4,
        group_ratio: 0.5,
        model_price: -1,
        displayMode: 'price',
      }),
    );
    const amountCny = (199 / 1e6) * 2 * 0.5 + (4096 / 1e6) * 2 * 4 * 0.5;
    expect(Math.round(amountCny * QPY)).toBe(16583000);
    expect(text).toContain(`合计：${renderQuotaTrimmed(16583000)}`);
  });
});

// 固定种子（mulberry32），失败时能原样重跑
const seededRandom = (seed) => () => {
  seed = (seed + 0x6d2b79f5) | 0;
  let t = Math.imul(seed ^ (seed >>> 15), 1 | seed);
  t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
  return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
};

describe('F2.1-1 renderModelPrice 的 20 组随机用例', () => {
  const rand = seededRandom(20261010);
  const pick = (min, max) => min + rand() * (max - min);
  const cases = Array.from({ length: 20 }, (_, i) => ({
    index: i + 1,
    prompt_tokens: 1 + Math.floor(pick(0, 20000)),
    completion_tokens: 1 + Math.floor(pick(0, 20000)),
    model_ratio: Number(pick(0.05, 3).toFixed(12)),
    completion_ratio: Number(pick(1, 7).toFixed(6)),
    group_ratio: Number(pick(0.1, 1.9).toFixed(6)),
  }));

  for (const c of cases) {
    it(`用例 ${c.index}：合计与花费列逐位一致`, () => {
      const text = renderText(renderModelPrice({ ...c, model_price: -1 }));
      const amountCny =
        (c.prompt_tokens / 1e6) * c.model_ratio * c.group_ratio +
        (c.completion_tokens / 1e6) *
          c.model_ratio *
          c.completion_ratio *
          c.group_ratio;
      expectTotalMatchesChargeColumn(text, amountCny);
    });
  }
});
