import React, { useCallback, useState } from 'react';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';

import ModelRatioEditor, {
  buildModeOptions,
} from '../components/ModelRatioEditor';
import { API } from '../../../helpers';

/**
 * ModelRatioEditor 被「档位折扣」复用时的三个差异点（设计 §8.2）。
 *
 * 这个组件同时承载分组折扣与档位折扣两套配置，参数给错不会报错，只会静默写坏价：
 * 模型下拉取错数据源就配不出别的分组的模型，override 没禁掉就会吃掉上游成本信息。
 */

vi.mock('../../../helpers', async () => {
  const actual = await vi.importActual('../../../helpers');
  return { ...actual, API: { get: vi.fn(), post: vi.fn(), put: vi.fn() } };
});

function TierHarness({ initial, onValue, ...overrides }) {
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
      group='vip'
      groupRatio={1}
      value={value}
      onChange={onChange}
      modelsEndpoint='/api/group/models'
      allowOverride={false}
      {...overrides}
    />
  );
}

beforeEach(() => {
  API.get.mockResolvedValue({ data: { success: true, data: [] } });
});

describe('ModelRatioEditor 复用为档位折扣', () => {
  it('模型下拉走全站接口，不按分组过滤', async () => {
    render(<TierHarness initial='{}' />);

    await waitFor(() => expect(API.get).toHaveBeenCalled());
    // 档位折扣按用户档索引、与供应链无关；带 group 参数会让运营配不出挂在
    // 别的分组上的模型
    expect(API.get).toHaveBeenCalledWith('/api/group/models');
  });

  it('不传 modelsEndpoint 时仍按分组取模型（分组折扣的原行为不变）', async () => {
    render(
      <TierHarness initial='{}' modelsEndpoint={undefined} group='premium' />,
    );

    await waitFor(() => expect(API.get).toHaveBeenCalled());
    expect(API.get).toHaveBeenCalledWith('/api/group/models?group=premium');
  });

  it('allowOverride=false 时模式选项只剩「折扣 ×」', () => {
    const t = (k) => k;

    expect(buildModeOptions(false, t).map((o) => o.value)).toEqual([
      'multiply',
    ]);
    expect(buildModeOptions(true, t).map((o) => o.value)).toEqual([
      'multiply',
      'override',
    ]);
  });

  it('写回的规则挂在用户档下，不串到别的档', async () => {
    const seen = [];
    render(
      <TierHarness
        initial={JSON.stringify({
          vip: { 'GLM-5': { mode: 'multiply', value: 0.6 } },
          geostar: { '*': { mode: 'multiply', value: 0.9 } },
        })}
        onValue={(v) => seen.push(v)}
      />,
    );

    const user = userEvent.setup();
    await user.type(await screen.findByDisplayValue('GLM-5'), '3');

    await waitFor(() => expect(seen.length).toBeGreaterThan(0));
    const written = JSON.parse(seen[seen.length - 1]);

    expect(written.geostar).toEqual({
      '*': { mode: 'multiply', value: 0.9 },
    });
    expect(Object.keys(written.vip)).toEqual(['GLM-53']);
  });
});
