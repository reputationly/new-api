import React from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

import TabPanel from '../TabPanel';
import {
  MUSIC_ENGINE_MINIMAX_MUSIC3,
  getPlaygroundTab,
} from '../../../constants/playgroundAdmin.constants';

// 体验区管理 → 文生音乐，模型卡片里的「AI 优化提示词（模型级）」两条行为：
//   1. ACE-Step 模型上要挂告警——它的按钮走 draftPlan，这里配的模板一个字都用不上；
//   2. 「已定制 / 跟随本 tab」的判据要与运行时同口径（trim 后判空）。
// 两条都是「不报错、只是把人引偏」的那类，所以由测试守。

const T2M = getPlaygroundTab('music', 't2m');

const makeDraft = (models) => ({
  stores: { MusicModelConfig: { models, defaults: {} } },
  tabConfig: {},
  allModels: [],
  options: {},
  patchTabConfig: vi.fn(),
  setTabField: vi.fn(),
  setModelField: vi.fn(),
  addModelToTab: vi.fn(),
  removeModelFromTab: vi.fn(),
});

const renderTab = (models) =>
  render(<TabPanel category='music' tab={T2M} draft={makeDraft(models)} />);

describe('文生音乐下 ACE-Step 的模型级模板要标明用不上', () => {
  it('ACE-Step（未声明引擎族）挂告警', () => {
    renderTab({ 'ace-step': { tabs: { t2m: {} } } });
    expect(screen.queryByText(/一键生成方案/)).not.toBeNull();
  });

  it('MiniMax-Music3 不挂告警——它走的正是这条优化链路', () => {
    renderTab({
      'minimax-music3': {
        engine: MUSIC_ENGINE_MINIMAX_MUSIC3,
        tabs: { t2m: {} },
      },
    });
    expect(screen.queryByText(/一键生成方案/)).toBeNull();
  });
});

describe('「已定制」的判据与运行时同口径', () => {
  it('写了内容 = 已定制', () => {
    renderTab({
      'minimax-music3': {
        engine: MUSIC_ENGINE_MINIMAX_MUSIC3,
        tabs: { t2m: { optimizePrompt: '本模型专用模板' } },
      },
    });
    expect(screen.queryAllByText('已定制').length).toBeGreaterThan(0);
  });

  // 纯空格进不了运行时（getModelOptimizePrompt 先 trim），管理端就不能说「已定制」。
  // setTabField 只在值恰好等于 '' 时删键，所以这个状态真的存在于草稿里。
  it('只敲了空格 = 仍是跟随本 tab，不能显示已定制', () => {
    renderTab({
      'minimax-music3': {
        engine: MUSIC_ENGINE_MINIMAX_MUSIC3,
        tabs: { t2m: { optimizePrompt: '   \n  ' } },
      },
    });
    expect(screen.queryAllByText('已定制')).toHaveLength(0);
    expect(screen.queryAllByText('跟随本 tab').length).toBeGreaterThan(0);
  });
});

// ---------------------------------------------------------------------------
// 图像画幅：一键填入推荐档 + 「两套都配了」的告警
// ---------------------------------------------------------------------------
import { getPlaygroundTab as getTab } from '../../../constants/playgroundAdmin.constants';

const T2I = getTab('image', 'text2image');

const makeImageDraft = (models, setTabField, setModelField) => ({
  stores: { ImageModelSizeConfig: { models, defaults: {} } },
  tabConfig: {},
  allModels: [],
  options: {},
  patchTabConfig: vi.fn(),
  setTabField: setTabField || vi.fn(),
  setModelField: setModelField || vi.fn(),
  addModelToTab: vi.fn(),
  removeModelFromTab: vi.fn(),
});

const renderImageTab = (models, setTabField, setModelField) =>
  render(
    <TabPanel
      category='image'
      tab={T2I}
      draft={makeImageDraft(models, setTabField, setModelField)}
    />,
  );

