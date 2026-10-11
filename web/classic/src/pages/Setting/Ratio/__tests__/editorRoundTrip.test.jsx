import { describe, it, expect, vi, beforeEach } from 'vitest';
import { renderHook, act, waitFor } from '@testing-library/react';

vi.mock('../../../../helpers', async () => {
  const actual = await vi.importActual('../../../../helpers');
  return {
    ...actual,
    API: {
      get: vi.fn().mockResolvedValue({ data: { success: true, data: [] } }),
      put: vi.fn().mockResolvedValue({ data: { success: true } }),
      post: vi.fn(),
    },
    showSuccess: vi.fn(),
    showError: vi.fn(),
  };
});

import { API } from '../../../../helpers';
import { useModelPricingEditorState } from '../hooks/useModelPricingEditorState';

/**
 * F3：编辑器所见即所存（往返测试）。
 *
 * 价格输入框里填的就是人民币，保存时原样落到 ModelRatio / ModelPrice / 视频矩阵格子，
 * 不再「÷ 汇率 ÷ 2」。所以「读 → 不改 → 保存」提交的 JSON 必须与原 JSON 逐键相同：
 * 一旦某一侧多了或少了一次换算，运营打开编辑器不动任何东西再保存，价格就漂了。
 */

const t = (s, vars) =>
  vars ? s.replace(/\{\{(\w+)\}\}/g, (_, k) => vars[k]) : s;

const OPTIONS = {
  ModelRatio: JSON.stringify({
    'gpt-x': 2,
    cheap: 0.01,
    'ratio-with-tail': 0.13698630137,
  }),
  ModelPrice: JSON.stringify({
    'img-x': 0.2,
    'price-with-tail': 0.41095890411,
  }),
  CompletionRatio: JSON.stringify({ 'gpt-x': 4, cheap: 2 }),
  CacheRatio: JSON.stringify({ 'gpt-x': 0.25 }),
  CreateCacheRatio: '{}',
  ImageRatio: '{}',
  AudioRatio: '{}',
  AudioCompletionRatio: '{}',
  VideoPricingConfig: JSON.stringify({
    'vid-per-second': {
      mode: 'per_second',
      per_second: { '720p': 0.5, '1080p': 0.73 },
    },
    'vid-per-call': {
      mode: 'per_call',
      per_call: { '720p': { 5: 0.2, 10: 0.4 } },
    },
  }),
  'billing_setting.billing_mode': JSON.stringify({ 'tiered-x': 'tiered_expr' }),
  'billing_setting.billing_expr': JSON.stringify({
    'tiered-x': 'v2:tier("base", p * 14.6 + c * 29.2)',
  }),
};

const CANDIDATES = [
  'gpt-x',
  'cheap',
  'ratio-with-tail',
  'img-x',
  'price-with-tail',
  'vid-per-second',
  'vid-per-call',
  'tiered-x',
];

const refresh = vi.fn().mockResolvedValue(undefined);

const putPayloads = () => {
  const payload = {};
  // API.put(url, { key, value })
  for (const [, body] of API.put.mock.calls) {
    payload[body.key] = JSON.parse(body.value);
  }
  return payload;
};

const submitAndGetPayload = async () => {
  const { result } = renderHook(() =>
    useModelPricingEditorState({
      options: OPTIONS,
      refresh,
      t,
      candidateModelNames: CANDIDATES,
    }),
  );
  await waitFor(() => expect(result.current.selectedModel).toBeTruthy());
  await act(async () => {
    await result.current.handleSubmit();
  });
  return putPayloads();
};

beforeEach(() => {
  vi.clearAllMocks();
  localStorage.clear();
  localStorage.setItem('quota_per_yuan', '100000000');
  localStorage.setItem('quota_display_type', 'CNY');
});

