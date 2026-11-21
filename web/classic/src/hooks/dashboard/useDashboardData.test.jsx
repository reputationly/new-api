import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useDashboardData } from './useDashboardData';

const mocks = vi.hoisted(() => ({
  get: vi.fn(),
  isAdmin: vi.fn(() => true),
  showError: vi.fn(),
  t: (value) => value,
  navigate: vi.fn(),
}));

vi.mock('react-router-dom', () => ({ useNavigate: () => mocks.navigate }));
vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: mocks.t }) }));
vi.mock('../../helpers', () => ({
  API: { get: mocks.get },
  isAdmin: mocks.isAdmin,
  showError: mocks.showError,
  showWarning: vi.fn(),
  timestamp2string: (seconds) => new Date(seconds * 1000).toISOString(),
  // 默认区间改用「今天 23:59:59」，mock 里要跟着提供，否则 hook 初始化就会抛
  getTodayEndTimestamp: () => 0,
}));
vi.mock('../common/useIsMobile', () => ({
  useIsMobile: () => false,
  MOBILE_BREAKPOINT: 768,
}));
vi.mock('../common/useMinimumLoadingTime', () => ({
  useMinimumLoadingTime: (loading) => loading,
}));

const start = '2026-01-01 00:00:00';
const end = '2026-01-02 00:00:00';
const response = (data) => ({ data: { success: true, data } });
const deferred = () => {
  let resolve, reject;
  const promise = new Promise((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
};

describe('分组消耗请求顺序', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.isAdmin.mockReturnValue(true);
    mocks.get.mockResolvedValue(response({}));
  });

  for (const stale of ['success', 'failure', 'rejection']) {
    it(`忽略晚到的旧请求 ${stale}`, async () => {
      const { result } = renderHook(() => useDashboardData(null, mocks.navigate, null));
      const old = deferred();
      const latest = deferred();
      mocks.get.mockReturnValueOnce(old.promise).mockReturnValueOnce(latest.promise);
      let first, second;
      act(() => {
        first = result.current.loadGroupQuotaData(start, end, 'old');
        second = result.current.loadGroupQuotaData(start, end, 'new');
      });
      await act(async () => {
        latest.resolve(response([{ Group: 'new' }]));
        await second;
      });
      await act(async () => {
        if (stale === 'rejection') old.reject(new Error('old error'));
        else if (stale === 'failure') old.resolve({ data: { success: false, message: 'old error' } });
        else old.resolve(response([{ Group: 'old' }]));
        await first;
      });
      expect(result.current.groupQuotaData).toEqual([{ Group: 'new' }]);
      expect(result.current.groupQuotaLoading).toBe(false);
      expect(mocks.showError).not.toHaveBeenCalled();
    });
  }

  it('旧请求结束不会提前关闭新请求的 loading，最新空结果可清空数据', async () => {
    const { result } = renderHook(() => useDashboardData(null, mocks.navigate, null));
    const old = deferred();
    const latest = deferred();
    mocks.get.mockReturnValueOnce(old.promise).mockReturnValueOnce(latest.promise);
    let first, second;
    act(() => {
      first = result.current.loadGroupQuotaData(start, end);
      second = result.current.loadGroupQuotaData(start, end);
    });
    await act(async () => { old.resolve(response([{ Group: 'old' }])); await first; });
    expect(result.current.groupQuotaLoading).toBe(true);
    expect(result.current.groupQuotaData).toEqual([]);
    await act(async () => { latest.resolve(response(null)); await second; });
    expect(result.current.groupQuotaLoading).toBe(false);
    expect(result.current.groupQuotaData).toEqual([]);
  });

  it('最新失败清空数据并显示错误', async () => {
    const { result } = renderHook(() => useDashboardData(null, mocks.navigate, null));
    mocks.get.mockResolvedValueOnce(response([{ Group: 'old' }]));
    await act(async () => { await result.current.loadGroupQuotaData(start, end); });
    mocks.get.mockResolvedValueOnce({ data: { success: false, message: 'failed' } });
    await act(async () => { await result.current.loadGroupQuotaData(start, end); });
    expect(result.current.groupQuotaData).toEqual([]);
    expect(result.current.groupQuotaLoading).toBe(false);
    expect(mocks.showError).toHaveBeenCalledWith('failed');
  });
});
