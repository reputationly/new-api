import React from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, act, screen } from '@testing-library/react';

vi.mock('../../../../helpers', () => ({
  API: { get: vi.fn(), put: vi.fn() },
  showError: vi.fn(),
  showSuccess: vi.fn(),
}));

import SettingsHiloCatalog from '../SettingsHiloCatalog';
import { API, showError } from '../../../../helpers';

// 这个组件此前连着三轮检视都在出新缺陷，原因是**没人渲染过它** ——
// 每一版都是盲写。下面每个用例对应一条真实踩过的坑，不是为覆盖率凑的。

const FACTORY = {
  image: [
    {
      platform_model: 'qwen-image',
      model: { id: 'qwen-image', name: 'Qwen', backend: 'qwen', type: 'image', display_name: 'Qwen', params: {} },
    },
  ],
  video: [
    {
      // **存的是聚合编排名，而 model.id 是 MiniMax-H3** —— 两者不同,
      // 正是「载入出厂目录」曾经猜错的地方。
      platform_model: 'minimax-h3-2k',
      model: { id: 'MiniMax-H3', name: 'H3', backend: 'minimax_v3', type: 'video', display_name: 'H3', params: {} },
    },
  ],
  audio: [],
};

const AGG = JSON.stringify([
  { name: 'minimax-h3-2k', enabled: true },
  { name: 'minimax-h3-ref-2k', enabled: false },
]);

const renderPage = async (options) => {
  let utils;
  await act(async () => {
    utils = render(
      <SettingsHiloCatalog options={options} refresh={vi.fn()} />,
    );
  });
  return utils;
};

const clickText = async (text) => {
  const el = [...document.querySelectorAll('button')].find((b) =>
    b.textContent.includes(text),
  );
  if (!el) throw new Error(`找不到按钮：${text}`);
  await act(async () => {
    el.dispatchEvent(new MouseEvent('click', { bubbles: true }));
  });
  return el;
};

beforeEach(() => {
  vi.clearAllMocks();
  // 候选**一定是挂载之后才到的** —— 这正是 Semi 那个 bug 的触发条件,
  // 一开始就把候选传进去的话，坏掉的写法也能过。
  API.get.mockImplementation((url) => {
    if (url === '/api/models/pricing') {
      return Promise.resolve({ data: { data: [{ model_name: 'qwen-image' }] } });
    }
    if (url === '/api/option/hilo_catalog_default') {
      return Promise.resolve({ data: { data: JSON.stringify(FACTORY) } });
    }
    return Promise.resolve({ data: {} });
  });
  API.put.mockResolvedValue({ data: { success: true } });
});

afterEach(() => {
  // Semi 的下拉挂在 document.body 上，RTL 的 cleanup 只回收自己建的容器。
  document.body.innerHTML = '';
});

describe('空配置 ≠ 空目录', () => {
  // 服务端语义：配置为空串 → 用出厂目录。而表格全空时序列化出来是
  // `{"image":[],...}`，那是一份**明确的空目录**，会被原样接受 ——
  // 于是所有客户端的模型一起消失，而管理员什么都没删。
  it('出厂状态下直接保存会被拦住，不会存成空目录', async () => {
    await renderPage({ HiloCatalog: '' });
    await clickText('保存');
    expect(API.put).not.toHaveBeenCalled();
    expect(showError).toHaveBeenCalled();
  });

  it('**原文模式也要拦** —— 它曾经是一条绕过去的路', async () => {
    await renderPage({ HiloCatalog: '' });
    // 「编辑原文 JSON」在出厂态会把空目录预填进去，一个字不改直接保存
    // 就把所有模型清空了。
    await clickText('编辑原文 JSON');
    await clickText('保存');
    expect(API.put).not.toHaveBeenCalled();
    expect(showError).toHaveBeenCalled();
  });

  it('「恢复出厂目录」要先确认，确认后存的是空串而不是空目录', async () => {
    await renderPage({ HiloCatalog: JSON.stringify(FACTORY) });
    await clickText('恢复出厂目录');

    // **点一下不能直接生效。** 它是一次立即的 PUT，会丢掉管理员攒的整份
    // 自定义配置且无法撤销，按钮又紧挨着「保存」。
    expect(API.put).not.toHaveBeenCalled();

    // 确认框里的「确定」。Semi 的 Modal 挂在 body 上。
    const ok = [...document.querySelectorAll('.semi-modal button')].find((b) =>
      /确定|确认|OK/i.test(b.textContent),
    );
    expect(ok, '应当弹出确认框').toBeTruthy();
    await act(async () => {
      ok.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    });

    // 存的是**空串**（= 用出厂目录），不是 `{"image":[],…}`（= 真的没有模型）。
    expect(API.put).toHaveBeenCalledWith('/api/option/', {
      key: 'HiloCatalog',
      value: '',
    });
  });

  it('有内容时正常保存', async () => {
    await renderPage({ HiloCatalog: JSON.stringify(FACTORY) });
    await clickText('保存');
    expect(API.put).toHaveBeenCalledTimes(1);
    const sent = JSON.parse(API.put.mock.calls[0][1].value);
    expect(sent.video[0].platform_model).toBe('minimax-h3-2k');
  });
});

