import React, { useCallback, useState } from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import ModelRatioEditor from '../components/ModelRatioEditor';
import { API } from '../../../helpers';

/**
 * 在第二页改折扣值，页码不能跳回第一页。
 *
 * Semi Table 的内置分页是非受控的，dataSource 换引用就重置到第一页。而这张表的
 * rows 每敲一个键都会重建（emitAndSet 要把最新值序列化给父组件），于是在第二页
 * 刚输入一个数字，正在改的那一行就被弹走了——配一批模型时每条都要重新翻页找。
 *
 * 断言方式是「那一行还在 DOM 里」而不是看页码组件：Semi Table 只渲染当前页，
 * 行被弹到别的页就是从 DOM 里消失，这正是用户看不到它的原因。
 */

vi.mock('../../../helpers', async () => {
  const actual = await vi.importActual('../../../helpers');
  return { ...actual, API: { get: vi.fn(), post: vi.fn(), put: vi.fn() } };
});

/** 构造 12 条既有规则，跨两页（每页 10 条） */
function rulesJSON(count) {
  const rules = {};
  for (let i = 1; i <= count; i += 1) {
    rules[`model-${String(i).padStart(2, '0')}`] = {
      mode: 'multiply',
      value: 0.9,
    };
  }
  return JSON.stringify({ vip: rules });
}

function Harness({ initial }) {
  const [value, setValue] = useState(initial);
  const onChange = useCallback((v) => setValue(v), []);
  return (
    <ModelRatioEditor
      group='vip'
      groupRatio={1}
      value={value}
      onChange={onChange}
    />
  );
}

describe('精确规则表的分页稳定性', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    API.get.mockResolvedValue({ data: { success: true, data: [] } });
  });

  it('在第二页改折扣值后，仍然停留在第二页', async () => {
    render(<Harness initial={rulesJSON(12)} />);

    // 前提：确实分页了，第 11、12 条在第二页
    expect(await screen.findByDisplayValue('model-01')).toBeInTheDocument();
    expect(screen.queryByDisplayValue('model-12')).not.toBeInTheDocument();

    await userEvent.click(screen.getByLabelText('Page 2'));
    const row12 = await screen.findByDisplayValue('model-12');
    expect(row12).toBeInTheDocument();
    // 第一页的行已经让位，确认翻页真的生效了
    expect(screen.queryByDisplayValue('model-01')).not.toBeInTheDocument();

    // 改这一页某行的折扣值。InputNumber 渲染的不是 textbox，按显示值定位
    const valueInputs = screen.getAllByDisplayValue('0.9');
    expect(valueInputs.length).toBeGreaterThan(0);
    await userEvent.type(valueInputs[0], '5');

    // 输入后不能被弹回第一页——正在改的那行必须还在
    await waitFor(() =>
      expect(screen.getByDisplayValue('model-12')).toBeInTheDocument(),
    );
    expect(screen.queryByDisplayValue('model-01')).not.toBeInTheDocument();
  });

  it('结果集缩短导致末页消失时，页码收敛而不是停在空白页', async () => {
    render(<Harness initial={rulesJSON(11)} />);
    await screen.findByDisplayValue('model-01');

    // 第 11 条独占第二页
    await userEvent.click(screen.getByLabelText('Page 2'));
    expect(await screen.findByDisplayValue('model-11')).toBeInTheDocument();

    // 搜索把结果缩到一条，第二页不再存在。
    // 关键词用 '-01' 而不是 'model-01'：后者会让搜索框自己的值也命中断言，
    // 分不清「表格里那行」和「搜索框里那串字」。
    await userEvent.type(screen.getByPlaceholderText('搜索模型或备注'), '-01');

    // 必须回落到第一页，而不是停在一张空表上（那看起来像规则全丢了）
    await waitFor(() =>
      expect(screen.getByDisplayValue('model-01')).toBeInTheDocument(),
    );
  });
});
