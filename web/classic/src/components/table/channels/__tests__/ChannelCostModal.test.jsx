import { describe, it, expect } from 'vitest';

import { mergeRows, toPayload } from '../modals/ChannelCostModal';

/**
 * 渠道成本录入的两个纯函数。
 *
 * 这里的取舍直接决定后端怎么选路和怎么对账：把「未配」当成 0 提交，该渠道会因为
 * 成本最低而静默抢走全部流量；把孤儿配置丢掉，对账会少掉几行而没人察觉。
 */

describe('mergeRows', () => {
  it('挂载模型与已配成本合成一张表', () => {
    const rows = mergeRows(
      ['GLM-5', 'Kimi-K3'],
      [{ model_name: 'GLM-5', cost_ratio: 0.62, remark: '并行 8 月报价' }],
    );

    expect(rows).toEqual([
      { model: 'GLM-5', ratio: 0.62, remark: '并行 8 月报价', mounted: true },
      { model: 'Kimi-K3', ratio: null, remark: '', mounted: true },
    ]);
  });

  it('未配成本的 ratio 是 null 而不是 0', () => {
    const [row] = mergeRows(['Kimi-K3'], []);
    expect(row.ratio).toBeNull();
    // 0 是「明确的零成本」，null 是「未配置」。混同会让该渠道在选路时被当成
    // 最便宜的，静默抢走全部流量
    expect(row.ratio).not.toBe(0);
  });

  it('配了成本但已不挂载的行保留并标记', () => {
    const rows = mergeRows(
      ['GLM-5'],
      [
        { model_name: 'GLM-5', cost_ratio: 0.62 },
        { model_name: 'retired-model', cost_ratio: 0.8 },
      ],
    );

    const orphan = rows.find((r) => r.model === 'retired-model');
    expect(orphan).toBeTruthy();
    expect(orphan.mounted).toBe(false);
  });

  it('成本比为 0 的行不会被当成未配置', () => {
    const [row] = mergeRows(
      ['self-hosted'],
      [{ model_name: 'self-hosted', cost_ratio: 0 }],
    );
    expect(row.ratio).toBe(0);
  });

  it('空输入不炸', () => {
    expect(mergeRows(null, null)).toEqual([]);
    expect(mergeRows([], [])).toEqual([]);
  });
});

describe('toPayload', () => {
  it('只提交填了值的行', () => {
    const payload = toPayload([
      { model: 'GLM-5', ratio: 0.62, remark: 'x' },
      { model: 'Kimi-K3', ratio: null, remark: '' },
    ]);

    expect(payload).toEqual([
      { model_name: 'GLM-5', cost_ratio: 0.62, remark: 'x' },
    ]);
  });

  it('成本比 0 要提交——它是明确的零成本，不是未配置', () => {
    const payload = toPayload([{ model: 'self-hosted', ratio: 0, remark: '' }]);
    expect(payload).toHaveLength(1);
    expect(payload[0].cost_ratio).toBe(0);
  });

  it('空串视为未配置', () => {
    expect(toPayload([{ model: 'a', ratio: '', remark: '' }])).toEqual([]);
  });

  it('数值化字符串输入', () => {
    const payload = toPayload([{ model: 'a', ratio: '0.5', remark: '' }]);
    expect(payload[0].cost_ratio).toBe(0.5);
  });
});
