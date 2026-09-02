import { describe, it, expect } from 'vitest';
import {
  IMAGE_ENGINE_SENSENOVA_U15,
  PLAYGROUND_MODEL_LEVEL_FIELDS,
} from '../playgroundAdmin.constants';
import {
  parseImageSizeConfig,
  getEngineForImageModel,
} from '../imagePlayground.constants';
import {
  defaultOptimizeSystemPrompt,
  optimizeOutputsJson,
  optimizeUserSuffix,
  GENERIC_OPTIMIZE_SYSTEM_PROMPT,
} from '../promptOptimize.constants';

// SenseNova-U1.5 的两份模板**逐字取自官方源码**（image_pe.py 的 IMAGE_PE_SYSTEM_PROMPT、
// edit_pe.py 的 REWRITE_SYSTEM_PROMPT_4_EDIT）。这里锁的是「别被顺手改写回散文」——
// 上一版这里放的就是按文档转述的散文版，与官方产出的形状完全不同（官方要 Render JSON），
// 出图质量差一大截，而且不报错。

const t2i = defaultOptimizeSystemPrompt(
  'text2image',
  IMAGE_ENGINE_SENSENOVA_U15,
);
const i2i = defaultOptimizeSystemPrompt(
  'image2image',
  IMAGE_ENGINE_SENSENOVA_U15,
);

describe('图像引擎族声明能往返（白名单式 parse 不能漏）', () => {
  it('保住 engine，并按统一口径 lower+trim', () => {
    const parsed = parseImageSizeConfig(
      JSON.stringify({
        models: {
          'u1.5': { engine: '  SenseNova-U1.5 ', sizes: ['2048x2048'] },
        },
      }),
    );
    expect(parsed.models['u1.5'].engine).toBe(IMAGE_ENGINE_SENSENOVA_U15);
    expect(getEngineForImageModel(parsed, 'u1.5')).toBe(
      IMAGE_ENGINE_SENSENOVA_U15,
    );
  });

  it('未声明的模型落空串，取模板时回落通用版', () => {
    const parsed = parseImageSizeConfig({
      models: { 'flux-1': { sizes: [] } },
    });
    expect(parsed.models['flux-1'].engine).toBe('');
    expect(getEngineForImageModel(parsed, 'flux-1')).toBe('');
  });

  it('管理页有地方可选，且有空值项好让运营取消', () => {
    const engine = (
      PLAYGROUND_MODEL_LEVEL_FIELDS.ImageModelSizeConfig || []
    ).find((f) => f.key === 'engine');
    expect(engine).toBeTruthy();
    expect(engine.type).toBe('select');
    expect(engine.options.some((o) => o.value === '')).toBe(true);
  });
});

describe('文生图用的是官方 Image PE 原文', () => {
  it('是 Render JSON 契约，不是散文改写', () => {
    expect(t2i).toMatch(/Compile the user request into one dense/);
    expect(t2i).toMatch(/Return raw JSON only/);
    expect(t2i).not.toBe(GENERIC_OPTIMIZE_SYSTEM_PROMPT);
  });

  it('JSON 骨架的字段一个都不能少', () => {
    for (const key of [
      'subjects',
      'scene',
      'lighting',
      'composition',
      'style',
      'camera',
      'visible_copy',
      'structure',
      'image_description',
      'canvas',
      'negative',
    ]) {
      expect(t2i, `缺字段 ${key}`).toContain(`"${key}"`);
    }
  });

  // 画布那五行是官方写死的 "immutable 2K row"，也是我们要让运营配进尺寸选择器的那五档。
  // 少一行就会让 PE 把比例映射到别处，出图分辨率跟着走偏。
  it('画布档位是官方那五行 2K', () => {
    for (const row of [
      '2048 x 2048',
      '2496 x 1664',
      '1664 x 2496',
      '2720 x 1536',
      '1536 x 2720',
    ]) {
      expect(t2i, `缺档位 ${row}`).toContain(row);
    }
  });

  it('不叠加我们自己的输出契约（会与 raw JSON 打架）', () => {
    expect(t2i).not.toMatch(/Output ONLY the rewritten prompt/);
    expect(t2i).not.toMatch(/no markdown fence/);
  });
});

describe('图生图用的是官方 Edit Instruction Rewriter 原文', () => {
  it('是编辑指令改写器，且自带「只回改写结果」的契约', () => {
    expect(i2i).toMatch(/# Edit Instruction Rewriter/);
    expect(i2i).toMatch(/Please provide only the rewritten instruction/);
  });

  it('保留官方那几节硬规则', () => {
    expect(i2i).toMatch(/Add, Delete, Replace Tasks/);
    expect(i2i).toMatch(/Text Editing Tasks/);
    expect(i2i).toMatch(/Reference-Image or Multi-Reference Tasks/);
    expect(i2i).toMatch(/Infographic and Related Graphic-Design/);
  });

  it('产物是自然语言，不是 JSON——两个玩法不能共用一套后处理', () => {
    expect(i2i).not.toMatch(/Return raw JSON only/);
    expect(optimizeOutputsJson('text2image', IMAGE_ENGINE_SENSENOVA_U15)).toBe(
      true,
    );
    expect(optimizeOutputsJson('image2image', IMAGE_ENGINE_SENSENOVA_U15)).toBe(
      false,
    );
  });
});

describe('官方的用户消息后缀', () => {
  it('只有 U1.5 图生图拼 Rewritten Prompt:', () => {
    expect(optimizeUserSuffix('image2image', IMAGE_ENGINE_SENSENOVA_U15)).toBe(
      '\n\nRewritten Prompt:',
    );
  });

  it('其余组合一律空串（拼上去等于没拼）', () => {
    expect(optimizeUserSuffix('text2image', IMAGE_ENGINE_SENSENOVA_U15)).toBe(
      '',
    );
    expect(optimizeUserSuffix('image2image', '')).toBe('');
    expect(optimizeUserSuffix('text2video', 'ltx-2.5')).toBe('');
  });
});

describe('回落', () => {
  it('U1.5 没覆盖的玩法回落通用版，不会把图像模板串到别处', () => {
    expect(
      defaultOptimizeSystemPrompt('text2video', IMAGE_ENGINE_SENSENOVA_U15),
    ).not.toBe(t2i);
    expect(optimizeOutputsJson('text2video', IMAGE_ENGINE_SENSENOVA_U15)).toBe(
      false,
    );
  });
});