describe('载入出厂目录', () => {
  // 曾经是从 `/api/v1/models/config` 反推的，而那份响应**没有
  // platform_model** —— H3 那条会被猜成 `MiniMax-H3`，保存之后
  // 要么丢掉 2K 编排、要么被后端过滤掉。
  it('走专用接口，保住 platform_model 和 model.id 的差异', async () => {
    await renderPage({ HiloCatalog: '' });
    await clickText('载入出厂目录');
    expect(API.get).toHaveBeenCalledWith('/api/option/hilo_catalog_default');

    await clickText('保存');
    const sent = JSON.parse(API.put.mock.calls[0][1].value);
    expect(sent.video[0].platform_model).toBe('minimax-h3-2k');
    expect(sent.video[0].model.id).toBe('MiniMax-H3');
  });

  it('**不从下发接口反推** —— 那条路没有 platform_model', async () => {
    await renderPage({ HiloCatalog: '' });
    await clickText('载入出厂目录');
    expect(API.get).not.toHaveBeenCalledWith('/api/v1/models/config');
  });
});

describe('平台模型候选', () => {
  it('聚合模型要并进候选，而且只并启用的那些', async () => {
    await renderPage({
      HiloCatalog: JSON.stringify(FACTORY),
      AggregateModelConfig: AGG,
    });

    // **必须真的把下拉打开看候选。**
    // 早先这里写的是 `expect(html).toContain('minimax-h3-2k')`，而那个字符串
    // 本来就作为 H3 那行的**值**渲染在 DOM 里 —— 把并入候选的代码整段删掉,
    // 断言照样通过。校准时发现的：一个永远不会失败的测试比没有测试更糟,
    // 它会让人以为这里被守住了。
    const selects = [...document.querySelectorAll('.semi-select')];
    expect(selects.length).toBeGreaterThan(0);
    await act(async () => {
      selects[0].dispatchEvent(new MouseEvent('click', { bubbles: true }));
    });
    const options = [...document.querySelectorAll('.semi-select-option')].map(
      (o) => o.textContent,
    );

    // 出厂默认值就是聚合模型，它必须在候选里，否则管理员从界面重选只能
    // 选到裸模型，静默把 2K 编排换掉。
    expect(options.some((o) => o.includes('minimax-h3-2k'))).toBe(true);
    // 停用的不该出现 —— 选了它等于承诺一条不跑的流水线。
    expect(options.some((o) => o.includes('minimax-h3-ref-2k'))).toBe(false);
    // 裸模型也还在。
    expect(options.some((o) => o.includes('qwen-image'))).toBe(true);
  });
});

describe('表格行', () => {
  it('每行 key 唯一', async () => {
    const two = {
      ...FACTORY,
      image: [
        FACTORY.image[0],
        {
          platform_model: 'qwen-image-edit',
          model: { id: 'qwen-image-edit', name: 'E', backend: 'qwen', type: 'image', display_name: 'E', params: {} },
        },
      ],
    };
    await renderPage({ HiloCatalog: JSON.stringify(two) });

    // rowKey 回调只收 record 不收 index（semi-foundation 的 getRecordKey），
    // 写成 `(r, i) => ...` 的话一组里所有行同 key，增删行时 React 会把
    // 有状态的编辑器 reconcile 到错的行上。
    //
    // **必须断言组件真正渲染出来的 key。** 这里第一版写的是
    // `two.image.map((_, i) => ...)` —— 那个数组是测试自己造的，和组件
    // 没有任何关系，把 __rowKey 整段删掉也照样通过。又一个"永远不会
    // 失败的测试"。
    const keys = [...document.querySelectorAll('tbody tr[data-row-key]')].map(
      (tr) => tr.getAttribute('data-row-key'),
    );
    expect(keys.length).toBeGreaterThanOrEqual(two.image.length);
    expect(new Set(keys).size).toBe(keys.length);
    expect(keys.some((k) => k.includes('undefined'))).toBe(false);
  });
});

describe('坏配置', () => {
  it('存量配置解析不了时切到原文模式，并把原文显示出来', async () => {
    await renderPage({ HiloCatalog: '{ 这不是 json' });
    expect(showError).toHaveBeenCalled();
    // 这时候更要让人看见原文，而不是一个看起来正常的空表格。
    expect(document.body.innerHTML).toContain('这不是 json');
  });
});
