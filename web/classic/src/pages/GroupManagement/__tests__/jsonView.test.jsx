import React, { useCallback, useState } from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import ModelRatioEditor from '../components/ModelRatioEditor';
import { API } from '../../../helpers';

/**
 * JSON 视图与表格视图必须逐条对应。
 *
 * 逐个下拉添加模型在配几十个模型时是纯体力活，所以给了一个可整段粘贴的 JSON 视图。
 * 它的全部价值建立在「两个视图是同一份数据的两种画法」上——一旦切过去再切回来
 * 会掉字段、改数值，这个功能就比没有更糟：人会以为自己配好了。
 */

vi.mock('../../../helpers', async () => {
  const actual = await vi.importActual('../../../helpers');
  return {
    ...actual,
    API: { get: vi.fn(), post: vi.fn(), put: vi.fn() },
    showSuccess: vi.fn(),
    showError: vi.fn(),
  };
});

function Harness({ initial, ...rest }) {
  const [value, setValue] = useState(initial);
  const onChange = useCallback((v) => setValue(v), []);
  return (
    <>
      <ModelRatioEditor
        group='default'
        groupRatio={1}
        value={value}
        onChange={onChange}
        {...rest}
      />
      {/* 把父组件持有的那份 JSON 暴露出来，断言的是真正会被保存的东西，
          而不是编辑器内部的中间状态 */}
      <textarea data-testid='committed' readOnly value={value} />
    </>
  );
}

const committed = () => JSON.parse(screen.getByTestId('committed').value);
const jsonBox = () => screen.getByPlaceholderText(/"GLM-5.2"/);

/**
 * userEvent.type 把 `{` 当成特殊键的起始符，字面量要写成 `{{`；`}` 不特殊。
 * 手写转义太容易多写少写一个括号，那样 JSON 本身就是坏的，测出来的是解析失败
 * 而不是想测的那条校验规则——所以统一走这个函数。
 */
