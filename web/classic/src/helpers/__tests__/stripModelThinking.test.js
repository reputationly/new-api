import { describe, it, expect } from 'vitest';
import { stripModelThinking } from '../playground';

// 体验区那三处辅助调用（AI 优化提示词 / 中译英 / ACE-Step 生成方案）都直接用
// choices[0].message.content：回填输入框、发给生成引擎、JSON.parse。
// 辅助模型配成推理模型时，思考段若混在 content 里，三处的后果都不报错或报错误的因。

describe('stripModelThinking', () => {
  it('剥掉成对的思考段，只留正文', () => {
    expect(
      stripModelThinking(
        '<think>先想想主体和镜头</think>\n\n一只橘猫在窗台打盹',
      ),
    ).toBe('\n\n一只橘猫在窗台打盹');
  });

  // 最常见的形态：chat template 已经替模型写好了开标签，completion 里只有闭标签。
  // 成对匹配的正则（constants 里的 THINK_TAG_REGEX）对这种一个字都剥不掉。
  it('只有闭标签也要剥——这才是多数模板的实际形态', () => {
    expect(
      stripModelThinking('用户想要一只猫…</think>一只橘猫在窗台打盹'),
    ).toBe('一只橘猫在窗台打盹');
  });

  it('思考段里再出现闭标签时取最后一个，不会把正文切碎', () => {
    expect(stripModelThinking('思考A</think>思考B</think>正文')).toBe('正文');
  });

  it('没有思考段时原样返回', () => {
    expect(stripModelThinking('一只橘猫在窗台打盹')).toBe('一只橘猫在窗台打盹');
  });

  // 被截断、只有开标签:此时正文本就不存在,原样返回让调用方按「模型未返回内容」
  // 报错,比猜着截一段假正文出来诚实。
  it('只有开标签（被截断）时不猜，原样返回', () => {
    expect(stripModelThinking('<think>想到一半就断了')).toBe(
      '<think>想到一半就断了',
    );
  });

  it('空值不炸', () => {
    expect(stripModelThinking(null)).toBe('');
    expect(stripModelThinking(undefined)).toBe('');
  });
});

// ---------------------------------------------------------------------------
// extractRenderJson：SenseNova-U1.5 Image PE 的产物提取，照搬官方 image_pe.py 的
// extract_json（剥围栏 + 取首个 { 到末个 }）。
// ---------------------------------------------------------------------------
import { extractRenderJson } from '../playground';

describe('extractRenderJson', () => {
  const brief =
    '{"subjects":[{"description":"cathedral"}],"canvas":{"aspect_ratio":"2:3"}}';

  it('干净的 JSON 原样返回', () => {
    expect(extractRenderJson(brief)).toBe(brief);
  });

  it('剥掉 ```json 围栏', () => {
    expect(extractRenderJson('```json\n' + brief + '\n```')).toBe(brief);
  });

  // 模型在 JSON 前后多说一句是常态，官方脚本正是靠「取首个 { 到末个 }」兜的。
  it('前后多说了话也能取出来', () => {
    expect(
      extractRenderJson(
        'Here is the render brief:\n' + brief + '\nHope it helps!',
      ),
    ).toBe(brief);
  });

  it('保留模型自己的换行缩进，不重新序列化', () => {
    const pretty = '{\n  "subjects": []\n}';
    expect(extractRenderJson(pretty)).toBe(pretty);
  });

  // 与官方唯一的分歧：官方 parse 失败就抛错终止，我们降级返回原文——产物是直接回填
  // 用户输入框的，给他一段能改的文本，比把这次优化整个丢掉好。
  // ⚠️ 下面这两条**都没到 JSON.parse**：没有闭花括号，走的是 end < start 那道门。
  // 它们守的是"没有 JSON 就别乱切"，不是"parse 失败要降级"——两件事，别混为一谈。
  it('压根没有完整花括号时原样返回', () => {
    expect(extractRenderJson('{ 这不是 JSON')).toBe('{ 这不是 JSON');
    expect(extractRenderJson('一段普通的提示词')).toBe('一段普通的提示词');
  });

  // 这条才是真正压 parse 校验的：花括号齐全、切得出来，但内容不是合法 JSON。
  // 去掉 try/catch 后它会返回那段残缺切片而不是原文——最初写的降级用例没能发现这点。
  it('花括号齐全但不是合法 JSON 时降级返回原文', () => {
    const broken = '{"subjects": [ }';
    expect(extractRenderJson(broken)).toBe(broken);
    expect(extractRenderJson('前言 {"a": } 后语')).toBe('前言 {"a": } 后语');
  });

  // ⚠️ 这条早先叫「顶层是数组也算不合规」，名不副实：它走的是「压根没有 {」那条路，
  // 跟任何 JSON 类型判断无关。曾经为此写过一个判数组的分支，删掉后测试照样全绿——
  // 死代码 + 假测试。改成陈述真实行为。
  it('没有花括号时不做提取，原样返回', () => {
    expect(extractRenderJson('[1,2,3]')).toBe('[1,2,3]');
  });

  // 数组里裹着对象时取出那个对象——这是官方 find('{') / rfind('}') 的既定行为，
  // 不是我们的发挥。锁住它，免得哪天有人"顺手"改成整段 parse 而悄悄改掉语义。
  it('数组里裹着对象时取出对象，与官方切法一致', () => {
    expect(extractRenderJson('[{"subjects":[]}]')).toBe('{"subjects":[]}');
  });

  it('空值不炸', () => {
    expect(extractRenderJson(null)).toBe('');
  });
});
