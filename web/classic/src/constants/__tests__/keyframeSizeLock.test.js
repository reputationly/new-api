import { describe, it, expect } from 'vitest';
import {
  VIDEO_ENGINE_LTX25,
  VIDEO_ENGINE_MINIMAX_H3,
  getTabFieldLock,
  tabHasField,
} from '../playgroundAdmin.constants';
import {
  buildVideoSizeChoices,
  getSizesForVideoModel,
} from '../videoPlayground.constants';

// 关键帧的分辨率档由引擎硬约束锁死（H3 按首图推画布、short_edge 硬校验 768）。
// 这一组测试守的是「配了也不作数」这件事——它一旦破防是**静默**的：用户选了 480P，
// 提交不报错、也不生效，仍出 768P。
describe('关键帧 sizes 锁定', () => {
  it('关键帧被锁定成 768P，且带上锁定原因', () => {
    const lock = getTabFieldLock('video', 'flf2v', 'sizes');
    expect(lock).toBeTruthy();
    expect(lock.value).toEqual(['768P']);
    expect(lock.reason).toBeTruthy();
  });

  it('H3 仍然锁死', () => {
    const lock = getTabFieldLock(
      'video',
      'flf2v',
      'sizes',
      VIDEO_ENGINE_MINIMAX_H3,
    );
    expect(lock?.value).toEqual(['768P']);
  });

  // wan 的两类关键帧实例 engine 留空，自这把锁上线起就一直吃着它。改成「只对 H3 生效」
  // 会顺手把它们解锁，让运营为文生视频配的档位顺着三级回落漏进关键帧——正是加锁要挡
  // 的事，且症状静默。所以默认（不传 / 空引擎）必须仍然锁死。
  it('引擎族留空（wan）仍然锁死', () => {
    expect(getTabFieldLock('video', 'flf2v', 'sizes', '')?.value).toEqual([
      '768P',
    ]);
  });

  // LTX-2.5 认请求里的 width/height（首帧图由引擎等比放大后居中裁剪去适配画布），
  // 不是按图推画布。锁不解，它会拿到 '768P' 这个档位词发给引擎——而清档位词的
  // h3DropResolutionToken 是 H3 专属的，LTX 这条路上没有，引擎直接报错。
  it('LTX-2.5 豁免这把锁，档位回到运营配置', () => {
    expect(
      getTabFieldLock('video', 'flf2v', 'sizes', VIDEO_ENGINE_LTX25),
    ).toBeNull();
  });

  // 运营输入的引擎族可能带空格/大小写（后端比较前也是 lower+trim），
  // 两边判据分叉的后果是静默的：管理端显示解锁、体验区仍按锁定值发。
  it('引擎族比较前 lower+trim，与后端同口径', () => {
    expect(getTabFieldLock('video', 'flf2v', 'sizes', '  LTX-2.5 ')).toBeNull();
  });

  // 锁定值只有在 sizes 仍是该 tab 的字段时才有用：字段被摘掉的话 sendsSize 变 false，
  // sizeChoices 的超分闸门整个关上，1080P 超分档再也出不来。
  it('sizes 仍在关键帧的 fields 里（超分档的闸门）', () => {
    expect(tabHasField('video', 'flf2v', 'sizes')).toBe(true);
  });

  // 别的玩法不该被这个机制波及：文生视频的档位是运营自己配的。
  it('文生视频不锁定，档位仍由运营配置决定', () => {
    expect(getTabFieldLock('video', 'text2video', 'sizes')).toBeNull();
  });
});

// 锁定的真正价值在于挡住「三级回落」：运营在关键帧 tab 上一个字都没填，
// 却因为模型级/分类默认值的配置而冒出失效档位。
describe('锁定值挡住 getSizesForVideoModel 的回落链', () => {
  // 关键帧 tab 未配 sizes，但模型级和分类默认值都配了——典型的「为文生视频配的档位」。
  const cfg = {
    default: { sizes: ['480P', '768P', '1080P'] },
    models: {
      h3: {
        pipeline: true,
        sizes: ['480P', '768P'],
        tabs: { flf2v: {} },
      },
    },
  };

  it('不加锁时，回落链确实会把 480P 漏给关键帧', () => {
    // 这一条不是在测我们的代码「应该」怎样，而是固定住回落行为本身：
    // 它正是锁定机制存在的理由，哪天回落改了这里会先红。
    expect(getSizesForVideoModel(cfg, 'h3', 'flf2v')).toEqual(['480P', '768P']);
  });

  it('锁定值优先于回落结果', () => {
    const lock = getTabFieldLock('video', 'flf2v', 'sizes');
    const native = lock?.value || getSizesForVideoModel(cfg, 'h3', 'flf2v');
    expect(native).toEqual(['768P']);
    expect(native).not.toContain('480P');
  });
});

// 锁成单档之后，1080P 只能来自模型级「超分档位」规则；规则的起步档必须是 768P，
// 否则 resolveUpscaleFrom 取不到、整档不渲染。
describe('1080P 超分档基于锁定的 768P 起步', () => {
  const cfgWithRule = {
    models: {
      h3: {
        pipeline: true,
        upscale: [{ from: '768P', to: '1080P', model: 'seedvr2' }],
      },
    },
  };

  it('起步档命中锁定值时产出 1080P 超分档', () => {
    const choices = buildVideoSizeChoices(
      cfgWithRule,
      'h3',
      ['768P'],
      ['seedvr2'],
    );
    expect(choices.map((c) => c.value)).toEqual(['768P', '1080P']);
    const up = choices.find((c) => c.value === '1080P');
    expect(up.isUpscale).toBe(true);
    expect(up.fromSize).toBe('768P');
    expect(up.srModel).toBe('seedvr2');
  });

  // 运营把起步档配成 480P（关键帧下不存在的档位）时整条规则不生效 —— 界面上不出
  // 1080P，而不是出一个点了会按 480P 起步的假档位。
  it('起步档不在锁定值里时整档不产出', () => {
    const badRule = {
      models: {
        h3: {
          pipeline: true,
          upscale: [{ from: '480P', to: '1080P', model: 'seedvr2' }],
        },
      },
    };
    const choices = buildVideoSizeChoices(badRule, 'h3', ['768P'], ['seedvr2']);
    expect(choices.map((c) => c.value)).toEqual(['768P']);
  });

  // 不配超分规则就只有 768P 一档（运营可以不加 1080P）。
  it('没有超分规则时只剩 768P', () => {
    const choices = buildVideoSizeChoices(
      { models: { h3: { pipeline: true } } },
      'h3',
      ['768P'],
      ['seedvr2'],
    );
    expect(choices.map((c) => c.value)).toEqual(['768P']);
  });
});
