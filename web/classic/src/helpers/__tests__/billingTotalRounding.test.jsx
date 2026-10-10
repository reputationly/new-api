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
  renderQuota,
} from '../render';

/**
 * F2：计费过程的「合计」按人民币 6 位四舍五入（远离零），与花费列逐位一致。
 *
 * 后端把 quota 四舍五入到整数，切换后 1 quota = ¥0.000001；前端合计若只做
 * `(usd × rate).toFixed(6)`，二进制近似会把落在边界上的金额推向错误的一侧，
 * 于是用户看到「计费过程 ¥0.016583 / 花费 ¥0.016586」这类对不上的账。
 *
 * 断言口径统一为：合计 === renderQuota(round(人民币金额 × 10⁶), 6)。
 */

const RATE = 7.3; // ¥/$
const QPU = 7300000; // 切换后的 QuotaPerUnit

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem('quota_per_unit', String(QPU));
  localStorage.setItem('quota_display_type', 'CNY');
  localStorage.setItem('status', JSON.stringify({ usd_exchange_rate: RATE }));
});

const renderText = (node) => render(<div>{node}</div>).container.textContent;

// 取最后一处「= ¥金额」：单价行没有等号，合计行才有
const totalText = (text) => {
  const matches = [...String(text).matchAll(/=\s*¥(-?[\d.]+)/g)];
  expect(matches.length).toBeGreaterThan(0);
  return matches[matches.length - 1][1];
};

// 与花费列（renderQuota(quota, 6)）逐位一致。
// quota = 后端把人民币金额取整到 10⁻⁶（远离零）；后端用精确十进制，所以这里也用
// 同一条规则算 quota —— 直接用 Math.round(浮点 × 10⁶) 会在 .5 边界上给出少 1 的结果，
// 那正是本次要修的现象。
const chargeColumnText = (amountCny) => {
  const quota = Math.round(roundToDisplay(amountCny, 6) * 1e6);
  return renderQuota(quota, 6).slice(1);
};

const expectTotalMatchesChargeColumn = (text, amountCny) => {
  expect(totalText(text)).toBe(chargeColumnText(amountCny));
};

