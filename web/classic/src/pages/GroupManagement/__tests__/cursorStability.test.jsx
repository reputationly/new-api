import React, { useCallback, useState } from 'react';
import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import ModelRatioEditor from '../components/ModelRatioEditor';
import GroupTable from '../components/GroupTable';

/**
 * 可编辑表格的光标稳定性。
 *
 * 这个 bug 在本仓库出现过三次，每次都只能靠人眼发现：`columns` 的 useMemo 依赖了
 * 某个每次敲键都会变的值 → Semi 的 Table 重建单元格 → 输入框光标跳到末尾。
 * 症状是「在中间插字，光标瞬间弹到最后」，用户改一个模型名要重新定位十几次。
 *
 * 断言方式刻意选了**在中间插字**而不是在末尾追加：光标跳到末尾时，末尾追加的
 * 结果和正常情况一模一样，测不出任何差别——这正是它当初能连续三次逃过自测的原因。
 *
 * 只覆盖用 Semi `Input` 的表格。实测 `InputNumber` 在 columns 不稳定时光标依然
 * 保持不动（对照实验：Input → 8，InputNumber → 2），所以给纯 InputNumber 的表
 * （GroupExtraSettings）写这条断言是个永远不会失败的假绿，删掉了。
 * 那里的依赖稳定性由 scripts/check-stable-columns.mjs 兜底。
 */

/** 复刻 index.jsx 的受控用法：子组件 onChange → 父组件 setState → 新 value 回流 */
function ControlledModelRatioEditor({ initial }) {
  const [value, setValue] = useState(initial);
  const onChange = useCallback((v) => setValue(v), []);
  return (
    <ModelRatioEditor
      group='premium'
      groupRatio={1.5}
      value={value}
      onChange={onChange}
    />
  );
}

function ControlledGroupTable({ groupRatio, userUsableGroups }) {
  const [state, setState] = useState({ groupRatio, userUsableGroups });
  const onChange = useCallback(
    ({ GroupRatio, UserUsableGroups }) =>
      setState({ groupRatio: GroupRatio, userUsableGroups: UserUsableGroups }),
    [],
  );
  return (
    <GroupTable
      groupRatio={state.groupRatio}
      userUsableGroups={state.userUsableGroups}
      onChange={onChange}
    />
  );
}

/**
 * 把光标放到 `caretAt`，敲一个字，断言光标停在插入字符之后（而不是被弹到末尾）。
 */
async function expectCaretStable(input, caretAt, char = 'X') {
  const user = userEvent.setup();
  const before = input.value;

  input.setSelectionRange(caretAt, caretAt);
  await user.type(input, char, {
    initialSelectionStart: caretAt,
    initialSelectionEnd: caretAt,
  });

  const expected = before.slice(0, caretAt) + char + before.slice(caretAt);
  expect(input.value).toBe(expected);
  expect(input.selectionStart).toBe(caretAt + char.length);
}

describe('ModelRatioEditor 模型名输入框', () => {
  it('在中间插字时光标不跳到末尾', async () => {
    render(
      <ControlledModelRatioEditor
        initial={JSON.stringify({
          premium: { 'GLM-5': { mode: 'multiply', value: 0.5 } },
        })}
      />,
    );

    const input = screen.getByDisplayValue('GLM-5');
    await expectCaretStable(input, 2);
  });
});

describe('GroupTable 分组名输入框', () => {
  it('在中间插字时光标不跳到末尾', async () => {
    render(
      <ControlledGroupTable
        groupRatio={JSON.stringify({ premium: 1.5 })}
        userUsableGroups={JSON.stringify({ premium: '高级' })}
      />,
    );

    const input = screen.getByDisplayValue('premium');
    await expectCaretStable(input, 3);
  });
});
