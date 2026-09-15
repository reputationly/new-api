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

  // **这个页面只渲染图片/视频/音频三段，但配置里还有 `text`（对话模型）。**
  //
  // 保存写的是 `JSON.stringify(catalog)` —— 不把未渲染的段留住，管理员在
  // 这里改一个显示名就会把整段对话模型**静默抹掉**：画布上突然选不到 LLM，
  // 而这里什么都不提示。实测踩过一次（差点又踩第二次）。
  it('保存时要保住页面不渲染的段（text 对话模型）', async () => {
    const withText = {
      ...FACTORY,
      text: [
        { platform_model: 'qwen3.8-27b', model: { id: 'qwen3.8-27b', name: 'Qwen3.8 27B' } },
      ],
    };
    await renderPage({ HiloCatalog: JSON.stringify(withText) });
    await clickText('保存');

    const sent = JSON.parse(API.put.mock.calls[0][1].value);
    expect(sent.text, '对话模型被静默抹掉了').toHaveLength(1);
    expect(sent.text[0].model.id).toBe('qwen3.8-27b');
    // 三段照常保存
    expect(sent.video[0].platform_model).toBe('minimax-h3-2k');
  });

  // **「编辑原文 JSON」也会丢。** 它把 catalog 预填进文本框，而原文保存是
  // `save(text)` —— 预填成什么就存什么。「进页面 → 点编辑原文 → 直接保存」
  // 一个字没改，对话模型就没了。
  it('「编辑原文 JSON」的预填要带上 text', async () => {
    const withText = {
      ...FACTORY,
      text: [{ platform_model: 'qwen3.8-27b', model: { id: 'qwen3.8-27b', name: 'Q' } }],
    };
    await renderPage({ HiloCatalog: JSON.stringify(withText) });
    await clickText('编辑原文 JSON');

    const box = document.querySelector('textarea');
    const prefilled = JSON.parse(box.value);
    expect(prefilled.text, '预填里没有 text，保存就会把它抹掉').toHaveLength(1);
    expect(prefilled.video[0].platform_model).toBe('minimax-h3-2k');
  });

  // **「载入出厂目录」也会丢。** 出厂目录本身带 text（见
  // setting/hilo_catalog.go 的 Text: defaultHiloTextCatalog()），
  // 只取三段的话，存下去就是一份没有对话模型的目录。
  it('「载入出厂目录」之后保存，text 要还在', async () => {
    const factoryWithText = {
      ...FACTORY,
      text: [{ platform_model: 'qwen3.8-27b', model: { id: 'qwen3.8-27b', name: 'Q' } }],
    };
    API.get.mockImplementation((url) => {
      if (url.includes('hilo_catalog_default')) {
        return Promise.resolve({
          data: { success: true, data: JSON.stringify(factoryWithText) },
        });
      }
      return Promise.resolve({ data: { success: true, data: [] } });
    });

    // 「载入出厂目录」只在出厂态（配置为空串）才渲染出来，所以从空配置进入。
    // 这也顺带保证了 text 只可能来自出厂目录，不是上一份配置残留的 ——
    // 空配置那条分支会把 passthrough 清空。
    await renderPage({ HiloCatalog: '' });
    await clickText('载入出厂目录');
    await clickText('保存');

    const sent = JSON.parse(API.put.mock.calls[0][1].value);
    expect(sent.text, '出厂目录里的 text 被丢掉了').toHaveLength(1);
  });

  // **「回到表格」不能把原文里的改动还原回去。**
  //
  // 这条比"丢数据"更阴险：管理员在原文模式里删掉 text，切回表格一保存，
  // 旧的 text 又被写回去，而界面提示「保存成功」—— 他以为删掉了，其实没有。
  it('原文里删掉 text 后回到表格，保存不该把它还原', async () => {
    const withText = {
      ...FACTORY,
      text: [{ platform_model: 'qwen3.8-27b', model: { id: 'qwen3.8-27b', name: 'Q' } }],
    };
    await renderPage({ HiloCatalog: JSON.stringify(withText) });
    await clickText('编辑原文 JSON');

    // 在原文里把 text 删掉
    const box = document.querySelector('textarea');
    const edited = JSON.parse(box.value);
    delete edited.text;
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLTextAreaElement.prototype, 'value',
      ).set;
      setter.call(box, JSON.stringify(edited, null, 2));
      box.dispatchEvent(new Event('input', { bubbles: true }));
    });

    await clickText('回到表格');
    await clickText('保存');

    const sent = JSON.parse(API.put.mock.calls[0][1].value);
    // 删掉之后存的是「没有对话模型」（缺键或空数组都算），关键是**不能**
    // 把旧那条还原回来 —— 那才是静默回滚。
    expect(sent.text ?? [], '管理员刚删掉的 text 又被还原回去了').toHaveLength(0);
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

