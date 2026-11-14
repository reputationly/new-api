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

const NOW = new Date('2026-01-15T10:30:00');

function Harness() {
  const [inputs, setInputs] = useState({
    start_timestamp: '2026-01-01 00:00:00',
    end_timestamp: '2026-01-02 00:00:00',
    username: '',
  });
  const [granularity, setGranularity] = useState('hour');

  const applyPreset = (key) => {
    if (key === 'custom') return;
    const range = getPresetRange(key);
    setInputs((prev) => ({
      ...prev,
      start_timestamp: range.start_timestamp,
      end_timestamp: range.end_timestamp,
    }));
    setGranularity(range.granularity);
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
    const expected = getPresetRange('last7d');

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
    const expected = getPresetRange('lastMonth');

    fireEvent.click(screen.getByText('上个月'));

    expect(
      screen.getByDisplayValue(expected.start_timestamp),
    ).toBeInTheDocument();
    expect(expected.start_timestamp).toContain('2025-12-01');
    expect(expected.end_timestamp).toContain('2025-12-31');
  });
});