describe('编辑器「读 → 不改 → 保存」往返', () => {
  it('倍率原样回写（¥/百万 tokens，不再 ÷ 汇率 ÷ 2）', async () => {
    const payload = await submitAndGetPayload();
    expect(payload.ModelRatio).toEqual(JSON.parse(OPTIONS.ModelRatio));
  });

  it('按次价原样回写（￥/次，不再 ÷ 汇率）', async () => {
    const payload = await submitAndGetPayload();
    expect(payload.ModelPrice).toEqual(JSON.parse(OPTIONS.ModelPrice));
  });

  it('派生倍率（补全 / 缓存）按比值回写，不漂', async () => {
    const payload = await submitAndGetPayload();
    expect(payload.CompletionRatio).toEqual({ 'gpt-x': 4, cheap: 2 });
    expect(payload.CacheRatio).toEqual({ 'gpt-x': 0.25 });
  });

  it('视频矩阵格子原样回写（结构与数值都逐键相同）', async () => {
    const payload = await submitAndGetPayload();
    expect(payload.VideoPricingConfig).toEqual(
      JSON.parse(OPTIONS.VideoPricingConfig),
    );
  });

  it('表达式带 v2: 前缀保存，且不会重复加前缀', async () => {
    const payload = await submitAndGetPayload();
    expect(payload['billing_setting.billing_expr']).toEqual({
      'tiered-x': 'v2:tier("base", p * 14.6 + c * 29.2)',
    });
    expect(payload['billing_setting.billing_mode']).toEqual({
      'tiered-x': 'tiered_expr',
    });
  });

  it('第二批：把 v1 旧表达式读进来再保存，会升级成 v2（系数不变，由运营核对）', async () => {
    const options = {
      ...OPTIONS,
      'billing_setting.billing_expr': JSON.stringify({
        'tiered-x': 'tier("base", p * 2 + c * 4)',
      }),
    };
    const { result } = renderHook(() =>
      useModelPricingEditorState({
        options,
        refresh,
        t,
        candidateModelNames: CANDIDATES,
      }),
    );
    await waitFor(() => expect(result.current.selectedModel).toBeTruthy());
    // 旧表达式在界面上带版本标记，编辑器据此提示「需要重录为 v2」
    const tiered = result.current.models.find((m) => m.name === 'tiered-x');
    expect(tiered.billingExprVersion).toBe(1);
    expect(tiered.billingExpr).toBe('tier("base", p * 2 + c * 4)');

    await act(async () => {
      await result.current.handleSubmit();
    });
    expect(putPayloads()['billing_setting.billing_expr']).toEqual({
      'tiered-x': 'v2:tier("base", p * 2 + c * 4)',
    });
  });

  it('已是 v2 的表达式读进来版本是 2，不提示重录', async () => {
    const { result } = renderHook(() =>
      useModelPricingEditorState({
        options: OPTIONS,
        refresh,
        t,
        candidateModelNames: CANDIDATES,
      }),
    );
    await waitFor(() => expect(result.current.selectedModel).toBeTruthy());
    const tiered = result.current.models.find((m) => m.name === 'tiered-x');
    expect(tiered.billingExprVersion).toBe(2);
    expect(tiered.billingExpr).toBe('tier("base", p * 14.6 + c * 29.2)');
  });

  it('价格输入框里显示的就是人民币原值（不做任何折算）', async () => {
    const { result } = renderHook(() =>
      useModelPricingEditorState({
        options: OPTIONS,
        refresh,
        t,
        candidateModelNames: CANDIDATES,
      }),
    );
    await waitFor(() => expect(result.current.selectedModel).toBeTruthy());
    const byName = (n) => result.current.models.find((m) => m.name === n);
    expect(byName('gpt-x').inputPrice).toBe('2');
    expect(byName('cheap').inputPrice).toBe('0.01');
    expect(byName('img-x').fixedPrice).toBe('0.2');
    expect(
      byName('vid-per-second').videoMatrix.cells['720p||__per_second__'],
    ).toBe('0.50');
  });
});
