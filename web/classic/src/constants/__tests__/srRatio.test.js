import { describe, it, expect } from 'vitest';
import {
  parseVideoModelConfig,
  getSrRatioForModel,
  VIDEO_SR_RATIO_UNCAPPED,
} from '../videoPlayground.constants';

// 超分倍率：配在**超分模型自己那一行**的模型级字段 srRatio 上。
//
// 守的是那次线上事故的形状：网关固定发 4.0，而 RelayVSR_X2 的 runner 里有一句
// `if sr_ratio != 2: raise`（LightX2V 的 relayvsr_runner.py）——整段超分失败，
// 然后**静默降级**成生成段成品：用户付了 2K 拿到 768P，接口不报错，只有一条 SysLog。
// 所以这里盯两件事：缺省必须是 4.0（SeedVR2 靠它"够不着"），配了就必须原样下发。

describe('getSrRatioForModel', () => {
  const cfg = parseVideoModelConfig(
    JSON.stringify({
      models: {
        relayvsr: { capabilities: ['视频超分'], sizes: ['2K'], srRatio: 2 },
        seedvr2: { capabilities: ['视频超分'], sizes: ['1080P'] },
        broken: { srRatio: 0 },
      },
    }),
  );

  it('按超分模型自己的声明取，不看生成模型', () => {
    expect(getSrRatioForModel(cfg, 'relayvsr')).toBe(2);
  });

  it('没配就回落到 4.0（SeedVR2 那套「倍率只是上限」的行为逐位不变）', () => {
    expect(getSrRatioForModel(cfg, 'seedvr2')).toBe(VIDEO_SR_RATIO_UNCAPPED);
    expect(VIDEO_SR_RATIO_UNCAPPED).toBe(4.0);
  });

  it('配成 0 / 非法值时当没配，不能把 0 发下去', () => {
    // 0 会被引擎当成"不放大"，比回落到 4.0 更糟
    expect(getSrRatioForModel(cfg, 'broken')).toBe(VIDEO_SR_RATIO_UNCAPPED);
    expect(getSrRatioForModel(cfg, undefined)).toBe(VIDEO_SR_RATIO_UNCAPPED);
    expect(getSrRatioForModel(null, 'relayvsr')).toBe(VIDEO_SR_RATIO_UNCAPPED);
  });

  // parseVideoConfig 是白名单式重建：漏一个字段 = 每次在管理页保存都会把它抹掉，
  // 而症状是"配好的 2 有一天又变回 4.0、超分又开始静默失败"。
  it('管理页保存往返后仍然保留', () => {
    expect(cfg.models.relayvsr.srRatio).toBe(2);
    const roundTripped = parseVideoModelConfig(JSON.stringify(cfg));
    expect(getSrRatioForModel(roundTripped, 'relayvsr')).toBe(2);
  });
});
