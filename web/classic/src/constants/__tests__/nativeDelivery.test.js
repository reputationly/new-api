import { describe, it, expect } from 'vitest';
import {
  parseVideoModelConfig,
  isNativeDeliveryModel,
  isPipelineModel,
  buildVideoSizeChoices,
  upscaleTargetShortEdge,
  normalizeUpscaleList,
  getUpscaleRulesForModel,
  normalizeVideoSize,
  videoSizeShortEdge,
  VIDEO_DELIVERY_TIERS,
  VIDEO_DELIVERY_SHORT_EDGE_KEY,
} from '../videoPlayground.constants';
import { PLAYGROUND_MODEL_LEVEL_FIELDS } from '../playgroundAdmin.constants';

// 「高分辨率档用纯放大」这个模型级开关。
//
// 它决定选中 1080P 时走哪条路：勾了 = 原生档生成 + 引擎出片前缩放（一段式）；
// 没勾 = 原生档生成 + 接超分模型跑第二段（两段式，原行为）。
//
// 这一组守的全是**静默**失效：开关被保存抹掉、字段名写错、把交付短边当生成尺寸下发，
// 三种都不报错，只是产物悄悄变成另一个东西。

describe('nativeDelivery 开关的配置往返', () => {
  // parseVideoModelConfig 是白名单式重建，且管理页草稿正是用它水合。漏一个键
  // = 运营每次打开配置页保存就把它删一次，而症状是「某天起 1080P 又开始接超分了、
  // 画面又开始沸腾」——没人会把这个联想到一次无关的保存。
  it('parse 保住 nativeDelivery，不会被保存抹掉', () => {
    const parsed = parseVideoModelConfig(
      JSON.stringify({
        models: { h3: { nativeDelivery: true, pipeline: true } },
      }),
    );
    expect(parsed.models.h3.nativeDelivery).toBe(true);
    expect(isNativeDeliveryModel(parsed, 'h3')).toBe(true);
  });

  it('未配时为 false —— 纯 opt-in，新接入模型默认走原有两段式', () => {
    const parsed = parseVideoModelConfig({
      models: { wan: { pipeline: true } },
    });
    expect(parsed.models.wan.nativeDelivery).toBe(false);
    expect(isNativeDeliveryModel(parsed, 'wan')).toBe(false);
    // 没配进配置的模型同样为 false，不能因为读不到就当成开
    expect(isNativeDeliveryModel(parsed, '不存在的模型')).toBe(false);
    expect(isNativeDeliveryModel(null, 'wan')).toBe(false);
  });

  it('与 pipeline 正交：两个开关各管各的', () => {
    const parsed = parseVideoModelConfig({
      models: { x: { nativeDelivery: true } },
    });
    // 一段式仍然要求自建引擎（提交侧 maybeHighRes 的第一个条件就是 usePipeline），
    // 勾了纯放大不等于自动获得自建引擎能力
    expect(isNativeDeliveryModel(parsed, 'x')).toBe(true);
    expect(isPipelineModel(parsed, 'x')).toBe(false);
  });

  it('运营在管理页能勾到这个开关', () => {
    const f = PLAYGROUND_MODEL_LEVEL_FIELDS.VideoModelConfig.find(
      (x) => x.key === 'nativeDelivery',
    );
    expect(f, '模型级字段里没有 nativeDelivery，运营勾不到').toBeTruthy();
    expect(f.type).toBe('bool');
    // 帮助文案必须写明「引擎不支持时会静默失效」——这是勾错的唯一代价，且不报错
    expect(f.help).toContain('静默');
  });
});

describe('交付短边字段', () => {
  // 三个短边字段语义完全不同，混用不报错只会静默走错路径：
  //   short_edge（H3 自算画布）/ target_short_edge（SwiftVR 超分段）/ 本字段
  it('键名与另外两个短边字段都不重名', () => {
    expect(VIDEO_DELIVERY_SHORT_EDGE_KEY).toBe('delivery_short_edge');
    expect(VIDEO_DELIVERY_SHORT_EDGE_KEY).not.toBe('short_edge');
    expect(VIDEO_DELIVERY_SHORT_EDGE_KEY).not.toBe('target_short_edge');
  });

  // 一段式与两段式的档位语义必须完全一致：都是「短边档」，长边由画幅决定。
  // 两条路各推一份迟早推出不同答案（keyframeTaskType 当初就是这么栽的）。
  it('交付短边与两段式的目标短边取自同一个换算', () => {
    for (const [tier, want] of [
      ['1080P', 1080],
      ['2K', 1440],
      ['4K', 2160],
      ['1248x704', 704],
    ]) {
      expect(upscaleTargetShortEdge(tier), `${tier} 换算不对`).toBe(want);
    }
  });
});