// ── 对话模型这一组 ────────────────────────────────────────────────
//
// 加这一组之前，text 只靠 passthrough 保住不丢、页面上完全不可见：
// 管理员看到「共 5 个模型」而客户端上有 9 个，想调一条对话模型只能去
// 「编辑原文 JSON」里手写 —— 而那正是这个表格要替代的事。
describe('对话模型', () => {
  const WITH_TEXT = {
    ...FACTORY,
    text: [
      { platform_model: 'qwen3.8-27b', model: { id: 'qwen3.8-27b', name: 'Qwen3.8 27B', supportsVideo: true } },
      { platform_model: 'GPT-5.4', model: { id: 'GPT-5.4', name: 'GPT-5.4' } },
    ],
  };

  it('在表格里显示出来，并计入总数', async () => {
    await renderPage({ HiloCatalog: JSON.stringify(WITH_TEXT) });

    expect(screen.getByText('对话'), '没有对话分组').toBeTruthy();
    // 显示名渲染在 <input value> 里，不是文本节点
    const names = [...document.querySelectorAll('input.semi-input')].map((i) => i.value);
    expect(names, '对话模型没渲染出来').toContain('Qwen3.8 27B');
    expect(names).toContain('GPT-5.4');
    // 模型 ID 是文本节点
    expect(screen.getAllByText('qwen3.8-27b').length, '模型 ID 没显示').toBeGreaterThan(0);
    // 2 图/视 + 2 对话 = 4；早先总数只数媒体，和客户端实际拿到的对不上
    expect(screen.getByText(/共 4 个模型/)).toBeTruthy();
  });

  // **不能套媒体模型那套字段。** 整份目录里只要有一条 backend 不在枚举里，
  // 后端会拒掉**整份**目录 —— 表现是"一个模型都没有"，不是"少了一个"。
  it('新增一条对话模型不会带出 backend / params 这些键', async () => {
    await renderPage({ HiloCatalog: JSON.stringify(WITH_TEXT) });

    // 「+ 上架模型」每组一个，对话组是最后一个
    const adds = screen.getAllByText('+ 上架模型');
    await act(async () => adds[adds.length - 1].closest('button').click());
    await clickText('保存');

    const sent = JSON.parse(API.put.mock.calls[0][1].value);
    const added = sent.text[sent.text.length - 1];
    expect(sent.text).toHaveLength(3);
    for (const k of ['backend', 'params', 'tool_names', 'type']) {
      expect(added.model, `新增的对话模型带上了 ${k}，整份目录会被拒`).not.toHaveProperty(k);
    }
  });

  // 渲染出来之后它就不再是 passthrough 的一部分了（pickPassthrough 按
  // GROUPS 算），这条钉住两套机制没有互相打架、改动能真的存下去。
  it('改显示名能存下去', async () => {
    await renderPage({ HiloCatalog: JSON.stringify(WITH_TEXT) });

    const box = [...document.querySelectorAll('input.semi-input')]
      .find((i) => i.value === 'Qwen3.8 27B');
    expect(box, '对话模型的显示名不可编辑').toBeTruthy();
    await act(async () => {
      const setter = Object.getOwnPropertyDescriptor(
        window.HTMLInputElement.prototype, 'value',
      ).set;
      setter.call(box, '通义 27B');
      box.dispatchEvent(new Event('input', { bubbles: true }));
    });
    await clickText('保存');

    const sent = JSON.parse(API.put.mock.calls[0][1].value);
    expect(sent.text[0].model.name).toBe('通义 27B');
    expect(sent.text[1].model.id, '另一条被带坏了').toBe('GPT-5.4');
  });
});
