import { describe, it, expect } from 'vitest';
import { calculateModelPrice } from '../utils';

// 「没配分时价」的模型必须与改造前**逐位一致**：
// 后端不下发 group_model_component_scale、前端拿不到任何分量系数，
// 价格/划线/角标全部走原路径。
describe('未配置分时价：行为与改造前一致', () => {
  const record = {
    model_name: 'm',
    quota_type: 0,
    model_ratio: 2,
    completion_ratio: 4,
    cache_ratio: 0.5,
    enable_groups: ['default'],
  };
  const base = {
    record,
    selectedGroup: 'default',
    groupRatio: { default: 1 },
    // 线路内模型折扣 5 折：终值 0.5
    groupModelRatio: { default: { m: 0.5 } },
    displayPrice: (v) => `¥${Number(v).toFixed(2)}`,
  };

  it('不传分量系数时，各项价格与分量系数全 1 时完全相同', () => {
    const plain = calculateModelPrice(base);
    const allOnes = calculateModelPrice({
      ...base,
      componentScaleMap: {
        default: {
          m: { output: 1, cache: 1, image: 1, audio_input: 1, audio_output: 1 },
        },
      },
    });
    ['inputPrice', 'completionPrice', 'cachePrice'].forEach((k) => {
      expect(allOnes[k]).toBe(plain[k]);
    });
  });

  it('划线价仍是原来的「折前价」（不是时段目录价）', () => {
    const p = calculateModelPrice(base);
    // 折前价 = 目录价（ratio 1）：输入 2.00、输出 8.00
    expect(p.originalInputPrice).toBe('¥2.00');
    expect(p.originalCompletionPrice).toBe('¥8.00');
  });
});
