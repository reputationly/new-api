import React, { useState } from 'react';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import SearchModal from '../SearchModal';
import { getPresetRange } from '../../../../helpers/dashboard';

// 快捷范围必须同时更新「表单显示值」和 hook 里的 inputs。
//
// 只改 inputs 时，Semi 的表单字段仍然显示自己内部的值（withField 把 [valueKey]: value
// 放在 ...rest 之后，传进来的 value 会被内部值覆盖），于是弹窗上显示的区间和确认后
// 实际查询的区间不是同一个——用户看到的和查到的对不上，比不生效更难发现。
//
// 另外，区间只能算一次：applyPreset 返回它，弹窗直接用它。两边各调一次 getPresetRange
// 时，「今天 / 近24小时」的结束时间来自「现在」，跨秒就会让显示值和查询值差 1 秒。

const NOW = new Date('2026-01-15T10:30:00');

const defaultRange = (key) => {
  const range = getPresetRange(key);
  return {
    start_timestamp: range.start_timestamp,
    end_timestamp: range.end_timestamp,
    granularity: range.granularity,
  };
};

function Harness({ applyPresetImpl = defaultRange }) {
  const [inputs, setInputs] = useState({
    start_timestamp: '2026-01-01 00:00:00',
    end_timestamp: '2026-01-02 00:00:00',
    username: '',
  });
  const [granularity, setGranularity] = useState('hour');

  const applyPreset = (key) => {
    if (key === 'custom') {
      return null;
    }
    const range = applyPresetImpl(key);
    setInputs((prev) => ({
      ...prev,
      start_timestamp: range.start_timestamp,
      end_timestamp: range.end_timestamp,
    }));
    setGranularity(range.granularity);
    return range;
  };

  return (
    <SearchModal
      searchModalVisible
      handleSearchConfirm={() => {}}
      handleCloseModal={() => {}}
      isMobile={false}
      isAdminUser={false}
      inputs={inputs}
      dataExportDefaultTime={granularity}
      timeOptions={[
        { key: 'hour', label: '小时', value: 'hour' },
        { key: 'day', label: '天', value: 'day' },
      ]}
      handleInputChange={() => {}}
      applyPreset={applyPreset}
      activePreset=''
      t={(key) => key}
    />
  );
}

describe('搜索弹窗快捷范围', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(NOW);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it('点「近7天」会同时更新起止时间框', () => {
    render(<Harness />);
    const expected = defaultRange('last7d');

    // 点之前：输入框显示的是旧值
    expect(
      screen.queryByDisplayValue(expected.start_timestamp),
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByText('近7天'));

    expect(
      screen.getByDisplayValue(expected.start_timestamp),
    ).toBeInTheDocument();
    expect(
      screen.getByDisplayValue(expected.end_timestamp),
    ).toBeInTheDocument();
  });

  it('点「上个月」按日历月填区间', () => {
    render(<Harness />);
    const expected = defaultRange('lastMonth');

    fireEvent.click(screen.getByText('上个月'));

    expect(
      screen.getByDisplayValue(expected.start_timestamp),
    ).toBeInTheDocument();
    expect(expected.start_timestamp).toContain('2025-12-01');
    expect(expected.end_timestamp).toContain('2025-12-31');
  });

  // 弹窗必须用 applyPreset 返回的那份区间，而不是自己再算一次。
  // 用一个「和真实计算结果不同」的返回值，就能把「各算一次」这种写法钉死。
  it('表单显示的是 applyPreset 返回的区间，不是弹窗自己重算的', () => {
    const sentinel = {
      start_timestamp: '2020-02-02 02:02:02',
      end_timestamp: '2020-03-03 03:03:03',
      granularity: 'day',
    };
    render(<Harness applyPresetImpl={() => sentinel} />);

    fireEvent.click(screen.getByText('近7天'));

    expect(
      screen.getByDisplayValue(sentinel.start_timestamp),
    ).toBeInTheDocument();
    expect(
      screen.getByDisplayValue(sentinel.end_timestamp),
    ).toBeInTheDocument();
    // 若弹窗自己重算，这里会出现真实「近7天」的区间
    const recomputed = defaultRange('last7d');
    expect(
      screen.queryByDisplayValue(recomputed.start_timestamp),
    ).not.toBeInTheDocument();
  });
});