describe('图像画幅：推荐档一键填入', () => {
  it('有实测推荐档的模型才出按钮', () => {
    renderImageTab({ 'sensenova-u1.5': { tabs: { text2image: {} } } });
    expect(screen.queryByText(/填入推荐档位/)).not.toBeNull();
  });

  // 第三方模型（gpt-image / dall-e / nano-banana 等）**永远不该有推荐档**：它们只认
  // 各自枚举的固定尺寸，面积档算出的 1344x768 这类值会被直接拒（dall-e 系在
  // relay/helper/valid_request.go 本地就 400，gpt-image 系由上游拒）。运营给它们配
  // 尺寸列表即可，一键填入不替没实测过的模型做决定。
  it('没有实测过的模型不出按钮——没验证过的东西不该摆在一键填入里', () => {
    renderImageTab({ 'gpt-image-2': { tabs: { text2image: {} } } });
    expect(screen.queryByText(/填入推荐档位/)).toBeNull();
  });

  // U1.5 是 area 档（比例+档位），点完必须把 sizes 清掉：留着就会立刻触发下面那条
  // 「两套都配了」的告警，等于按钮自己造了个矛盾。
  it('填入 area 档时把 sizes 清空，并写模型级对齐粒度', () => {
    const setTabField = vi.fn();
    const setModelField = vi.fn();
    renderImageTab(
      { 'sensenova-u1.5': { tabs: { text2image: {} } } },
      setTabField,
      setModelField,
    );
    screen
      .getByText(/填入推荐档位/)
      .closest('button')
      .click();
    const calls = Object.fromEntries(
      setTabField.mock.calls.map((c) => [c[3], c[4]]),
    );
    expect(calls.aspectRatios).toEqual(['1:1', '3:2', '2:3', '16:9', '9:16']);
    // 2026-09-03 实测三档全部原样返回（1024² 15.5s / 1536² 28.1s / 2048² 50.9s）。
    // 4096 基准同样可用但 223s，逼近同步链路上限，故不进推荐档。
    expect(calls.sizeTiers).toEqual(['1024', '1536', '2048']);
    expect(calls.sizes).toBeUndefined();
    expect(setModelField).toHaveBeenCalledWith(
      'ImageModelSizeConfig',
      'sensenova-u1.5',
      'sizeAlign',
      32,
    );
  });

  // HunyuanImage-3.0 反过来：面积被权重锁死在 ~1MP（checkpoint 的 image_base_size
  // 同时是喂给模型的条件 token <img_size_1024>，实测请求里带 image_base_size:2048
  // 依然出 1024x1024），没有"更高质量"这个维度，所以推荐档给的是 sizes、清掉比例与档位。
  it('填入 table 档时把比例与档位清空', () => {
    const setTabField = vi.fn();
    renderImageTab(
      { 'hunyuan-image-3': { tabs: { text2image: {} } } },
      setTabField,
    );
    screen
      .getByText(/填入推荐档位/)
      .closest('button')
      .click();
    const calls = Object.fromEntries(
      setTabField.mock.calls.map((c) => [c[3], c[4]]),
    );
    expect(calls.sizes).toContain('1280x720');
    expect(calls.aspectRatios).toBeUndefined();
    expect(calls.sizeTiers).toBeUndefined();
  });
});

describe('图像画幅：「配了但不生效」都要告警', () => {
  // 砍掉 ratio 模式后，sizes 里的比例词不再被"提升"成宽高比 —— 这种配置现在是
  // 「只配了档位、没配比例」，属于不成对，尺寸列表照常生效。告警要说清这一点，
  // 否则运营会以为档位已经在起作用。
  // 断言必须指向**告警本身**（⚠️ 开头那条），不能只匹配「必须成对配置」：
  // 同一页的「宽高比」「画质档」两处字段说明也写了这句话（提前提示与事后告警口径
  // 一致是有意为之），只匹配这五个字会撞上多个元素、报「Found multiple elements」，
  // 而那个报错看着像"告警没出现"，实际是断言太松。
  const WARN_PAIR = /⚠️ 「宽高比」与「画质档」必须成对配置/;

  it('只配了画质档、没配比例 → 提示必须成对', () => {
    renderImageTab({
      m: {
        tabs: {
          text2image: {
            sizes: ['1:1', '16:9', '1024x1024'],
            sizeTiers: [2048],
          },
        },
      },
    });
    expect(screen.queryByText(WARN_PAIR)).not.toBeNull();
  });

  it('只配了宽高比、没配画质档 → 同样提示必须成对', () => {
    renderImageTab({
      m: {
        tabs: { text2image: { sizes: ['1024x1024'], aspectRatios: ['16:9'] } },
      },
    });
    expect(screen.queryByText(WARN_PAIR)).not.toBeNull();
  });

  // 配齐了比例与档位、同时还配了尺寸 → 画幅按算出来的像素走，那份尺寸列表一个值
  // 都不会被用到。
  it('配齐比例档位时，尺寸列表不生效要告警', () => {
    renderImageTab({
      m: {
        tabs: {
          text2image: {
            sizes: ['1024x1024'],
            aspectRatios: ['16:9'],
            sizeTiers: [2048],
          },
        },
      },
    });
    expect(screen.queryByText(/那份列表一个值都不会生效/)).not.toBeNull();
  });

  it('只配一套时不告警', () => {
    renderImageTab({
      m: {
        tabs: { text2image: { aspectRatios: ['16:9'], sizeTiers: [2048] } },
      },
    });
    expect(screen.queryByText(/不会生效/)).toBeNull();
  });
});
