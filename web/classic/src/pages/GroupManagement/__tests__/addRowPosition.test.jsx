import React, { useCallback, useState } from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import ModelRatioEditor from '../components/ModelRatioEditor';
import { API } from '../../../helpers';

/**
 * 新增规则行必须落在**当前可见的那一页**。
 *
 * 精确规则表是分页的（每页 10 条）。新行追加到末尾时，规则一超过一页就落到最后一页，
 * 而表格数据一变又回到第一页——点了「添加」，界面上什么都没发生。人的反应是再点几次，
 * 于是多出好几条空规则，还得回头删。逐条录入十几个模型时每次都撞上。
 *
 * 断言方式是「DOM 里出现了一个空的模型名输入框」而不是「数组长度 +1」：Semi Table
 * 只渲染当前页，落到第二页的行根本不在 DOM 里——这正是用户看不到它的原因，也是
 * 数组长度断言测不出这个缺陷的原因。
 */

vi.mock('../../../helpers', async () => {
  const actual = await vi.importActual('../../../helpers');
  return { ...actual, API: { get: vi.fn(), post: vi.fn(), put: vi.fn() } };
});

/** 构造超过一页（10 条）的既有规则 */
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

/**
 * 模型名那一列的 Input 没有 placeholder，是表里唯一没有的——备注列有
 * 「为什么是这个价」，搜索框有「搜索模型或备注」。所以「无 placeholder 且值为空」
 * 精确对应「刚添加、还没填的规则行」。
 */
function emptyPatternInputs() {
  return screen
    .getAllByRole('textbox')
    .filter((el) => !el.placeholder && el.value === '');
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

describe('新增规则行的落点', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    API.get.mockResolvedValue({ data: { success: true, data: [] } });
  });

  it('已有 12 条（超过一页）时，新行仍然可见', async () => {
    render(<Harness initial={rulesJSON(12)} />);

    // 前提：确实分页了，第 11、12 条不在首页
    expect(await screen.findByDisplayValue('model-01')).toBeInTheDocument();
    expect(screen.queryByDisplayValue('model-12')).not.toBeInTheDocument();

    await userEvent.click(screen.getByText('自定义/通配规则'));

    // 新行是空的模型名输入框；若追加到末尾，它会落到第二页而不在 DOM 里
    await waitFor(() => expect(emptyPatternInputs().length).toBe(1));
  });

  it('规则不足一页时同样可见（不因插入位置改变而回归）', async () => {
    render(<Harness initial={rulesJSON(2)} />);
    await screen.findByDisplayValue('model-01');

    await userEvent.click(screen.getByText('自定义/通配规则'));

    await waitFor(() => expect(emptyPatternInputs().length).toBe(1));
  });
});
