import React, { useCallback, useState } from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

/**
 * 结果集缩短时，中间不能有任何一帧切出空数组。
 *
 * 越界收敛如果只靠 useEffect，它是 passive 的、跑在 commit 之后：缩短的那一次
 * 渲染仍然拿着越界的 page，切片得到 []，表格先画出「该分组暂无模型折扣」再被
 * 纠正回来。数据没丢，但闪出来的那一帧和「规则全没了」长得一模一样。
 *
 * 这一帧在最终 DOM 上观测不到——RTL 的 act() 会 flush 掉 effect，等断言执行时
 * 页面已经自愈了。所以这里把 CardTable 换成记录 props 的桩：**每一次**渲染传给
 * 它的 dataSource 都留档，中间帧就跑不掉了。
 *
 * 与 paginationStability.test.jsx 分开成两个文件，是因为 vi.mock 是文件级的：
 * 那边要断言真实表格渲染出来的行，不能被桩替换掉。
 */

const { tableRenders } = vi.hoisted(() => ({ tableRenders: [] }));

vi.mock('../../../components/common/ui/CardTable', () => ({
  default: (props) => {
    tableRenders.push(props);
    return null;
  },
}));

vi.mock('../../../helpers', async () => {
  const actual = await vi.importActual('../../../helpers');
  return { ...actual, API: { get: vi.fn(), post: vi.fn(), put: vi.fn() } };
});

const ModelRatioEditor = (await import('../components/ModelRatioEditor'))
  .default;
const { API } = await import('../../../helpers');

/** 11 条规则：跨两页，且第二页只有一条——搜索一收窄第二页就不存在了 */
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

/**
 * 精确规则表是传了 pagination 的那个；通配表传的是 hidePagination。
 * 本用例没有通配规则，但按 prop 区分比按渲染顺序取更抗改动。
 */
function exactTableRenders() {
  return tableRenders.filter((p) => p.pagination);
}

describe('分页越界时不闪空表', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    tableRenders.length = 0;
    API.get.mockResolvedValue({ data: { success: true, data: [] } });
  });

  it('搜索把结果收窄到一页时，没有任何一帧的 dataSource 为空', async () => {
    render(<Harness initial={rulesJSON(11)} />);

    // 翻到第二页。表格被替换成桩了，没有真的翻页器可点，直接调它拿到的回调
    const beforeJump = exactTableRenders().at(-1);
    expect(beforeJump).toBeDefined();
    await userEvent.click(document.body); // 让出一个 tick，确保首轮 effect 已结算
    beforeJump.pagination.onPageChange(2);

    // 只关心搜索之后发生的渲染
    tableRenders.length = 0;

    // 逐字符输入 '-01'：敲到第二个字符时结果只剩 9 条（model-01..09），
    // 第二页当场消失——越界就发生在这一次渲染里
    await userEvent.type(screen.getByPlaceholderText('搜索模型或备注'), '-01');

    const renders = exactTableRenders();
    expect(renders.length).toBeGreaterThan(0);

    const emptyFrames = renders.filter(
      (p) => p.dataSource.length === 0 && p.pagination.total > 0,
    );
    expect(emptyFrames).toHaveLength(0);
  });

  it('结果集真的为空时，仍然允许渲染空表', async () => {
    render(<Harness initial={rulesJSON(11)} />);
    tableRenders.length = 0;

    // 搜一个匹配不到任何规则的词：这时候空表是正确的显示，
    // 不能因为上一条断言就把合法的空态也一起禁掉
    await userEvent.type(
      screen.getByPlaceholderText('搜索模型或备注'),
      'zzz-not-exist',
    );

    const last = exactTableRenders().at(-1);
    expect(last.dataSource).toHaveLength(0);
    expect(last.pagination.total).toBe(0);
  });
});
