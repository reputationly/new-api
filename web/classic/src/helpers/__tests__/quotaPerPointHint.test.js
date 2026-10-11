import { describe, expect, it, beforeEach } from 'vitest';
import { quotaPerPointHintText } from '../quota';

// 后台换算率输入框的说明文案必须显示**当前生效值**：写死 1000000 会让管理员在单位切换后
// 照着填回去，把 1 积分从 1 分钱改成一个错的数。
describe('quotaPerPointHintText', () => {
  beforeEach(() => {
    localStorage.clear();
  });

  it('显示当前生效的 1000000（1 积分 = 1 分钱）', () => {
    localStorage.setItem('quota_per_point', '1000000');
    const text = quotaPerPointHintText();
    expect(text).toContain('1000000');
    expect(text).toContain('0 = 自动');
    expect(text).not.toContain('{{value}}');
  });

  it('显示当前生效的 2000000（单位切换后）', () => {
    localStorage.setItem('quota_per_point', '2000000');
    const text = quotaPerPointHintText();
    expect(text).toContain('2000000');
    expect(text).toContain('0 = 自动');
  });

  it('读不到配置时退回 fallback 并仍给出数值', () => {
    const text = quotaPerPointHintText();
    expect(text).toContain('1000000');
  });
});