describe('roundToDisplay', () => {
  it('F2.4 的五个样例：四舍五入、远离零、带浮点容差', () => {
    expect(roundToDisplay(0.0165835)).toBe(0.016584);
    expect(roundToDisplay(0.0165825)).toBe(0.016583);
    expect(roundToDisplay(-0.0000005)).toBe(-0.000001);
    expect(roundToDisplay(1.0000005)).toBe(1.000001);
    expect(roundToDisplay(0.1 + 0.2)).toBe(0.3);
  });

  it('返回的是数值，不是定宽字符串（定宽由调用方 toFixed）', () => {
    expect(typeof roundToDisplay(0.0165835)).toBe('number');
    expect(roundToDisplay(0.0165835).toFixed(6)).toBe('0.016584');
    expect(roundToDisplay(0.5e-6).toFixed(6)).toBe('0.000001');
  });

  it('位数可调，且同样远离零', () => {
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
  // 用户 junm1888 的截图样例：输入 199 / 输出 4096，输入 ¥2 / 1M、输出 ¥8 / 1M，分组 0.5
  const modelRatio = 2 / (2 * RATE); // ¥2 / 1M → model_ratio = 2 ÷ (2 × 汇率)
  const completionRatio = 4; // ¥8 / 1M → 补全倍率 4

  it('按 token 分支：199 / 4096 tokens → 合计 ¥0.016583', () => {
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
    const amountUsd =
      (199 / 1e6) * (modelRatio * 2) * 0.5 +
      (4096 / 1e6) * (modelRatio * 2 * completionRatio) * 0.5;
    expectTotalMatchesChargeColumn(text, amountUsd * RATE);
    expect(totalText(text)).toBe('0.016583');
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
    const amountUsd =
      ((opts.prompt_tokens -
        opts.cache_tokens +
        opts.cache_tokens * opts.cache_ratio) /
        1e6) *
        (opts.model_ratio * 2) *
        opts.group_ratio +
      (opts.completion_tokens / 1e6) *
        (opts.model_ratio * 2 * opts.completion_ratio) *
        opts.group_ratio;
    expectTotalMatchesChargeColumn(text, amountUsd * RATE);
  });

  it('按次分支：合计 = 按次价 × 分组倍率，按人民币 6 位舍入', () => {
    const opts = {
      prompt_tokens: 100,
      completion_tokens: 100,
      model_price: 0.0002,
      group_ratio: 0.333,
      displayMode: 'price',
    };
    const text = renderText(renderModelPrice(opts));
    expectTotalMatchesChargeColumn(
      text,
      opts.model_price * opts.group_ratio * RATE,
    );
  });

  // 边界样例：人民币金额正好落在第 6 位小数的 .5 上。这里的具体值是
  // 0.0002555（=¥0.0002555 → 256 quota）与 0.0000365（→ 37 quota），
  // 旧实现 (amount × rate).toFixed(6) 给出 0.000255 / 0.000036，比花费列少 1 quota。
  it('按 token 分支：.5 边界向上（合计 ¥0.000256，不是 ¥0.000255）', () => {
    const opts = {
      prompt_tokens: 150,
      completion_tokens: 50,
      model_ratio: 0.1,
      completion_ratio: 4,
      group_ratio: 0.5,
      model_price: -1,
      displayMode: 'price',
    };
    const text = renderText(renderModelPrice(opts));
    const amountUsd = (150 / 1e6) * 0.2 * 0.5 + (50 / 1e6) * 0.2 * 4 * 0.5;
    expect(amountUsd * RATE).toBeCloseTo(0.0002555, 12);
    expectTotalMatchesChargeColumn(text, amountUsd * RATE);
    expect(totalText(text)).toBe('0.000256');
  });

  it('按次分支：.5 边界向上（合计 ¥0.000037，不是 ¥0.000036）', () => {
    const opts = {
      model_price: 0.000005,
      group_ratio: 1,
      displayMode: 'price',
    };
    const text = renderText(renderModelPrice(opts));
    const amountUsd = 0.000005 * 1;
    expect(amountUsd * RATE).toBeCloseTo(0.0000365, 12);
    expectTotalMatchesChargeColumn(text, amountUsd * RATE);
    expect(totalText(text)).toBe('0.000037');
  });
});

describe('F2.1-2 renderAudioModelPrice 的合计', () => {
  it('按次分支', () => {
    const opts = {
      model_price: 0.0002,
      group_ratio: 0.333,
      displayMode: 'price',
    };
    const text = renderText(renderAudioModelPrice(opts));
    expectTotalMatchesChargeColumn(
      text,
      opts.model_price * opts.group_ratio * RATE,
    );
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
    const inputRatioPrice = opts.model_ratio * 2;
    const completionRatioPrice = opts.model_ratio * 2 * opts.completion_ratio;
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
    expectTotalMatchesChargeColumn(text, (textPrice + audioPrice) * RATE);
  });

  it('按 token 分支：.5 边界向上（合计 ¥0.000694，不是 ¥0.000693）', () => {
    const opts = {
      prompt_tokens: 550,
      completion_tokens: 0,
      model_ratio: 0.1,
      completion_ratio: 4,
      audio_ratio: 2,
      audio_input: 200,
      group_ratio: 0.5,
      model_price: -1,
      displayMode: 'price',
    };
    const text = renderText(renderAudioModelPrice(opts));
    const inputRatioPrice = 0.1 * 2;
    const amountUsd =
      (550 / 1e6) * inputRatioPrice * 0.5 +
      (200 / 1e6) * inputRatioPrice * 2 * 0.5;
    expect(amountUsd * RATE).toBeCloseTo(0.0006935, 12);
    expectTotalMatchesChargeColumn(text, amountUsd * RATE);
    expect(totalText(text)).toBe('0.000694');
  });

  it('按次分支：.5 边界向上（合计 ¥0.000037）', () => {
    const text = renderText(
      renderAudioModelPrice({
        model_price: 0.000005,
        group_ratio: 1,
        displayMode: 'price',
      }),
    );
    expectTotalMatchesChargeColumn(text, 0.000005 * RATE);
    expect(totalText(text)).toBe('0.000037');
  });
});

describe('F2.1-3 renderClaudeModelPrice 的合计', () => {
  it('按次分支', () => {
    const opts = {
      model_price: 0.0002,
      group_ratio: 0.333,
      displayMode: 'price',
    };
    const text = renderText(renderClaudeModelPrice(opts));
    expectTotalMatchesChargeColumn(
      text,
      opts.model_price * opts.group_ratio * RATE,
    );
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
    const inputRatioPrice = opts.model_ratio * 2;
    const completionRatioPrice = opts.model_ratio * 2 * opts.completion_ratio;
    const effectiveInputTokens =
      opts.prompt_tokens +
      opts.cache_tokens * opts.cache_ratio +
      opts.cache_creation_tokens_5m * opts.cache_creation_ratio_5m +
      opts.cache_creation_tokens_1h * opts.cache_creation_ratio_1h;
    const amountUsd =
      (effectiveInputTokens / 1e6) * inputRatioPrice * opts.group_ratio +
      (opts.completion_tokens / 1e6) * completionRatioPrice * opts.group_ratio;
    expectTotalMatchesChargeColumn(text, amountUsd * RATE);
  });

  it('分项算式分支：.5 边界向上（合计 ¥0.000475，不是 ¥0.000474）', () => {
    const opts = {
      prompt_tokens: 600,
      completion_tokens: 0,
      model_ratio: 0.1,
      completion_ratio: 4,
      cache_tokens: 100,
      cache_ratio: 0.5,
      group_ratio: 0.5,
      model_price: -1,
      displayMode: 'price',
    };
    const text = renderText(renderClaudeModelPrice(opts));
    const inputRatioPrice = 0.1 * 2;
    const amountUsd = ((600 + 100 * 0.5) / 1e6) * inputRatioPrice * 0.5;
    expect(amountUsd * RATE).toBeCloseTo(0.0004745, 12);
    expectTotalMatchesChargeColumn(text, amountUsd * RATE);
    expect(totalText(text)).toBe('0.000475');
  });

  it('按次分支：.5 边界向上（合计 ¥0.000037）', () => {
    const text = renderText(
      renderClaudeModelPrice({
        model_price: 0.000005,
        group_ratio: 1,
        displayMode: 'price',
      }),
    );
    expectTotalMatchesChargeColumn(text, 0.000005 * RATE);
    expect(totalText(text)).toBe('0.000037');
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
      video_unit_price: 0.1,
      video_seconds: 10,
      group_ratio: 0.5,
    };
    const text = processRowText(buildVideoMatrixDetailRows(opts));
    expectTotalMatchesChargeColumn(text, 0.1 * 10 * 0.5 * RATE);
  });

  it('按 token：tokens / 1M × 单价 × 倍率', () => {
    const opts = {
      video_price_mode: 'token',
      video_unit_price: 8,
      completion_tokens: 12345,
      group_ratio: 0.5,
    };
    const text = processRowText(buildVideoMatrixDetailRows(opts));
    expectTotalMatchesChargeColumn(text, (12345 / 1e6) * 8 * 0.5 * RATE);
  });

  it('按次：单价 × 倍率', () => {
    const opts = {
      video_price_mode: 'per_call',
      video_unit_price: 0.2,
      group_ratio: 0.5,
    };
    const text = processRowText(buildVideoMatrixDetailRows(opts));
    expectTotalMatchesChargeColumn(text, 0.2 * 0.5 * RATE);
  });

  // 边界样例：¥46 / 1M × 0.5 × 5 tokens = ¥0.0008395 → 840 quota；
  // 旧实现 toFixed(6) 给 0.000839，比花费列少 1 quota。
  it('按 token：.5 边界向上（合计 ¥0.000840，不是 ¥0.000839）', () => {
    const opts = {
      video_price_mode: 'token',
      video_unit_price: 46,
      completion_tokens: 5,
      group_ratio: 0.5,
    };
    const text = processRowText(buildVideoMatrixDetailRows(opts));
    expect((5 / 1e6) * 46 * 0.5 * RATE).toBeCloseTo(0.0008395, 12);
    expectTotalMatchesChargeColumn(text, (5 / 1e6) * 46 * 0.5 * RATE);
    expect(totalText(text)).toBe('0.000840');
  });

  it('按次：.5 边界向上（合计 ¥0.000037，不是 ¥0.000036）', () => {
    const opts = {
      video_price_mode: 'per_call',
      video_unit_price: 0.000005,
      group_ratio: 1,
    };
    const text = processRowText(buildVideoMatrixDetailRows(opts));
    expectTotalMatchesChargeColumn(text, 0.000005 * RATE);
    expect(totalText(text)).toBe('0.000037');
  });

  it('按秒：.5 边界向上（合计 ¥0.000037，不是 ¥0.000036）', () => {
    const opts = {
      video_price_mode: 'per_second',
      video_unit_price: 0.000005,
      video_seconds: 1,
      group_ratio: 1,
    };
    const text = processRowText(buildVideoMatrixDetailRows(opts));
    expectTotalMatchesChargeColumn(text, 0.000005 * 1 * RATE);
    expect(totalText(text)).toBe('0.000037');
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
      video_unit_price: 0.1,
      video_seconds: 10,
      group_ratio: 0.5,
    };
    const text = formulaText(renderVideoMatrixPriceSimple(opts));
    expect(text.endsWith(`= ¥${compactAmount(0.1 * 10 * 0.5 * RATE)}`)).toBe(
      true,
    );
  });

  it('按 token：金额去掉末尾 0', () => {
    const opts = {
      video_price_mode: 'token',
      video_unit_price: 8,
      completion_tokens: 12345,
      group_ratio: 0.5,
    };
    const text = formulaText(renderVideoMatrixPriceSimple(opts));
    expect(
      text.endsWith(`= ¥${compactAmount((12345 / 1e6) * 8 * 0.5 * RATE)}`),
    ).toBe(true);
  });

  // 第 5 项既要去掉末尾 0，也要按新的舍入规则取金额：
  // ¥0.0008395 → 840 quota → ¥0.000840 → 去尾 0 后是 ¥0.00084（旧实现是 ¥0.000839）
  it('按 token：.5 边界向上后仍是去掉末尾 0 的写法', () => {
    const opts = {
      video_price_mode: 'token',
      video_unit_price: 46,
      completion_tokens: 5,
      group_ratio: 0.5,
    };
    const text = formulaText(renderVideoMatrixPriceSimple(opts));
    expect(text.endsWith('= ¥0.00084')).toBe(true);
  });

  it('按秒：.5 边界向上（¥0.000037，不是 ¥0.000036）', () => {
    const opts = {
      video_price_mode: 'per_second',
      video_unit_price: 0.000005,
      video_seconds: 1,
      group_ratio: 1,
    };
    const text = formulaText(renderVideoMatrixPriceSimple(opts));
    expect(text.endsWith('= ¥0.000037')).toBe(true);
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
      const amountUsd =
        (c.prompt_tokens / 1e6) * (c.model_ratio * 2) * c.group_ratio +
        (c.completion_tokens / 1e6) *
          (c.model_ratio * 2 * c.completion_ratio) *
          c.group_ratio;
      expectTotalMatchesChargeColumn(text, amountUsd * RATE);
    });
  }
});
