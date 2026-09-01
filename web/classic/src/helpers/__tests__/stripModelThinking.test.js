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
