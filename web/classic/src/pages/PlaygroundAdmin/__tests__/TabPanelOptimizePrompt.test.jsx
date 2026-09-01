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