const typeJson = async (el, obj) => {
  // 切到 JSON 视图时框里已经有内容（空分组也是 "{}"），不清就是往后追加，
  // 得到的是语法错误而不是想测的那条校验规则
  await userEvent.clear(el);
  await userEvent.type(el, JSON.stringify(obj).replace(/\{/g, '{{'));
};

/** 展开「同步到其他分组」的 Dropdown 并点中目标分组——菜单渲染在 portal 里 */
const pickSyncTarget = async (target) => {
  await userEvent.click(screen.getByText('同步到其他分组'));
  const item = await waitFor(() => {
    const el = Array.from(
      document.querySelectorAll('.semi-dropdown-item'),
    ).find((n) => n.textContent === target);
    expect(el).toBeTruthy();
    return el;
  });
  await userEvent.click(item);
};

describe('模型折扣的 JSON 视图', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    API.get.mockResolvedValue({ data: { success: true, data: [] } });
  });

  it('切到 JSON 显示的是当前分组的规则，不含其他分组', async () => {
    render(
      <Harness
        initial={JSON.stringify({
          default: { 'model-a': { mode: 'multiply', value: 0.5 } },
          premium: { 'model-b': { mode: 'multiply', value: 0.9 } },
        })}
      />,
    );

    await userEvent.click(await screen.findByText('切换到 JSON'));

    const shown = JSON.parse(jsonBox().value);
    expect(shown).toEqual({ 'model-a': { mode: 'multiply', value: 0.5 } });
    expect(shown).not.toHaveProperty('premium');
  });

  it('JSON 改完切回表格，改动落到父组件且其他分组不受影响', async () => {
    render(
      <Harness
        initial={JSON.stringify({
          default: { 'model-a': { mode: 'multiply', value: 0.5 } },
          premium: { 'model-b': { mode: 'multiply', value: 0.9 } },
        })}
      />,
    );

    await userEvent.click(await screen.findByText('切换到 JSON'));
    await typeJson(jsonBox(), {
      'model-c': { mode: 'override', value: 2 },
    });

    await waitFor(() =>
      expect(committed().default).toEqual({
        'model-c': { mode: 'override', value: 2 },
      }),
    );
    // 其他分组必须原样保留——JSON 视图的作用域是当前分组
    expect(committed().premium).toEqual({
      'model-b': { mode: 'multiply', value: 0.9 },
    });

    await userEvent.click(screen.getByText('切换到表格'));
    expect(await screen.findByDisplayValue('model-c')).toBeInTheDocument();
  });

  it('JSON 非法时不提交，父组件保持原值', async () => {
    render(
      <Harness
        initial={JSON.stringify({
          default: { 'model-a': { mode: 'multiply', value: 0.5 } },
        })}
      />,
    );

    await userEvent.click(await screen.findByText('切换到 JSON'));
    // 故意不 clear：在合法内容末尾追加垃圾字符。清空框会被当作「该分组没有规则」
    // 而提交一次空值，那是另一条语义，会盖掉这里要断言的原值
    await userEvent.type(jsonBox(), 'x');

    expect(await screen.findByText(/JSON 无效/)).toBeInTheDocument();
    // 原值一字未动
    expect(committed().default).toEqual({
      'model-a': { mode: 'multiply', value: 0.5 },
    });
    // 解析不了就不许切回表格，否则看到的是旧数据而人以为新的已经生效
    await userEvent.click(screen.getByText('切换到表格'));
    expect(jsonBox()).toBeInTheDocument();
  });

  it('校验口径与后端一致：拒绝中间带 * 的模式串', async () => {
    render(<Harness initial='{}' />);

    await userEvent.click(await screen.findByText('切换到 JSON'));
    await typeJson(jsonBox(), {
      'GLM-*-Flash': { mode: 'multiply', value: 0.9 },
    });

    expect(await screen.findByText(/只能放在结尾/)).toBeInTheDocument();
    expect(committed()).toEqual({});
  });

  it('接受空 mode —— 后端把它归一成 multiply，前端不能更严', async () => {
    render(<Harness initial='{}' />);

    await userEvent.click(await screen.findByText('切换到 JSON'));
    await typeJson(jsonBox(), { 'model-a': { mode: '', value: 0.5 } });

    expect(screen.queryByText(/JSON 无效/)).toBeNull();
    // 归一成 multiply 后提交，与 CheckGroupModelRatio 的 case "" 分支一致
    await waitFor(() =>
      expect(committed().default).toEqual({
        'model-a': { mode: 'multiply', value: 0.5 },
      }),
    );
  });

  it('拒绝非字符串 mode —— 后端 unmarshal 会直接失败', async () => {
    render(<Harness initial='{}' />);

    await userEvent.click(await screen.findByText('切换到 JSON'));
    await typeJson(jsonBox(), { 'model-a': { mode: 0, value: 0.5 } });

    expect(await screen.findByText(/mode 必须是字符串/)).toBeInTheDocument();
    expect(committed()).toEqual({});
  });

  it('allowOverride=false 时拒绝 override（档位折扣复用同一编辑器）', async () => {
    render(<Harness initial='{}' allowOverride={false} />);

    await userEvent.click(await screen.findByText('切换到 JSON'));
    await typeJson(jsonBox(), { 'GLM-5.2': { mode: 'override', value: 2 } });

    expect(await screen.findByText(/不允许 override/)).toBeInTheDocument();
    expect(committed()).toEqual({});
  });

  it('表格与 JSON 往返一圈，规则逐字段不变', async () => {
    const original = {
      'model-a': { mode: 'multiply', value: 0.5, remark: '自建' },
      'wan2.2-*': { mode: 'override', value: 3 },
    };
    render(<Harness initial={JSON.stringify({ default: original })} />);

    await userEvent.click(await screen.findByText('切换到 JSON'));
    expect(JSON.parse(jsonBox().value)).toEqual(original);

    // 一个字都不改地切回去，父组件里的那份必须逐字段等同
    await userEvent.click(screen.getByText('切换到表格'));
    await waitFor(() => expect(committed().default).toEqual(original));
  });

  it('同步到其他分组：把当前分组的规则整份复制过去', async () => {
    render(
      <Harness
        initial={JSON.stringify({
          default: { 'model-a': { mode: 'multiply', value: 0.5 } },
          premium: { 'old-rule': { mode: 'multiply', value: 0.1 } },
        })}
        syncTargets={['default', 'premium']}
      />,
    );

    await screen.findByText('切换到 JSON');
    await pickSyncTarget('premium');

    await waitFor(() =>
      expect(committed().premium).toEqual({
        'model-a': { mode: 'multiply', value: 0.5 },
      }),
    );
    // 源分组不受影响
    expect(committed().default).toEqual({
      'model-a': { mode: 'multiply', value: 0.5 },
    });
  });

  it('没有可同步的目标时不渲染同步下拉', async () => {
    render(<Harness initial='{}' syncTargets={['default']} />);
    await screen.findByText('切换到 JSON');
    expect(screen.queryByText('同步到其他分组')).toBeNull();
  });
});
