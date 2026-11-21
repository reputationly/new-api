import { describe, it, expect } from 'vitest';
import { getPresetRange } from '../dashboard';
import { getTodayEndTimestamp, getTodayStartTimestamp } from '../utils';

// 「今天」的时间窗必须是 00:00:00 ~ 23:59:59。
//
// 上游 v1.0 前端原来的默认是 now + 3600（结束时间落在未来），有三个问题：
//   1. 界面把结束时间显示成未来，像是包含未来时段；
//   2. 与看板「今天」预设（原来是 end = now）给出两个不同的值；
//   3. end 按秒变化，任何缓存的 key 都永不重复。
// 后端是纯区间过滤、不会按 end 补零，所以把 end 放到当天末尾是安全的。
describe('今天的时间窗', () => {
  it('结束时刻是当天 23:59:59，不是未来', () => {
    const end = new Date(getTodayEndTimestamp() * 1000);
    expect(end.getHours()).toBe(23);
    expect(end.getMinutes()).toBe(59);
    expect(end.getSeconds()).toBe(59);
    expect(getTodayEndTimestamp()).toBeGreaterThan(getTodayStartTimestamp());
    expect(end.getDate()).toBe(new Date().getDate());
  });

  it('看板「今天」预设与日志页默认区间一致（同为当天末尾）', () => {
    const preset = getPresetRange('today');
    expect(preset.granularity).toBe('hour');
    const startDate = new Date(preset.start_timestamp.replace(' ', 'T'));
    const endDate = new Date(preset.end_timestamp.replace(' ', 'T'));
    expect(startDate.getHours()).toBe(0);
    expect(startDate.getMinutes()).toBe(0);
    expect(endDate.getHours()).toBe(23);
    expect(endDate.getMinutes()).toBe(59);
    expect(endDate.getSeconds()).toBe(59);
  });
});
