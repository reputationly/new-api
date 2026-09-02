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
  GENERIC_OPTIMIZE_SYSTEM_PROMPT,
} from '../promptOptimize.constants';

// SenseNova-U1.5 的内置优化模板。整条链任何一环断了都**不报错**，只是优化退回通用
// 模板：可见文案被改写（这个模型真的会把字渲染进画面，改一个字就是错一个字）、
// 多图融合时不说明每张图的角色、编辑时漏掉「哪些必须不变」而整张被重画。

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

  it('管理页有地方可选——没有这一项，运营根本声明不了引擎族', () => {
    const keys = (PLAYGROUND_MODEL_LEVEL_FIELDS.ImageModelSizeConfig || []).map(
      (f) => f.key,
    );
    expect(keys).toContain('engine');
  });
});

describe('U1.5 的两个玩法各有专用模板', () => {
  const t2i = defaultOptimizeSystemPrompt(
    'text2image',
    IMAGE_ENGINE_SENSENOVA_U15,
  );
  const i2i = defaultOptimizeSystemPrompt(
    'image2image',
    IMAGE_ENGINE_SENSENOVA_U15,
  );

  it('文生图与图生图拿到的不是同一份，也都不是通用版', () => {
    expect(t2i).not.toBe(i2i);
    expect(t2i).not.toBe(GENERIC_OPTIMIZE_SYSTEM_PROMPT);
    expect(i2i).not.toBe(GENERIC_OPTIMIZE_SYSTEM_PROMPT);
  });

  it('文生图模板守住官方 PE 要求保留的东西：逐字文案、数量、版面、排除项', () => {
    expect(t2i).toMatch(/verbatim/i);
    expect(t2i).toMatch(/quantities/i);
    expect(t2i).toMatch(/layout/i);
    expect(t2i).toMatch(/exclusions/i);
  });

  it('图生图模板同时要求「改什么」与「哪些不变」，并交代多图顺序与角色', () => {
    expect(i2i).toMatch(/preservation/i);
    expect(i2i).toMatch(/the order the user uploaded them/i);
    expect(i2i).toMatch(/role/i);
  });

  // 体验区底图上限是 3（IMAGE_MAX_EDIT_IMAGES）。官方示例里出现过 5 张，照抄会让优化
  // 模型编出用户根本传不上去的第 4、5 张的角色说明。
  it('多图数量与体验区实际上限一致，不照抄官方示例的 5 张', () => {
    expect(i2i).toMatch(/one to three base images/i);
    expect(i2i).not.toMatch(/five (reference )?images/i);
  });

  it('输出契约仍是通用那条：只回正文，不要围栏', () => {
    expect(t2i).toMatch(/Output ONLY the rewritten prompt/);
    expect(i2i).toMatch(/no markdown fence/);
  });

  it('U1.5 没覆盖的玩法回落通用版，不会把图像模板串到别处', () => {
    expect(
      defaultOptimizeSystemPrompt('text2video', IMAGE_ENGINE_SENSENOVA_U15),
    ).not.toBe(t2i);
  });
});
