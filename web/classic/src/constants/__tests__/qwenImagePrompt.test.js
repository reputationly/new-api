import { describe, it, expect } from 'vitest';
import {
  IMAGE_ENGINE_OPTIONS_INLINE,
  IMAGE_ENGINE_QWEN_IMAGE_21,
} from '../playgroundAdmin.constants';
import {
  appendOptimizeContext,
  buildImageOptimizeContext,
  defaultOptimizeSystemPrompt,
  optimizeOutputKind,
} from '../promptOptimize.constants';
import {
  extractQwenRewrittenPrompt,
  qwenImageOptimizeSystemPrompt,
} from '../qwenImagePrompt.constants';

// Qwen-Image-2.1 的「AI 优化提示词」层。守三件事：
//   1. 系统提示词是**官方原文**（service/qwenpe/*.txt 的逐字节副本）+ 后端那两条补丁，
//      拼装顺序也与后端一致 —— 被"顺手精简/改写"了这里就会红；
//   2. 产物是 JSON，但只有 rewritten_prompt 会被取出来（另外两个字段是画幅，体验区不用）；
//   3. 取值有兜底：裸引号让整段 JSON.parse 失败时，仍能把字段抠出来。

const t2i = defaultOptimizeSystemPrompt(
  'text2image',
  IMAGE_ENGINE_QWEN_IMAGE_21,
);
const i2i = defaultOptimizeSystemPrompt(
  'image2image',
  IMAGE_ENGINE_QWEN_IMAGE_21,
);

const EDIT_CLOSING = "The user's edit instruction to rewrite is:";

describe('官方原文模板', () => {
  it('文生图用官方 Image PE 原文，不是通用散文版', () => {
    expect(t2i).toContain('# Image Prompt Rewriting Expert');
    // 官方给的最后一行就是输出契约（JSON 形状），它必须还在
    expect(t2i).toContain(
      '{"rewritten_prompt": "<the description>", "wh_ratio": "<e.g. 3:2>"}',
    );
    // 通用契约（只回正文、不要字段名）与官方 JSON 契约相反，不能叠上去
    expect(t2i).not.toContain('Output ONLY the rewritten prompt itself');
  });

  it('图生图用官方编辑提示词原文', () => {
    expect(i2i).toContain('# Edit Prompt Enhancer');
    expect(i2i).toContain('Output a valid JSON object with exactly three fields');
    expect(i2i).not.toContain('Output ONLY the rewritten prompt itself');
  });

  it('编辑模板以官方收尾句结束（它的下一行就是用户指令）', () => {
    expect(i2i.endsWith(`${EDIT_CLOSING}\n`)).toBe(true);
  });
});

describe('我们自己补的两条规则', () => {
  it('转义说明：两份都有，且编辑那份插在收尾句之前', () => {
    for (const p of [t2i, i2i]) {
      expect(p).toContain('JSON escaping:');
      // 正文里的裸引号会弄坏 JSON，所以必须明说要写成 \" —— 这条实实在在是
      // 反斜杠 + 引号两个字符，被哪层转义吃掉一个这里就会红
      expect(p).toContain('escaped as \\"');
    }
    expect(i2i.indexOf('JSON escaping:')).toBeLessThan(
      i2i.indexOf(EDIT_CLOSING),
    );
  });

  it('「先分析后改写」只给文生图（编辑侧加了更差，见后端注释）', () => {
    expect(t2i).toContain('Make "analysis" the FIRST key');
    expect(i2i).not.toContain('Make "analysis" the FIRST key');
  });
});

describe('回落', () => {
  it('图像两个 tab 之外的玩法拿不到模板，回落通用版', () => {
    expect(qwenImageOptimizeSystemPrompt('text2video')).toBe('');
    expect(
      defaultOptimizeSystemPrompt('text2video', IMAGE_ENGINE_QWEN_IMAGE_21),
    ).toContain('Output ONLY the rewritten prompt itself');
  });

  it('引擎族选项里能选到 Qwen-Image-2.1', () => {
    expect(
      IMAGE_ENGINE_OPTIONS_INLINE.some(
        (o) => o.value === IMAGE_ENGINE_QWEN_IMAGE_21,
      ),
    ).toBe(true);
  });
});

