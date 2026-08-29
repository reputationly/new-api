import React from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';

import EditModelModal, {
  describeVisibleGroups,
  serializeVisibleGroups,
} from '../modals/EditModelModal';
import { API } from '../../../../helpers';

vi.mock('../../../../helpers', async () => {
  const actual = await vi.importActual('../../../../helpers');
  return {
    ...actual,
    API: { get: vi.fn(), post: vi.fn(), put: vi.fn() },
    showError: vi.fn(),
    showSuccess: vi.fn(),
  };
});

/**
 * 「仅这些用户档可用」是白名单：填谁谁能用。
 *
 * 这个语义会被读反——字段往那一放，第一反应是填「要屏蔽的档」。现网就填反过一次：
 * 想挡住 geostar 和 free，结果填的正是这两个，等于把零毛利模型专门开放给了
 * ratio 最低的两个分组。填反不报错、不留痕，只有对账毛利异常时才可能发现。
 *
 * 所以界面上直接把补集算出来显示。这里测的就是那个补集。
 */

const ALL = ['default', 'premium', 'geostar', 'free', 'vip'];

describe('describeVisibleGroups', () => {
  it('留空 = 不限制，没有任何档被排除', () => {
    expect(describeVisibleGroups(ALL, [])).toEqual({
      unrestricted: true,
      excluded: [],
    });
    expect(describeVisibleGroups(ALL, undefined).unrestricted).toBe(true);
  });

  it('填了就算补集：没列出的档会被挡', () => {
    const { unrestricted, excluded } = describeVisibleGroups(ALL, [
      'default',
      'premium',
      'vip',
    ]);
    expect(unrestricted).toBe(false);
    expect(excluded).toEqual(['geostar', 'free']);
  });

  it('填反了也如实显示——这正是它要暴露的那个错误', () => {
    // 运营想挡住 geostar/free，却把它们填进了白名单
    const { excluded } = describeVisibleGroups(ALL, ['geostar', 'free']);
    // 提示会明确写出「default、premium、vip 将无法使用」，与意图相反，一眼可辨
    expect(excluded).toEqual(['default', 'premium', 'vip']);
  });

  it('全部档都列出时等同于不限制，但仍是受限状态', () => {
    const { unrestricted, excluded } = describeVisibleGroups(ALL, ALL);
    expect(unrestricted).toBe(false);
    expect(excluded).toEqual([]);
  });

  it('忽略空白项，避免误判成「已限制」', () => {
    // TagInput 的 addOnBlur 可能带进空串
    expect(describeVisibleGroups(ALL, ['', '  ']).unrestricted).toBe(true);
  });

  /**
   * 上一条只说了界面怎么显示。真正危险的是显示与提交不一致：
   * 界面把空白当没填（不限制），而 ['', '  '].join(',') = ',  ' 传到后端，
   * parseVisibleGroups(model/model_visibility.go:85) 先 TrimSpace 再判空——
   * 逗号还在，非空，于是落进「配了但一个档都没勾」，语义是**谁都看不到**。
   *
   * 两句话说的是相反的事，模型会静默地对所有人消失。
   */
  it('提交时滤掉空白项，不能拼出一个只剩逗号的串', () => {
    expect(serializeVisibleGroups(['', '  '])).toBe('');
    expect(serializeVisibleGroups(['  ', 'default', ''])).toBe('default');
    // 与 describeVisibleGroups 的判断保持一致：界面说不限制，提交的就得是不限制
    expect(describeVisibleGroups(ALL, ['', '  ']).unrestricted).toBe(true);
    expect(serializeVisibleGroups(['', '  '])).toBe('');
  });

  it('正常值原样保留顺序', () => {
    expect(serializeVisibleGroups(['default', 'premium', 'vip'])).toBe(
      'default,premium,vip',
    );
  });

  it('分组列表拿不到时不报错，只是算不出补集', () => {
    const { unrestricted, excluded } = describeVisibleGroups([], ['default']);
    expect(unrestricted).toBe(false);
    expect(excluded).toEqual([]);
  });
});

/**
 * 上面全是纯函数用例——它们无法证明界面真的用上了这个函数。
 * 把 extraText 改回旧的静态文案，上面 6 条依然全绿，而运营看到的还是那个
 * 需要在脑子里做减法的提示。所以必须有一条打到组件上的断言。
 */
describe('EditModelModal 把补集显示出来', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    API.get.mockImplementation((url) => {
      if (url.startsWith('/api/group/')) {
        return Promise.resolve({ data: { success: true, data: ALL } });
      }
      return Promise.resolve({ data: { success: true, data: [] } });
    });
  });

  it('新建（白名单为空）时提示不限制', async () => {
    render(
      <EditModelModal
        visiable
        editingModel={{}}
        refresh={() => {}}
        handleClose={() => {}}
      />,
    );

    // 这句只可能来自 describeVisibleGroups 的 unrestricted 分支；
    // 改回旧的静态 extraText 文案，这条就红
    expect(
      await screen.findByText(
        '留空表示不限制，所有用户档都能看到并调用此模型。',
      ),
    ).toBeInTheDocument();
  });

  it('已配白名单的模型，打开就列出被挡的档', async () => {
    API.get.mockImplementation((url) => {
      if (url.startsWith('/api/group/')) {
        return Promise.resolve({ data: { success: true, data: ALL } });
      }
      if (url === '/api/models/7') {
        return Promise.resolve({
          data: {
            success: true,
            data: {
              id: 7,
              model_name: 'Kimi-K3',
              visible_groups: 'default,premium,vip',
              status: 1,
            },
          },
        });
      }
      return Promise.resolve({ data: { success: true, data: [] } });
    });

    render(
      <EditModelModal
        visiable
        editingModel={{ id: 7, model_name: 'Kimi-K3' }}
        refresh={() => {}}
        handleClose={() => {}}
      />,
    );

    // 文案与补集被渲染成两个相邻文本节点，findByText 匹配不到整串，只能读容器
    await waitFor(() => {
      expect(document.body.textContent).toContain(
        '以下用户档将无法使用，会得到「模型不存在」：geostar、free',
      );
    });
  });
});