// 一段式整条路上没有第二个模型，档位就不该再按超分模型的分组可用性去筛 ——
// 否则「运营把 SR 模型对某分组停用」会让一个压根不需要它的模型的 1080P 档静默消失。
describe('档位可用性过滤只对两段式成立', () => {
  const cfg = (nativeDelivery) => ({
    models: {
      m: {
        pipeline: true,
        nativeDelivery,
        upscale: [{ from: '1248x704', to: '1080P', model: 'seedvr2' }],
      },
    },
  });
  const native = ['1248x704'];

  it('两段式：超分模型对该分组不可用时整档不出', () => {
    const choices = buildVideoSizeChoices(cfg(false), 'm', native, [
      '其它模型',
    ]);
    expect(choices.map((c) => c.value)).toEqual(['1248x704']);
  });

  it('两段式：超分模型可用时正常出 1080P', () => {
    const choices = buildVideoSizeChoices(cfg(false), 'm', native, ['seedvr2']);
    expect(choices.map((c) => c.value)).toEqual(['1248x704', '1080P']);
  });

  // 一段式：调用方（sizeChoices）不再传可用列表，传 null 即不过滤
  it('一段式：不传可用列表时 1080P 照常出', () => {
    const choices = buildVideoSizeChoices(cfg(true), 'm', native, null);
    expect(choices.map((c) => c.value)).toEqual(['1248x704', '1080P']);
  });
});

// 一段式的超分规则行**不需要 model**。
//
// 这条是踩出来的:管理页曾把「超分模型」置灰(因为它确实不生效),结果新增行填不了
// model,保存后被 normalizeUpscaleList 整行丢弃 ——「添加超分档位」变成不可能完成的
// 操作,而且不报错,运营只会看到「新加的档位怎么没了」。
describe('一段式的规则行允许 model 为空', () => {
  const rules = [{ to: '4K', from: '768P' }]; // 没有 model('4K' 会被归一成 '4k')

  it('两段式:缺 model 的行被丢弃（没有超分模型就没有第二段可跑）', () => {
    expect(normalizeUpscaleList(rules)).toEqual([]);
  });

  it('一段式:缺 model 的行保留，档位照常成立', () => {
    const out = normalizeUpscaleList(rules, true);
    expect(out).toHaveLength(1);
    expect(out[0].to).toBe('4k'); // normalizeVideoSize 只对 \d+p 转大写
    expect(out[0].from).toBe('768P');
  });

  // 运行时取值必须跟着放行，否则运营配好了、体验区照样不出这一档
  it('getUpscaleRulesForModel 对一段式模型放行无 model 的规则', () => {
    const cfg = {
      models: {
        h3: { pipeline: true, nativeDelivery: true, upscale: rules },
        wan: { pipeline: true, upscale: rules },
      },
    };
    expect(getUpscaleRulesForModel(cfg, 'h3')).toHaveLength(1);
    expect(getUpscaleRulesForModel(cfg, 'wan')).toHaveLength(0);
  });

  // 配置往返也要保住:parse 是白名单式重建，一段式必须传同一个开关进去
  it('parseVideoModelConfig 对一段式模型保住无 model 的规则', () => {
    const parsed = parseVideoModelConfig({
      models: { h3: { nativeDelivery: true, upscale: rules } },
    });
    expect(parsed.models.h3.upscale).toHaveLength(1);
  });

  // to 仍然必填:没有目标档位这条规则什么都定义不了
  it('缺 to 的行两种模式都丢弃', () => {
    expect(normalizeUpscaleList([{ from: '768P' }], true)).toEqual([]);
  });
});

// 交付档位表必须是归一**之后**的形态，否则管理页那一格看起来是空的：
// 存下来的 to 是 normalizeVideoSize 的结果，而下拉选项若用未归一的写法就匹配不上。
describe('VIDEO_DELIVERY_TIERS 与归一口径一致', () => {
  it('每一项都等于自己归一后的值', () => {
    for (const tier of VIDEO_DELIVERY_TIERS) {
      expect(normalizeVideoSize(tier), `${tier} 未归一`).toBe(tier);
    }
  });

  it('每一项都能解析出短边（否则超分档排序与起步档筛选会失效）', () => {
    for (const tier of VIDEO_DELIVERY_TIERS) {
      expect(videoSizeShortEdge(tier), `${tier} 解析不出短边`).toBeGreaterThan(
        0,
      );
    }
  });
});