describe('产物怎么取', () => {
  it('只有 Qwen 的两个 tab 取 rewritten_prompt，其余不受影响', () => {
    expect(optimizeOutputKind('text2image', IMAGE_ENGINE_QWEN_IMAGE_21)).toBe(
      'qwen-json',
    );
    expect(optimizeOutputKind('image2image', IMAGE_ENGINE_QWEN_IMAGE_21)).toBe(
      'qwen-json',
    );
    expect(optimizeOutputKind('text2video', IMAGE_ENGINE_QWEN_IMAGE_21)).toBe(
      'text',
    );
    expect(optimizeOutputKind('text2image', '')).toBe('text');
  });

  it('只取字段，不把整段 JSON 塞回输入框', () => {
    const reply = JSON.stringify({
      analysis: { orientation: 'horizontal', ratio: '16:9' },
      rewritten_prompt: 'A weathered brass telescope on an oak desk, backlit.',
      wh_ratio: '16:9',
      ratio_follow: '',
    });
    expect(extractQwenRewrittenPrompt(reply)).toBe(
      'A weathered brass telescope on an oak desk, backlit.',
    );
  });

  it('容忍围栏、前后废话与只有两个字段的编辑产物', () => {
    expect(
      extractQwenRewrittenPrompt(
        'Sure! Here it is:\n```json\n{"rewritten_prompt":"把外套换成亮黄色","wh_ratio":"","ratio_follow":"<image1>"}\n```\nHope that helps.',
      ),
    ).toBe('把外套换成亮黄色');
    expect(
      extractQwenRewrittenPrompt(
        '{"rewritten_prompt":"make the sky warmer","wh_ratio":"16:9"}',
      ),
    ).toBe('make the sky warmer');
  });

  // 正文里只要有一个没转义的引号，整段 JSON.parse 就失败，但字段本身往往完好。
  it('裸引号兜底：JSON 坏了也能把字段抠出来并还原转义', () => {
    const broken =
      '{"rewritten_prompt": "a sign that reads "OPEN" above the door\\nwide shot", "wh_ratio": "16:9"}';
    expect(extractQwenRewrittenPrompt(broken)).toBe(
      'a sign that reads "OPEN" above the door\nwide shot',
    );
  });

  it('取不到就返回空串（调用方据此报错，绝不回填原文）', () => {
    expect(extractQwenRewrittenPrompt('')).toBe('');
    expect(extractQwenRewrittenPrompt('一段自然语言，不是 JSON')).toBe('');
    expect(extractQwenRewrittenPrompt('{"wh_ratio":"16:9"}')).toBe('');
  });
});

// 与 U1.5 同一个坑：编辑模板的收尾句语义是"下面紧跟原文"，请求事实拼在它后面会被
// 模型当成要改写的指令。判据是字面匹配，不看引擎族。
describe('本次请求事实的插入位置', () => {
  it('插在 Qwen 编辑模板的收尾句之前', () => {
    const withContext = appendOptimizeContext(
      i2i,
      buildImageOptimizeContext({ size: '16:9', imageCount: 2 }),
    );
    expect(withContext.indexOf('- Target canvas: aspect ratio 16:9.')).toBeLessThan(
      withContext.indexOf(EDIT_CLOSING),
    );
    expect(withContext).toContain('- Input images: 2');
  });

  it('文生图模板没有收尾句，照旧追加在末尾', () => {
    const withContext = appendOptimizeContext(
      t2i,
      buildImageOptimizeContext({ size: '2048x1152' }),
    );
    expect(withContext.endsWith('- Target canvas: 2048x1152 pixels.')).toBe(true);
  });
});
