import { describe, it, expect } from 'vitest';
import {
  appendOptimizeContext,
  buildImageOptimizeContext,
  defaultOptimizeSystemPrompt,
  U15_EDIT_CLOSING_MARKER,
} from '../promptOptimize.constants';
import { IMAGE_ENGINE_SENSENOVA_U15 } from '../playgroundAdmin.constants';
import { IMAGE_SIZE_AUTO } from '../imagePlayground.constants';

// 优化模型看不到左侧面板。这段「本次请求事实」补的就是它猜不到、且猜错不报错的两件事：
// 目标画幅（决定要不要压缩文案，U1.5 编辑模板 §6 的硬要求）与底图张数编号（决定
// 「第 N 张」这个说法成不成立）。与视频区的 buildH3OptimizeContext 同一机制。

describe('buildImageOptimizeContext', () => {
  it('没有任何事实可说时返回空串（拼上去等于没拼）', () => {
    expect(buildImageOptimizeContext()).toBe('');
    expect(buildImageOptimizeContext({ size: '', imageCount: 0 })).toBe('');
  });

  it('文生图只说画幅，不提图片', () => {
    const out = buildImageOptimizeContext({ size: '1536x2720' });
    expect(out).toContain('1536x2720');
    expect(out).not.toContain('Input images');
  });

  it('auto 档不编造具体画幅', () => {
    // 编一个具体值比不说更糟：模型会照着它做排版可行性判断，而真实画幅由引擎定。
    expect(buildImageOptimizeContext({ size: IMAGE_SIZE_AUTO })).toBe('');
  });

  // sizes 的语义是「发什么」不是「什么比例」，运营两种写法混着用（文生图历来填比例
  // 词）。不分开写就会发出 "Target canvas: 16:9 pixels" —— 一句自相矛盾的假事实。
  it('比例词不写成 pixels，而是 aspect ratio', () => {
    const out = buildImageOptimizeContext({ size: '16:9' });
    expect(out).toContain('aspect ratio 16:9');
    expect(out).not.toContain('pixels');
  });

  it('像素档仍写 pixels', () => {
    const out = buildImageOptimizeContext({ size: '1664x928' });
    expect(out).toContain('1664x928 pixels');
    expect(out).not.toContain('aspect ratio');
  });

  it('单张底图不写编号区间', () => {
    const out = buildImageOptimizeContext({ size: '', imageCount: 1 });
    expect(out).toContain('<Image 1>');
    expect(out).not.toContain('..');
  });

  it('多张底图写出编号区间，且把「第 N 张 = 这个顺序」说明白', () => {
    const out = buildImageOptimizeContext({ size: '', imageCount: 3 });
    expect(out).toContain('<Image 1>..<Image 3>');
    expect(out).toContain('the Nth image');
  });

  // 这段 context 是**无条件**拼在任何模板末尾的（usePromptOptimize:
  // template + context），所以它多说一句指令，就是对每一份模板都多说一句。
  //
  // 之前这里跟过一句「Every required text element must fit legibly at this canvas」：
  // 对 U1.5 是重复 §6（两处打架），对通用模板则是凭空新增，且与 IMAGE_PROMPT 的
  // 「未经用户要求不得在画面里编造文字」直接冲突——那句话预设了文字元素存在，会推着
  // 模型给「一只猫在窗台上打盹」也去规划画面文案。约定没有用例守着，就是这么破的。
  it('只陈述事实，不含祈使句', () => {
    const out = buildImageOptimizeContext({ size: '1664x928', imageCount: 2 });
    expect(out).toBeTruthy();
    expect(out).not.toMatch(/\b(must|should|ensure|make sure)\b/i);
  });

  it('画幅与底图同时存在时两条都在', () => {
    const out = buildImageOptimizeContext({
      size: '1664x2496',
      imageCount: 2,
    });
    expect(out).toContain('1664x2496');
    expect(out).toContain('<Image 1>..<Image 2>');
  });
});

// 官方 U1.5 编辑模板的收尾句「Below is the Prompt to be rewritten」本意是紧接原文。
// 请求事实若拼在它后面,模型会把那段英文当成「原文」,「用原文语言改写」就变成了英文
// 改写(线上一次中文海报请求就是这样出了整段英文)。所以事实必须插在收尾句之前。
describe('appendOptimizeContext', () => {
  const ctx = buildImageOptimizeContext({ size: '1536x2720', imageCount: 1 });

  it('U1.5 编辑模板:事实插在收尾句之前,收尾句仍是模板最后一段', () => {
    const base = defaultOptimizeSystemPrompt(
      'image2image',
      IMAGE_ENGINE_SENSENOVA_U15,
    );
    const out = appendOptimizeContext(base, ctx);
    const at = out.indexOf(ctx);
    expect(at).toBeGreaterThan(0);
    expect(at).toBeLessThan(out.indexOf(U15_EDIT_CLOSING_MARKER));
    expect(
      out.endsWith(base.slice(base.lastIndexOf(U15_EDIT_CLOSING_MARKER))),
    ).toBe(true);
    // 模板正文一个字不少:去掉插入段后与原模板相同。
    expect(out.replace(ctx + '\n', '')).toBe(base);
  });

  it('没有收尾句的模板仍追加在末尾', () => {
    expect(appendOptimizeContext('通用模板', ctx)).toBe('通用模板' + ctx);
    expect(
      appendOptimizeContext(
        defaultOptimizeSystemPrompt('image2image', ''),
        ctx,
      ).endsWith(ctx),
    ).toBe(true);
  });

  it('运营把官方模板贴进改写里也命中(判据是字面,不是引擎族)', () => {
    const pasted = '我的前言\n' + U15_EDIT_CLOSING_MARKER + ' 请只回改写。';
    const out = appendOptimizeContext(pasted, ctx);
    expect(out.indexOf(ctx)).toBeLessThan(out.indexOf(U15_EDIT_CLOSING_MARKER));
  });

  it('没有事实时原样返回', () => {
    expect(appendOptimizeContext('X', '')).toBe('X');
    expect(appendOptimizeContext('X', undefined)).toBe('X');
  });
});
