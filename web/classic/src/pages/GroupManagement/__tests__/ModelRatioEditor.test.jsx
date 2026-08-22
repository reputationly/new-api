import React, { useCallback, useState } from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import ModelRatioEditor from '../components/ModelRatioEditor';
import { API } from '../../../helpers';

/**
 * 折扣编辑器写回 GroupModelRatio 的行为。
 *
 * 这里存的东西直接决定后端怎么扣钱：写坏一个分组的规则、或者把别的分组的规则
 * 顺手抹掉，都不会有任何报错，只会在下一次调用时按错的价扣。
 */

vi.mock('../../../helpers', async () => {
  const actual = await vi.importActual('../../../helpers');
  return { ...actual, API: { get: vi.fn(), post: vi.fn(), put: vi.fn() } };
});

function Harness({ initial, onValue }) {
  const [value, setValue] = useState(initial);
  const onChange = useCallback(
    (v) => {
      setValue(v);
      onValue?.(v);
    },
    [onValue],
  );
  return (
    <ModelRatioEditor
      group='premium'
      groupRatio={1.5}
      value={value}
      onChange={onChange}
    />
  );
}

beforeEach(() => {
  // 模型下拉的数据源。给一个空列表就够，用例不依赖它
  API.get.mockResolvedValue({ data: { success: true, data: [] } });
});

describe('ModelRatioEditor 序列化', () => {
  it('改一个分组的规则时不动其他分组', async () => {
    const seen = [];
    render(
      <Harness
        initial={JSON.stringify({
          premium: { 'GLM-5': { mode: 'multiply', value: 0.5 } },
          default: { 'gpt-4o': { mode: 'multiply', value: 0.9 } },
        })}
        onValue={(v) => seen.push(v)}
      />,
    );

    const user = userEvent.setup();
    await user.type(screen.getByDisplayValue('GLM-5'), '0');

    await waitFor(() => expect(seen.length).toBeGreaterThan(0));
    const written = JSON.parse(seen[seen.length - 1]);

    expect(written.default).toEqual({
      'gpt-4o': { mode: 'multiply', value: 0.9 },
    });
    expect(Object.keys(written.premium)).toEqual(['GLM-50']);
  });

  it('裸数字写法被读进来后按 multiply 处理，不吞掉手写的配置', async () => {
    const seen = [];
    render(
      <Harness
        initial={JSON.stringify({ premium: { 'GLM-5': 0.5 } })}
        onValue={(v) => seen.push(v)}
      />,
    );

    // 读进来时应展示成 multiply + 0.5
    expect(screen.getByDisplayValue('GLM-5')).toBeInTheDocument();
    expect(screen.getByDisplayValue('0.5')).toBeInTheDocument();

    const user = userEvent.setup();
    await user.type(screen.getByDisplayValue('GLM-5'), 'x');

    await waitFor(() => expect(seen.length).toBeGreaterThan(0));
    const written = JSON.parse(seen[seen.length - 1]);
    expect(written.premium['GLM-5x']).toMatchObject({
      mode: 'multiply',
      value: 0.5,
    });
  });

  it('「实际倍率」列按模式算：折扣乘基础倍率，定价直接取值', async () => {
    render(
      <Harness
        initial={JSON.stringify({
          premium: {
            discounted: { mode: 'multiply', value: 0.8 },
            repriced: { mode: 'override', value: 2.2 },
          },
        })}
      />,
    );

    // 基础倍率 1.5：折扣 0.8 → 1.2；定价 2.2 → 2.2（与基础倍率无关）
    expect(screen.getByText('1.2x')).toBeInTheDocument();
    expect(screen.getByText('2.2x')).toBeInTheDocument();
  });

  // 「定价 =」会吃掉针对用户分组配置的身份折扣（设计 §3.3），这是它最容易被误用的
  // 地方，页面必须主动提示——否则管理员配完只会在账单上发现 vip 没享受到折扣。
  //
  // 两种情况分成两个用例而不是 rerender：Harness 的 useState(initial) 只在挂载时
  // 求值，rerender 换 prop 不会重置内部 state，那样写出来的第二段其实什么都没测。
  it('只有折扣规则时不弹覆盖警告', () => {
    render(
      <Harness
        initial={JSON.stringify({
          premium: { a: { mode: 'multiply', value: 0.8 } },
        })}
      />,
    );
    expect(screen.queryByText(/会覆盖掉针对用户分组配置的身份折扣/)).toBeNull();
  });

  it('存在「定价 =」规则时弹出覆盖警告', () => {
    render(
      <Harness
        initial={JSON.stringify({
          premium: { a: { mode: 'override', value: 2 } },
        })}
      />,
    );
    expect(
      screen.getByText(/会覆盖掉针对用户分组配置的身份折扣/),
    ).toBeInTheDocument();
  });
});
