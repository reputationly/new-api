import { describe, it, expect, beforeEach } from 'vitest';
import {
  previewComputePoints,
  formatPreviewAmount,
} from '../computePointPreview';

// 换算率与 quota_per_unit 都从 localStorage 读，测试里固定成好算的数：
// 1 点 = 100 quota，1 美元 = 500000 quota → 50000 点 = 5000000 quota = 10 美元
beforeEach(() => {
  localStorage.setItem('quota_per_compute_point', '100');
  localStorage.setItem('quota_per_unit', '500000');
});

describe('previewComputePoints', () => {
  it('按次计费模型换成调用次数', () => {
    const r = previewComputePoints(50000, { quota_type: 1, model_price: 0.04 });
    expect(r.kind).toBe('calls');
    expect(r.amount).toBe(250); // 10 美元 / 0.04
  });

  it('按量计费模型换成 tokens，口径与定价页一致（USD/1M = ratio × 2）', () => {
    const r = previewComputePoints(50000, { quota_type: 0, model_ratio: 2.5 });
    expect(r.kind).toBe('tokens');
    // 10 美元 / (2.5 × 2 美元每 1M) = 2M tokens
    expect(r.amount).toBe(2_000_000);
  });

  // 预览回答的是「最多能用多少」，所以取矩阵里最便宜的一格。
  // 与最坏成本估算刚好相反，那边取最贵——两者方向必须各自正确。
  it('视频按秒计费取最便宜档位换成秒数', () => {
    const r = previewComputePoints(50000, {
      video_pricing: {
        mode: 'per_second',
        per_second: { '720p': 0.05, '1080p': 0.2 },
      },
    });
    expect(r.kind).toBe('seconds');
    expect(r.amount).toBe(200); // 10 / 0.05
  });

  it('视频按次计费取最便宜格换成次数', () => {
    const r = previewComputePoints(50000, {
      video_pricing: {
        mode: 'per_call',
        per_call: { '720p': { '5s': 0.2, '10s': 0.4 }, '1080p': { '5s': 0.5 } },
      },
    });
    expect(r.kind).toBe('calls');
    expect(r.amount).toBe(50); // 10 / 0.2
  });

  it('视频 token 模式无法直接换算', () => {
    const r = previewComputePoints(50000, {
      video_pricing: { mode: 'token', token: { a: { b: 1 } } },
    });
    expect(r.kind).toBe('unknown');
  });

  it('矩阵优先于 model_price——后者是预扣锚点不是真实单价', () => {
    const r = previewComputePoints(50000, {
      quota_type: 1,
      model_price: 0.01,
      video_pricing: { mode: 'per_second', per_second: { '720p': 0.05 } },
    });
    expect(r.kind).toBe('seconds');
  });

  // model_ratio 对表达式计费模型只是预扣锚点，拿它换算出的数字是假的
  it('表达式动态计费模型返回 unknown，不拿锚点价硬算', () => {
    const r = previewComputePoints(50000, {
      quota_type: 0,
      model_ratio: 2.5,
      billing_mode: 'tiered_expr',
      billing_expr: 'p * 2',
    });
    expect(r.kind).toBe('unknown');
  });

  it('缺价格或缺模型时如实返回 unknown', () => {
    expect(previewComputePoints(50000, null).kind).toBe('unknown');
    expect(
      previewComputePoints(0, { quota_type: 1, model_price: 1 }).kind,
    ).toBe('unknown');
    expect(
      previewComputePoints(50000, { quota_type: 1, model_price: 0 }).kind,
    ).toBe('unknown');
    expect(
      previewComputePoints(50000, { quota_type: 0, model_ratio: 0 }).kind,
    ).toBe('unknown');
  });
});

describe('formatPreviewAmount', () => {
  it('加千分位', () => {
    expect(formatPreviewAmount(1250000)).toBe('1,250,000');
    expect(formatPreviewAmount(0)).toBe('0');
    expect(formatPreviewAmount(undefined)).toBe('0');
  });
});
